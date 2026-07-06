package mutator

import (
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

// Mutator defines a generic interface to manipulate coordinates or attributes
// of point batches.
type Mutator interface {
	// RequiredAttributes returns optional per-point attributes this mutator
	// needs the reader to provide as input. Names are canonicalized by the
	// tiler before they are passed to readers.
	RequiredAttributes() model.Attributes

	// MutateChunk transforms or discards the points in input.
	//
	// Points are expressed in the local CRS with Z-up. AttributeLayout describes
	// the packed attributes attached to each point. localToGlobal can be used to
	// forward transform from the local CRS to global EPSG:4978 and inverse
	// transform from global CRS back to the local CRS.
	//
	// Implementations may mutate chunk.Points in place and should return the
	// kept points, usually as a subslice of chunk.Points.
	//
	// MutateChunk may be invoked concurrently from multiple goroutines on the
	// same instance: implementations must be safe for concurrent use, and any
	// internal state requires synchronization. Chunks never overlap, so mutating
	// chunk.Points in place is safe without locking.
	MutateChunk(chunk PointChunk, localToGlobal model.Transform) []model.Point

	// Close releases resources held by the mutator. It is called once by the
	// tiler after all mutation work completes.
	Close() error
}

// PointChunk is a mutable batch of local-coordinate points passed to
// chunk-aware mutators. AttributeLayout describes the packed Attributes blob
// attached to each point.
type PointChunk struct {
	Points          []model.Point
	AttributeLayout []model.AttributeLayoutEntry
}

// AttributeView returns a typed view over the i-th point's packed attributes.
func (c PointChunk) AttributeView(i int) model.AttributeView {
	return model.NewAttributeView(c.AttributeLayout, c.Points[i].Attributes)
}

// MutateChunk applies m to the whole chunk.
func MutateChunk(m Mutator, chunk PointChunk, localToGlobal model.Transform) []model.Point {
	if m == nil || len(chunk.Points) == 0 {
		return chunk.Points
	}
	return m.MutateChunk(chunk, localToGlobal)
}
