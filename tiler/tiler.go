package tiler

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mfbonfigli/gotiler-core/internal/conv/coor/proj"
	"github.com/mfbonfigli/gotiler-core/internal/pc"
	"github.com/mfbonfigli/gotiler-core/internal/tree/kd"
	"github.com/mfbonfigli/gotiler-core/internal/utils"
	"github.com/mfbonfigli/gotiler-core/internal/writer"
	coor "github.com/mfbonfigli/gotiler-core/tiler/coord"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/mutator"
	"github.com/mfbonfigli/gotiler-core/tiler/pointcloud"
	"github.com/mfbonfigli/gotiler-core/tiler/tree"
)

type Tiler interface {
	ProcessFiles(inputFiles []string, outputFolder string, sourceCRS string, opts *TilerOptions, ctx context.Context) error
	ProcessFolder(inputFolder, outputFolder string, sourceCRS string, opts *TilerOptions, ctx context.Context) error
}

// GoTiler wraps the logic required to convert
// point clouds into OGC 3D tiles
type GoTiler struct {
	convFactory  coor.ConverterFactory
	treeProvider TreeProvider
	writerProvider
	pointcloudReaderProvider
}

// TreeProvider creates a spatial hierarchy implementation for a tiling run.
type TreeProvider func(opts tree.Options, output string) tree.Tree

type writerProvider func(folder string, opts *TilerOptions) (writer.Writer, error)
type pointcloudReaderProvider func(inputFiles []string, sourceCRS string, eightbit bool, attrs model.Attributes) (pointcloud.Reader, error)

// effectivePointsPerTile returns the internal points-per-tile threshold to use when building
// the tree. In ADD refine mode, bubble-up halves the points in each tile, so the threshold is
// doubled so that tiles on disk end up containing the user-configured value.
func effectivePointsPerTile(opts *TilerOptions) int {
	if opts.refineMode == model.RefineAdd {
		return opts.PointsPerTile * 2
	}
	return opts.PointsPerTile
}

func treeOptions(opts *TilerOptions, inputAttributes model.Attributes) tree.Options {
	return tree.Options{
		NumWorkers:            opts.numWorkers,
		PointsPerTile:         effectivePointsPerTile(opts),
		RefineMode:            opts.refineMode,
		InitialGeometricError: opts.initialGeometricError,
		Attributes:            inputAttributes,
		OutputAttributes:      opts.attributes,
	}
}

func mergeAttributes(attrSets ...model.Attributes) model.Attributes {
	var names []string
	for _, attrs := range attrSets {
		names = append(names, attrs.Names()...)
	}
	return model.NewAttributes(names...)
}

// NewGoTiler returns a new tiler to be used to convert Point Cloud files into OGC 3D Tiles
func NewGoTiler() (*GoTiler, error) {
	return &GoTiler{
		convFactory: func() (coor.Converter, error) {
			return proj.NewProjCoordinateConverter()
		},
		treeProvider: func(opts tree.Options, output string) tree.Tree {
			return kd.NewTree(
				kd.WithNumWorkers(opts.NumWorkers),
				kd.WithPointsPerTile(opts.PointsPerTile),
				kd.WithRefineMode(opts.RefineMode),
				kd.WithAttributes(opts.Attributes),
				kd.WithOutputAttributes(opts.OutputAttributes),
				kd.WithDataFolder(output),
				kd.WithRootTargetGeomErr(opts.InitialGeometricError),
			)
		},
		writerProvider: func(folder string, opts *TilerOptions) (writer.Writer, error) {
			writerOpts := []func(*writer.StandardWriter){
				writer.WithNumWorkers(opts.numWorkers),
				writer.WithEncoder(opts.encoderID),
				writer.WithWriterMiddleware(opts.writerMiddleware...),
				writer.WithWriterFinalizer(opts.writerFinalizers...),
				writer.WithGECorrection(opts.geCorrection),
				writer.WithAttributes(opts.attributes),
			}
			if opts.writerProvider != nil {
				writerOpts = append(writerOpts, writer.WithWriterProvider(opts.writerProvider))
			}
			return writer.NewWriter(folder, writerOpts...)
		},
		pointcloudReaderProvider: func(inputFiles []string, sourceCRS string, eightbit bool, attrs model.Attributes) (pointcloud.Reader, error) {
			return pc.NewCombinedPointCloudReader(inputFiles, sourceCRS, eightbit, attrs)
		},
	}, nil
}

