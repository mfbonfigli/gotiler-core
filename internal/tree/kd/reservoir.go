package kd

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"

	"github.com/mfbonfigli/gotiler-core/internal/utils"
	coor "github.com/mfbonfigli/gotiler-core/tiler/coord"
	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/mutator"
	"github.com/mfbonfigli/gotiler-core/tiler/pointcloud"
	"github.com/mfbonfigli/gotiler-core/tiler/tree"
)

const pipelineBatchSize = 16384

// progressReportInterval is the number of points between intermediate progress updates.
const progressReportInterval = 1_000_000

// reservoirLoaderResult holds the output of the reservoir sampling pipeline.
type reservoirLoaderResult struct {
	sample        []model.Point    // reservoir sample in local coordinates
	bounds        geom.BoundingBox // global bounds from ALL points
	totalPoints   int
	localToGlobal model.Transform
	tempFilePath  string // path to temporary binary file with all converted points
}

// reservoirLoader reads LAS files, converts coordinates and stores them
// into a temp file and extracts a statistically uniform sample via reservoir sampling
// to be used to build a KD-tree.
//
// A reservoirLoader instance is safe for one call to Run; create a new one
// for each pipeline execution.
type reservoirLoader struct {
	convFactory   coor.ConverterFactory
	mut           mutator.Mutator
	numWorkers    int
	reservoirSize int
	tempFolder    string
	ioFactory     IoFactory

	rawBatchPool   *utils.SlicePool[geom.Point64]
	localBatchPool *utils.SlicePool[model.Point]
	flatCoordsPool *utils.SlicePool[float64]
}

// NewReservoirLoader creates a configured loader.
func NewReservoirLoader(
	convFactory coor.ConverterFactory,
	mut mutator.Mutator,
	reservoirSize int,
	numWorkers int,
	tempFolder string,
	ioFactory IoFactory,
) *reservoirLoader {
	return &reservoirLoader{
		convFactory:    convFactory,
		mut:            mut,
		numWorkers:     numWorkers,
		reservoirSize:  reservoirSize,
		tempFolder:     tempFolder,
		ioFactory:      ioFactory,
		rawBatchPool:   utils.NewSlicePool[geom.Point64](pipelineBatchSize),
		localBatchPool: utils.NewSlicePool[model.Point](pipelineBatchSize),
		flatCoordsPool: utils.NewSlicePool[float64](pipelineBatchSize * 3),
	}
}

