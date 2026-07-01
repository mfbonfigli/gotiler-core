package kd

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"sync/atomic"

	"github.com/mfbonfigli/gotiler-core/internal/utils"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

const (
	pointRecordSize = 20 // X,Y,Z float32 (12) + R,G,B uint8 (3) + Intensity uint16 (2) + Classification uint8 (1) + ReturnNumber uint8 (1) + NumberOfReturns uint8 (1)
	pointsPerChunk  = 4096
	chunkBufferSize = pointsPerChunk * pointRecordSize // ~80KB sequential blocks
)

// scartchPool provides a shared pool of buffer to read chunks of points
var scratchPool = utils.NewSlicePool[byte](pointRecordSize)

// PointReader reads point records from a backing store.
type PointReader interface {
	NumPoints() int
	NextBatch(dest []model.Point) ([]model.Point, error)
	Close() error
}

// PointWriter writes point records to a backing store.
type PointWriter interface {
	WriteBatch(points []model.Point) error
	Close() error
}

// IoFactory creates PointReader and PointWriter instances for
// the KD-tree's intermediate point storage. Swap with a custom
// implementation to redirect point I/O.
type IoFactory interface {
	NewReader(filePath string) (PointReader, error)
	NewWriter(name string) (PointWriter, error)
}

// fileIoFactory is the default IoFactory backed by disk files.
type fileIoFactory struct{}

// NewFileIoFactory returns the default file-based IoFactory.
func NewFileIoFactory() IoFactory {
	return &fileIoFactory{}
}

func (f *fileIoFactory) NewReader(filePath string) (PointReader, error) {
	return NewFilePointReader(filePath)
}

func (f *fileIoFactory) NewWriter(name string) (PointWriter, error) {
	return NewFilePointWriter(name)
}

// filePointReader reads 32-byte point records from a raw binary file efficiently.
// It assigns large, sequential 256 KB blocks to calling goroutines to preserve
// the OS filesystem's native read-ahead caching mechanism.
type filePointReader struct {
	file      *os.File
	numPoints int64
	nextChunk atomic.Int64
	closed    atomic.Bool
	chunkPool *utils.SlicePool[byte]
}

// NewFilePointReader opens the given file and computes numPoints from the file size.
func NewFilePointReader(filePath string) (PointReader, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	return &filePointReader{
		file:      f,
		numPoints: info.Size() / pointRecordSize,
		chunkPool: utils.NewSlicePool[byte](chunkBufferSize),
	}, nil
}

// NumPoints returns the total number of points in the file.
func (r *filePointReader) NumPoints() int {
	return int(r.numPoints)
}

// NextBatch reads up to pointsPerChunk points into a provided destination slice in a single sequential IO block.
// This completely bypasses item-by-item atomic contention. Returns the slice and io.EOF when done.
func (r *filePointReader) NextBatch(dest []model.Point) ([]model.Point, error) {
	dest = dest[:0]
	if r.closed.Load() {
		return dest, io.EOF
	}

	// Claim a large sequential block atomically
	chunkIdx := r.nextChunk.Add(1) - 1
	startPoint := chunkIdx * pointsPerChunk
	if startPoint >= r.numPoints {
		return dest, io.EOF
	}

	endPoint := startPoint + pointsPerChunk
	if endPoint > r.numPoints {
		endPoint = r.numPoints
	}
	pointsToRead := endPoint - startPoint

	bufPtr := r.chunkPool.GetWithMinCapacity(int(pointsToRead) * pointRecordSize)
	defer r.chunkPool.Put(bufPtr)
	buf := (*bufPtr)[:int(pointsToRead)*pointRecordSize]

	// Large sequential disk read
	offset := startPoint * pointRecordSize
	if _, err := r.file.ReadAt(buf, offset); err != nil && err != io.EOF {
		return dest, err
	}

	// Parse points sequentially out of the memory chunk
	for i := int64(0); i < pointsToRead; i++ {
		pIdx := i * pointRecordSize
		dest = append(dest, model.Point{
			X:               math.Float32frombits(binary.LittleEndian.Uint32(buf[pIdx : pIdx+4])),
			Y:               math.Float32frombits(binary.LittleEndian.Uint32(buf[pIdx+4 : pIdx+8])),
			Z:               math.Float32frombits(binary.LittleEndian.Uint32(buf[pIdx+8 : pIdx+12])),
			R:               buf[pIdx+12],
			G:               buf[pIdx+13],
			B:               buf[pIdx+14],
			Intensity:       binary.LittleEndian.Uint16(buf[pIdx+15 : pIdx+17]),
			Classification:  buf[pIdx+17],
			ReturnNumber:    buf[pIdx+18],
			NumberOfReturns: buf[pIdx+19],
		})
	}

	var returnErr error
	if endPoint >= r.numPoints {
		returnErr = io.EOF
	}
	return dest, returnErr
}

// Close closes the underlying file descriptor.
func (r *filePointReader) Close() error {
	if r.closed.CompareAndSwap(false, true) {
		return r.file.Close()
	}
	return nil
}

// filePointWriter writes raw 18-byte point records to a file using bulk memory blocks.
// All methods are safe for concurrent use.
type filePointWriter struct {
	file *os.File
	bw   *bufio.Writer
	mu   sync.Mutex
	// scratch is an embedded array allocated once per writer lifecycle.
	scratch [pointRecordSize]byte
}

// NewFilePointWriter creates a buffered writer using an optimized 256 KB streaming layout.
func NewFilePointWriter(name string) (PointWriter, error) {
	// Open with append mode. If the file already exists, we stream to the end of it.
	f, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", name, err)
	}

	return &filePointWriter{
		file: f,
		bw:   bufio.NewWriterSize(f, chunkBufferSize),
	}, nil
}

// WriteBatch serializes and flushes an entire collection of points under a single lock transaction.
func (w *filePointWriter) WriteBatch(points []model.Point) error {
	if len(points) == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for _, pt := range points {
		// Serialize floats into LittleEndians using our zero-alloc internal scratch slice
		binary.LittleEndian.PutUint32(w.scratch[0:4], math.Float32bits(pt.X))
		binary.LittleEndian.PutUint32(w.scratch[4:8], math.Float32bits(pt.Y))
		binary.LittleEndian.PutUint32(w.scratch[8:12], math.Float32bits(pt.Z))

		// Map remaining structural attributes
		w.scratch[12] = pt.R
		w.scratch[13] = pt.G
		w.scratch[14] = pt.B
		binary.LittleEndian.PutUint16(w.scratch[15:17], pt.Intensity)
		w.scratch[17] = pt.Classification
		w.scratch[18] = pt.ReturnNumber
		w.scratch[19] = pt.NumberOfReturns

		// Stream to the bufio block buffer
		if _, err := w.bw.Write(w.scratch[:]); err != nil {
			return fmt.Errorf("point writer failed during batch streaming: %w", err)
		}
	}

	return nil
}

// Close flushes the system buffer and terminates the active file descriptor.
// The file is guaranteed to close even if the underlying flush operation fails.
func (w *filePointWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	flushErr := w.bw.Flush()
	closeErr := w.file.Close()
	if flushErr != nil {
		return fmt.Errorf("failed to flush buffer before closing: %w", flushErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close file descriptor: %w", closeErr)
	}

	return nil
}