// ProcessFolder converts all point cloud files found in the provided input folder converting them into separate tilesets
// each tileset is stored in a subdirectory in the outputFolder named after the filename.
// If sourceCRS is left empty, the CRS will attempted to be autodetected from point cloud GeoTIFF or WKT VLRs.
func (t *GoTiler) ProcessFolder(inputFolder, outputFolder string, sourceCRS string, opts *TilerOptions, ctx context.Context) error {
	files, err := utils.FindPointCloudFilesInFolder(inputFolder)
	if err != nil {
		return err
	}
	for _, f := range files {
		subfolderName := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		err := t.ProcessFiles([]string{f}, filepath.Join(outputFolder, subfolderName), sourceCRS, opts, ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

// ProcessFiles converts the specified point cloud files as a single 3D Tiles tileset and stores them in the given output folder.
// If sourceCRS is left empty, the CRS will attempted to be autodetected from LAS GeoTIFF or WKT VLRs if processing LAS files and the field is available.
func (t *GoTiler) ProcessFiles(inputFiles []string, outputFolder string, sourceCRS string, opts *TilerOptions, ctx context.Context) error {
	return t.processFiles(inputFiles, outputFolder, sourceCRS, opts, ctx)
}

// buildPhaseMap constructs a phase map from the tree's declared phases.
// The tiler always owns phase 1 (preparation) and the last phase (exporting);
// the tree provides its own intermediate phases in execution order.
func buildPhaseMap(tr tree.Tree) map[string]Phase {
	treePhases := tr.Phases()
	total := 1 + len(treePhases) + 1 // preparation + tree phases + exporting
	phases := map[string]Phase{
		"preparation": {Index: 1, Total: total, Name: "Preparation"},
	}
	for i, p := range treePhases {
		phases[p.Name] = Phase{Index: 2 + i, Total: total, Name: p.Label, Unit: p.Unit}
	}
	phases["exporting"] = Phase{Index: total, Total: total, Name: "Exporting", Unit: "tiles"}
	return phases
}

func (t *GoTiler) processFiles(inputFiles []string, outputFolder string, sourceCRS string, opts *TilerOptions, ctx context.Context) (err error) {
	start := time.Now()
	provider := t.treeProvider
	if opts.treeProvider != nil {
		provider = opts.treeProvider
	}
	mutatorPipeline := mutator.NewPipeline(opts.mutators...)
	defer func() {
		closeErr := mutatorPipeline.Close()
		if err == nil {
			err = closeErr
		}
	}()
	inputAttributes := mergeAttributes(opts.attributes, mutatorPipeline.RequiredAttributes())
	tr := provider(treeOptions(opts, inputAttributes), outputFolder)
	defer tr.Dispose()

	inputDesc := fmt.Sprintf("%d files", len(inputFiles))
	if len(inputFiles) == 1 {
		inputDesc = inputFiles[0]
	}

	// Build a single reporter that translates phase-name strings from all internal
	// components into fully-qualified ProgressEvent values for the user callback.
	reporter := newPhaseMappedReporter(opts.progressCallback, inputDesc, start, buildPhaseMap(tr))

	// PHASE 1: PREPARATION — read point cloud header data and detect CRS (near-instant)
	tree.ReportProgress(reporter, tree.ProgressUpdate{
		Phase:       "preparation",
		Percent:     0,
		Message:     "reading file headers",
		IsMilestone: true,
	})
	if err := opts.validateEncoder(); err != nil {
		tree.ReportProgress(reporter, tree.ProgressUpdate{
			Phase:       "preparation",
			Percent:     -1,
			Message:     fmt.Sprintf("encoder error: %v", err),
			IsMilestone: true,
		})
		return err
	}
	pointcloudFile, err := t.pointcloudReaderProvider(inputFiles, sourceCRS, opts.eightBitColors, inputAttributes)
	if err != nil {
		tree.ReportProgress(reporter, tree.ProgressUpdate{
			Phase:       "preparation",
			Percent:     -1,
			Message:     fmt.Sprintf("read error: %v", err),
			IsMilestone: true,
		})
		return err
	}
	defer pointcloudFile.Close()

	pointCount := int64(pointcloudFile.NumberOfPoints())
	tree.ReportProgress(reporter, tree.ProgressUpdate{
		Phase:       "preparation",
		Percent:     100,
		Message:     fmt.Sprintf("found %d points, CRS: %s", pointCount, pointcloudFile.GetCRS()),
		IsMilestone: true,
		ItemTotal:   pointCount,
	})

	// PHASES 2+3: READING + SPLITTING — reservoir sampling and leaf distribution
	err = tr.Load(pointcloudFile, t.convFactory, mutatorPipeline, ctx, reporter)
	if err != nil {
		return err
	}

	// PHASE 4: BUILDING — bubble-up point promotion
	err = tr.Build(ctx, reporter)
	if err != nil {
		return err
	}

	// PHASE 5: EXPORTING — write 3D tiles to disk
	w, err := t.writerProvider(outputFolder, opts)
	if err != nil {
		tree.ReportProgress(reporter, tree.ProgressUpdate{
			Phase:       "exporting",
			Percent:     -1,
			Message:     fmt.Sprintf("export init error: %v", err),
			IsMilestone: true,
		})
		return err
	}
	err = w.Write(tr, "", ctx, reporter)
	if err != nil {
		return err
	}

	return nil
}
