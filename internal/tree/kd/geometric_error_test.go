package kd

import (
	"math"
	"testing"

	"github.com/mfbonfigli/gotiler-core/tiler/geom"
)

func TestRootTargetGeometricErrorDefaultsToOneFifthCubicBoundingBoxDiagonal(t *testing.T) {
	n := NewTree()
	n.bounds = geom.NewBoundingBox(-1, 2, 10, 14, 100, 112)

	expected := math.Sqrt(3) * 12 / 5
	if got := n.rootTargetGeometricError(); math.Abs(got-expected) > 1e-9 {
		t.Fatalf("expected root target GE %v, got %v", expected, got)
	}
}

func TestRootTargetGeometricErrorOverride(t *testing.T) {
	n := NewTree(WithRootTargetGeomErr(128))
	n.bounds = geom.NewBoundingBox(-1, 2, 10, 14, 100, 112)

	if got := n.rootTargetGeometricError(); got != 128 {
		t.Fatalf("expected overridden root target GE 128, got %v", got)
	}
}

func TestDefaultRootTargetGeometricErrorHandlesDegenerateBounds(t *testing.T) {
	bounds := geom.NewBoundingBox(5, 5, -2, -2, 7, 7)

	if got := defaultRootTargetGeometricError(bounds); got != 0 {
		t.Fatalf("expected zero target GE for degenerate bounds, got %v", got)
	}
}
