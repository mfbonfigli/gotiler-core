package mutator

import (
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

// Mutator defines a generic interface to manipulate coordinates or attributes of points.
type Mutator interface {
	// RequiredAttributes returns optional per-point attributes this mutator
	// needs the reader to provide as input. Names are canonicalized by the
	// tiler before they are passed to readers.
	RequiredAttributes() model.Attributes

	// Mutate transforms or discards the points in input.
	//
	// The function receives in input the point, with coordinates expressed in
	// the local CRS with Z-up, a typed view over the point's optional
	// attributes (empty when none were requested), and a transform object that
	// can be used to forward transform from the local CRS to the global EPSG
	// 4978 CRS and inverse transform from the global CRS to the local CRS.
	//
	// Attribute changes made through the view's setters are applied in place
	// and flow into the output tiles. The function returns the manipulated
	// point and true if the point is to be used or false if the point should
	// be discarded from the final point cloud.
	Mutate(pt model.Point, attrs model.AttributeView, localToGlobal model.Transform) (model.Point, bool)
}