// Run reads the LAS file, extracts a sample for the KD tree structure,
// converts the points to local CRS and saves them to a temporary file.
//
// It uses a concurrent producer-consumer pipeline:
//   - 1 producer reads LAS points sequentially and groups them into batches
//   - N workers convert CRS→ECEF→local in parallel per batch (the expensive PROJ call)
//   - 1 collector writes all converted batches to a temp file and reservoir-samples
//
// reporter receives progress updates for the "reading" phase; pass nil to suppress.
func (s *reservoirLoader) Run(reader pointcloud.Reader, ctx context.Context, reporter tree.ProgressReporter) (*reservoirLoaderResult, error) {
	if s.reservoirSize <= 0 {
		return nil, fmt.Errorf("reservoir size must be > 0, got %d", s.reservoirSize)
	}

	// Total point count from the LAS header; used for percent calculation.
	// May be 0 for files with an unknown header count.
	totalPointsHint := reader.NumberOfPoints()

	tree.ReportProgress(reporter, tree.ProgressUpdate{
		Phase:       "reading",
		Percent:     0,
		Message:     fmt.Sprintf("reading %d points", totalPointsHint),
		IsMilestone: true,
		ItemCount:   0,
		ItemTotal:   int64(totalPointsHint), // 0 when header count unknown → percent-based fallback
	})

	c, err := s.convFactory()
	if err != nil {
		return nil, err
	}
	defer c.Cleanup()

	localToGlobal, firstLocalPt, readErr := baseline(reader, c, s.mut)
	if readErr != nil {
		return nil, readErr
	}

	tmpFile, err := os.CreateTemp(s.tempFolder, "points_*.bin")
	if err != nil {
		return nil, fmt.Errorf("kdbu: failed to create temp file: %w", err)
	}
	name := tmpFile.Name()
	tmpFile.Close()

	writer, err := s.ioFactory.NewWriter(name)
	if err != nil {
		_ = os.Remove(name)
		return nil, fmt.Errorf("kdbu: failed to create writer: %w", err)
	}

	rawCh := make(chan *[]geom.Point64, s.numWorkers*2)
	localCh := make(chan *[]model.Point, s.numWorkers*2)
	errCh := make(chan error, s.numWorkers+2)
	var sourcePointsRead atomic.Int64

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sourceCRS := reader.GetCRS()

	// Local Random Generator for Deterministic Samplings
	localRand := rand.New(rand.NewPCG(42, 42))

	// Producer
	var producerWg sync.WaitGroup
	producerWg.Add(1)
	go func() {
		defer producerWg.Done()
		defer close(rawCh)

		bufPtr := s.rawBatchPool.Get()
		*bufPtr = (*bufPtr)[:0]

		for {
			pt, err := reader.GetNext()
			if err != nil {
				if len(*bufPtr) > 0 {
					select {
					case rawCh <- bufPtr:
					case <-subCtx.Done():
						s.rawBatchPool.Put(bufPtr)
						return
					}
				} else {
					s.rawBatchPool.Put(bufPtr)
				}
				if err == io.EOF {
					return
				}
				select {
				case errCh <- fmt.Errorf("reservoir: read failed: %w", err):
				default:
				}
				return
			}
			sourcePointsRead.Add(1)

			*bufPtr = append(*bufPtr, pt)

			if len(*bufPtr) >= pipelineBatchSize {
				select {
				case rawCh <- bufPtr:
				case <-subCtx.Done():
					s.rawBatchPool.Put(bufPtr)
					return
				}
				bufPtr = s.rawBatchPool.Get()
				*bufPtr = (*bufPtr)[:0]
			}
		}
	}()

	// Workers
	var workerWg sync.WaitGroup
	for i := 0; i < s.numWorkers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			conv, err := s.convFactory()
			if err != nil {
				select {
				case errCh <- fmt.Errorf("reservoir: converter creation failed: %w", err):
				default:
				}
				cancel()
				return
			}
			defer conv.Cleanup()

			// To prevent deadlocks, always drain the channel entirely even if canceled
			for rawBatchPtr := range rawCh {
				if subCtx.Err() != nil {
					s.rawBatchPool.Put(rawBatchPtr)
					continue
				}

				n := len(*rawBatchPtr)
				localBatchPtr := s.localBatchPool.Get()
				*localBatchPtr = (*localBatchPtr)[:0]

				flatCoordsPtr := s.flatCoordsPool.GetWithMinCapacity(n * 3)
				// Explicitly reslice to length (n*3) to prevent index out of bounds panics
				*flatCoordsPtr = (*flatCoordsPtr)[:n*3]

				for i, rawPt := range *rawBatchPtr {
					offset := i * 3
					(*flatCoordsPtr)[offset] = rawPt.X
					(*flatCoordsPtr)[offset+1] = rawPt.Y
					(*flatCoordsPtr)[offset+2] = rawPt.Z
				}

				if err := conv.ToWGS84CartesianFlat(sourceCRS, *flatCoordsPtr); err != nil {
					s.flatCoordsPool.Put(flatCoordsPtr)
					s.localBatchPool.Put(localBatchPtr)
					s.rawBatchPool.Put(rawBatchPtr)
					select {
					case errCh <- fmt.Errorf("reservoir: conversion failed: %w", err):
					default:
					}
					cancel()
					continue
				}

				for i, rawPt := range *rawBatchPtr {
					offset := i * 3
					ecefPt := geom.Point64{
						Vector: model.Vector{
							X: (*flatCoordsPtr)[offset],
							Y: (*flatCoordsPtr)[offset+1],
							Z: (*flatCoordsPtr)[offset+2],
						},
						R:               rawPt.R,
						G:               rawPt.G,
						B:               rawPt.B,
						Intensity:       rawPt.Intensity,
						Classification:  rawPt.Classification,
						ReturnNumber:    rawPt.ReturnNumber,
						NumberOfReturns: rawPt.NumberOfReturns,
					}
					localPt := toLocalPoint(ecefPt, localToGlobal)
					keep := true
					if s.mut != nil {
						localPt, keep = s.mut.Mutate(localPt, localToGlobal)
						if !keep {
							continue
						}
					}
					*localBatchPtr = append(*localBatchPtr, localPt)
				}

				s.flatCoordsPool.Put(flatCoordsPtr)
				s.rawBatchPool.Put(rawBatchPtr)

				if len(*localBatchPtr) > 0 {
					select {
					case localCh <- localBatchPtr:
					case <-subCtx.Done():
						s.localBatchPool.Put(localBatchPtr)
					}
				} else {
					s.localBatchPool.Put(localBatchPtr)
				}
			}
		}()
	}

	// Collector
	var collectorWg sync.WaitGroup
	collectorWg.Add(1)
	var collectErr error
	var sample []model.Point
	totalPoints := 0
	bounds := geom.BoundingBox{
		Xmin: math.Inf(1), Xmax: math.Inf(-1),
		Ymin: math.Inf(1), Ymax: math.Inf(-1),
		Zmin: math.Inf(1), Zmax: math.Inf(-1),
	}
	sample = make([]model.Point, 0, s.reservoirSize)

	go func() {
		defer collectorWg.Done()

		var lastReportedSource int64 // source points read at the last progress update

		if err := writer.WriteBatch([]model.Point{firstLocalPt}); err != nil {
			collectErr = err
			cancel()
		} else {
			totalPoints = 1
			updateBounds(&bounds, firstLocalPt)
			sample = append(sample, firstLocalPt)
		}

		for localBatchPtr := range localCh {
			if subCtx.Err() != nil {
				s.localBatchPool.Put(localBatchPtr)
				continue // Keep draining channel to avoid blocking workers
			}

			if err := writer.WriteBatch(*localBatchPtr); err != nil {
				collectErr = err
				s.localBatchPool.Put(localBatchPtr)
				cancel()
				continue
			}

			for _, localPt := range *localBatchPtr {
				totalPoints++
				updateBounds(&bounds, localPt)

				if totalPoints <= s.reservoirSize {
					sample = append(sample, localPt)
				} else {
					// Use deterministic local generator instance
					j := localRand.IntN(totalPoints)
					if j < s.reservoirSize {
						sample[j] = localPt
					}
				}
			}
			// Return the batch to the pool before any further work. totalPoints is
			// already correct at this point, so use it (not len(*localBatchPtr))
			// for the threshold check to avoid reading the slice after the Put.
			s.localBatchPool.Put(localBatchPtr)

			// Emit a throttled progress update every progressReportInterval source points.
			sourceRead := sourcePointsRead.Load()
			if sourceRead-lastReportedSource >= progressReportInterval {
				lastReportedSource = sourceRead
				pct := -1.0
				if totalPointsHint > 0 {
					pct = math.Min(99, float64(sourceRead)/float64(totalPointsHint)*100)
				}
				tree.ReportProgress(reporter, tree.ProgressUpdate{
					Phase:     "reading",
					Percent:   pct,
					Message:   fmt.Sprintf("read %d points", sourceRead),
					ItemCount: sourceRead,
					ItemTotal: int64(totalPointsHint),
				})
			}
		}
	}()

	producerWg.Wait()
	workerWg.Wait()
	close(localCh)
	collectorWg.Wait()
	close(errCh)

	// Catch any worker or producer error signals
	for e := range errCh {
		if e != nil {
			_ = writer.Close()
			_ = os.Remove(name)
			return nil, e
		}
	}

	if collectErr != nil {
		_ = writer.Close()
		_ = os.Remove(name)
		return nil, collectErr
	}

	if err := writer.Close(); err != nil {
		_ = os.Remove(name)
		return nil, err
	}

	// If the parent context was cancelled while goroutines were draining,
	// partial data must not be treated as a successful read.
	if err := ctx.Err(); err != nil {
		_ = os.Remove(name)
		return nil, err
	}

	if totalPoints == 0 {
		_ = os.Remove(name)
		return nil, fmt.Errorf("no valid points found")
	}

	tree.ReportProgress(reporter, tree.ProgressUpdate{
		Phase:       "reading",
		Percent:     100,
		Message:     fmt.Sprintf("read %d points", sourcePointsRead.Load()),
		IsMilestone: true,
		ItemCount:   sourcePointsRead.Load(),
		ItemTotal:   int64(totalPointsHint),
	})

	return &reservoirLoaderResult{
		sample:        sample,
		bounds:        bounds,
		totalPoints:   totalPoints,
		localToGlobal: localToGlobal,
		tempFilePath:  name,
	}, nil
}

