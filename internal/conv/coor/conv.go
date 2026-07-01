package coor

import (
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

type Converter interface {
	Transform(sourceCRS string, targetCRS string, coord model.Vector) (model.Vector, error)
	ToWGS84Cartesian(sourceCRS string, coord model.Vector) (model.Vector, error)
	// ToWGS84CartesianFlat transforms a flat slice of X,Y,Z coordinates in-place.
	// flatCoords layout: [X₀, Y₀, Z₀, X₁, Y₁, Z₁, ...], len must be multiple of 3.
	ToWGS84CartesianFlat(sourceCRS string, flatCoords []float64) error
	Cleanup()
}

// ConverterFactory returns a new CoordinateConverter that should only be used in the same goroutine
// to avoid race conditions
type ConverterFactory func() (Converter, error)
