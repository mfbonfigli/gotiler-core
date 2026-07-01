package tree

import (
	"context"

	"github.com/mfbonfigli/gotiler-core/tiler/coord"
	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/mutator"
	"github.com/mfbonfigli/gotiler-core/tiler/pointcloud"
)

// Options contains the tree-specific settings for one tiling run.
type Options struct {
	NumWorkers            int
	PointsPerTile         int
	RefineMode            model.RefineMode
	InitialGeometricError float64
}

// Provider creates a Tree for one tiling run.
type Provider func(opts Options, output string) Tree

// Tree represents a point-cloud spatial hierarchy usable by the tiler.
type Tree interface {
	Phases() []PhaseInfo
	Load(pointcloud.Reader, coord.ConverterFactory, mutator.Mutator, context.Context, ProgressReporter) error
	Build(ctx context.Context, reporter ProgressReporter) error
	RootNode() Node
	Dispose() error
}

// Node models one tile node in a Tree.
type Node interface {
	BoundingBox() geom.BoundingBox
	ChildrenAt(i uint8) Node
	Points() (geom.PointList, error)
	TotalNumberOfPoints() int
	NumberOfPoints() int
	IsRoot() bool
	IsLeaf() bool
	GeometricError() float64
	ToParentCRS() *model.Transform
	RefineMode() model.RefineMode
}