// baseline returns the local-to-global transform for the first valid point, and the point in local coords.
func baseline(reader pointcloud.Reader, c coor.Converter, mut mutator.Mutator) (model.Transform, model.Point, error) {
	sourceCRS := reader.GetCRS()
	for {
		first, err := reader.GetNext()
		if err != nil {
			return model.Transform{}, model.Point{}, err
		}
		ecefPt, err := transformToECEF(c, first, sourceCRS)
		if err != nil {
			return model.Transform{}, model.Point{}, err
		}
		localToGlobal := geom.LocalToGlobalTransformFromPoint(ecefPt.X, ecefPt.Y, ecefPt.Z)
		localPt := toLocalPoint(ecefPt, localToGlobal)
		keep := true
		if mut != nil {
			localPt, keep = mut.Mutate(localPt, localToGlobal)
			if !keep {
				continue
			}
		}
		return localToGlobal, localPt, nil
	}
}

func toLocalPoint(ecefPt geom.Point64, l2g model.Transform) model.Point {
	localCoords := l2g.Inverse(ecefPt.Vector)
	return model.Point{
		X:               float32(localCoords.X),
		Y:               float32(localCoords.Y),
		Z:               float32(localCoords.Z),
		R:               ecefPt.R,
		G:               ecefPt.G,
		B:               ecefPt.B,
		Intensity:       ecefPt.Intensity,
		Classification:  ecefPt.Classification,
		ReturnNumber:    ecefPt.ReturnNumber,
		NumberOfReturns: ecefPt.NumberOfReturns,
	}
}

func transformToECEF(c coor.Converter, pt geom.Point64, sourceCRS string) (geom.Point64, error) {
	out, err := c.ToWGS84Cartesian(sourceCRS, pt.Vector)
	if err != nil {
		return pt, err
	}
	pt.X = out.X
	pt.Y = out.Y
	pt.Z = out.Z
	return pt, nil
}

func updateBounds(b *geom.BoundingBox, p model.Point) {
	x := float64(p.X)
	y := float64(p.Y)
	z := float64(p.Z)
	if x < b.Xmin {
		b.Xmin = x
	}
	if x > b.Xmax {
		b.Xmax = x
	}
	if y < b.Ymin {
		b.Ymin = y
	}
	if y > b.Ymax {
		b.Ymax = y
	}
	if z < b.Zmin {
		b.Zmin = z
	}
	if z > b.Zmax {
		b.Zmax = z
	}
}
