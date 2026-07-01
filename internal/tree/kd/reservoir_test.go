package kd

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/mfbonfigli/gotiler-core/internal/pc"
	"github.com/mfbonfigli/gotiler-core/internal/utils"
	"github.com/mfbonfigli/gotiler-core/internal/utils/test"
	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/mutator"
)

func TestReservoirLoader_InvalidSize(t *testing.T) {
	conv := test.GetTestCoordinateConverterFactory()
	loader := NewReservoirLoader(conv, nil, 0, 1, t.TempDir(), NewFileIoFactory())

	reader := &pc.MockLasReader{
		CRS: "EPSG:4978",
		Pts: []geom.Point64{{Vector: model.Vector{X: 4399228.288985, Y: 855784.797006, Z: 0}}},
	}

	_, err := loader.Run(reader, context.Background(), nil)
	if err == nil {
		t.Errorf("expected error for invalid reservoir size, got nil")
	}
}

func TestReservoirLoader_EmptyInput(t *testing.T) {
	conv := test.GetTestCoordinateConverterFactory()
	loader := NewReservoirLoader(conv, nil, 10, 1, t.TempDir(), NewFileIoFactory())

	reader := &pc.MockLasReader{
		CRS: "EPSG:4978",
		Pts: []geom.Point64{},
	}

	_, err := loader.Run(reader, context.Background(), nil)
	if err == nil {
		t.Errorf("expected error for empty input, got nil")
	}
}

func TestReservoirLoader_BasicPipeline(t *testing.T) {
	conv := test.GetTestCoordinateConverterFactory()
	loader := NewReservoirLoader(conv, nil, 100, 1, t.TempDir(), NewFileIoFactory())

	reader := &pc.MockLasReader{
		CRS: "EPSG:4978",
		Pts: []geom.Point64{
			{Vector: model.Vector{X: 4399228.288985, Y: 855784.797006, Z: 0}, R: 255, G: 0, B: 0, Intensity: 1, Classification: 1, ReturnNumber: 1, NumberOfReturns: 3},
			{Vector: model.Vector{X: 4399238.288985, Y: 855784.797006, Z: 0}, R: 0, G: 255, B: 0, Intensity: 2, Classification: 2, ReturnNumber: 2, NumberOfReturns: 3},
			{Vector: model.Vector{X: 4399228.288985, Y: 855794.797006, Z: 0}, R: 0, G: 0, B: 255, Intensity: 3, Classification: 3, ReturnNumber: 3, NumberOfReturns: 3},
			{Vector: model.Vector{X: 4399228.288985, Y: 855784.797006, Z: 10}, R: 128, G: 128, B: 128, Intensity: 4, Classification: 4, ReturnNumber: 1, NumberOfReturns: 2},
			{Vector: model.Vector{X: 4399233.288985, Y: 855789.797006, Z: 5}, R: 64, G: 64, B: 64, Intensity: 5, Classification: 5, ReturnNumber: 2, NumberOfReturns: 2},
		},
	}

	result, err := loader.Run(reader, context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Ensure file is deleted when test finishes
	t.Cleanup(func() { _ = os.Remove(result.tempFilePath) })

	if result.totalPoints != 5 {
		t.Errorf("expected 5 total points, got %d", result.totalPoints)
	}

	if len(result.sample) != 5 {
		t.Errorf("expected 5 sample points, got %d", len(result.sample))
	}

	if result.tempFilePath == "" {
		t.Errorf("expected non-empty temp file path")
	}

	if diff, err := utils.CompareWithTolerance(result.bounds.Xmin, -10.0, 1e-4); err != nil {
		t.Errorf("Xmin: diff %f above tolerance, got %f expected -10", diff, result.bounds.Xmin)
	}
	if diff, err := utils.CompareWithTolerance(result.bounds.Xmax, 0.0, 1e-4); err != nil {
		t.Errorf("Xmax: diff %f above tolerance, got %f expected 0", diff, result.bounds.Xmax)
	}
	if diff, err := utils.CompareWithTolerance(result.bounds.Ymin, -1.909512, 1e-4); err != nil {
		t.Errorf("Ymin: diff %f above tolerance, got %f expected ~ -1.91", diff, result.bounds.Ymin)
	}
	if diff, err := utils.CompareWithTolerance(result.bounds.Ymax, 9.815995, 1e-4); err != nil {
		t.Errorf("Ymax: diff %f above tolerance, got %f expected ~ 9.82", diff, result.bounds.Ymax)
	}
	if diff, err := utils.CompareWithTolerance(result.bounds.Zmin, 0.0, 1e-4); err != nil {
		t.Errorf("Zmin: diff %f above tolerance, got %f expected 0", diff, result.bounds.Zmin)
	}
	if diff, err := utils.CompareWithTolerance(result.bounds.Zmax, 9.815995, 1e-4); err != nil {
		t.Errorf("Zmax: diff %f above tolerance, got %f expected ~ 9.82", diff, result.bounds.Zmax)
	}

	reader2, err := NewFilePointReader(result.tempFilePath)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}
	defer reader2.Close()

	if reader2.NumPoints() != 5 {
		t.Errorf("expected 5 points in temp file, got %d", reader2.NumPoints())
	}

	buf := make([]model.Point, 0, 5)
	buf, readErr := reader2.NextBatch(buf)
	if readErr != nil && readErr != io.EOF {
		t.Fatalf("failed to read points from temp file: %v", readErr)
	}
	if len(buf) != 5 {
		t.Fatalf("expected 5 points, got %d", len(buf))
	}

	if diff, err := utils.CompareWithTolerance(float64(buf[0].X), 0.0, 1e-4); err != nil {
		t.Errorf("point 0 X: diff %f above tolerance, got %f expected 0", diff, buf[0].X)
	}
	if diff, err := utils.CompareWithTolerance(float64(buf[0].Y), 0.0, 1e-4); err != nil {
		t.Errorf("point 0 Y: diff %f above tolerance, got %f expected 0", diff, buf[0].Y)
	}
	if diff, err := utils.CompareWithTolerance(float64(buf[0].Z), 0.0, 1e-4); err != nil {
		t.Errorf("point 0 Z: diff %f above tolerance, got %f expected 0", diff, buf[0].Z)
	}

	expectedPts := reader.Pts
	for i := range 5 {
		if buf[i].R != expectedPts[i].R {
			t.Errorf("point %d R: expected %d got %d", i, expectedPts[i].R, buf[i].R)
		}
		if buf[i].G != expectedPts[i].G {
			t.Errorf("point %d G: expected %d got %d", i, expectedPts[i].G, buf[i].G)
		}
		if buf[i].B != expectedPts[i].B {
			t.Errorf("point %d B: expected %d got %d", i, expectedPts[i].B, buf[i].B)
		}
		if buf[i].Intensity != expectedPts[i].Intensity {
			t.Errorf("point %d Intensity: expected %d got %d", i, expectedPts[i].Intensity, buf[i].Intensity)
		}
		if buf[i].Classification != expectedPts[i].Classification {
			t.Errorf("point %d Classification: expected %d got %d", i, expectedPts[i].Classification, buf[i].Classification)
		}
		if buf[i].ReturnNumber != expectedPts[i].ReturnNumber {
			t.Errorf("point %d ReturnNumber: expected %d got %d", i, expectedPts[i].ReturnNumber, buf[i].ReturnNumber)
		}
		if buf[i].NumberOfReturns != expectedPts[i].NumberOfReturns {
			t.Errorf("point %d NumberOfReturns: expected %d got %d", i, expectedPts[i].NumberOfReturns, buf[i].NumberOfReturns)
		}

		ecefPt := result.localToGlobal.Forward(model.Vector{
			X: float64(buf[i].X),
			Y: float64(buf[i].Y),
			Z: float64(buf[i].Z),
		})
		if diffX, errX := utils.CompareWithTolerance(ecefPt.X, expectedPts[i].X, 1e-3); errX != nil || diffX > 1e-2 {
			t.Errorf("point %d X round-trip: diff %f above tolerance, got %f expected %f", i, diffX, ecefPt.X, expectedPts[i].X)
		}
		if diffY, errY := utils.CompareWithTolerance(ecefPt.Y, expectedPts[i].Y, 1e-3); errY != nil || diffY > 1e-2 {
			t.Errorf("point %d Y round-trip: diff %f above tolerance, got %f expected %f", i, diffY, ecefPt.Y, expectedPts[i].Y)
		}
		if diffZ, errZ := utils.CompareWithTolerance(ecefPt.Z, expectedPts[i].Z, 1e-3); errZ != nil || diffZ > 1e-2 {
			t.Errorf("point %d Z round-trip: diff %f above tolerance, got %f expected %f", i, diffZ, ecefPt.Z, expectedPts[i].Z)
		}
	}
}

func TestReservoirLoader_FewerPointsThanReservoir(t *testing.T) {
	conv := test.GetTestCoordinateConverterFactory()
	loader := NewReservoirLoader(conv, nil, 1000, 1, t.TempDir(), NewFileIoFactory())

	reader := &pc.MockLasReader{
		CRS: "EPSG:4978",
		Pts: []geom.Point64{
			{Vector: model.Vector{X: 4399228.288985, Y: 855784.797006, Z: 0}},
			{Vector: model.Vector{X: 4399238.288985, Y: 855794.797006, Z: 10}},
			{Vector: model.Vector{X: 4399248.288985, Y: 855804.797006, Z: 20}},
		},
	}

	result, err := loader.Run(reader, context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(result.tempFilePath) })

	if result.totalPoints != 3 {
		t.Errorf("expected 3 total points, got %d", result.totalPoints)
	}
	if len(result.sample) != 3 {
		t.Errorf("expected 3 sample points, got %d", len(result.sample))
	}
}

func TestReservoirLoader_WithZOffset(t *testing.T) {
	conv := test.GetTestCoordinateConverterFactory()
	zOffset := mutator.NewZOffset(5.0)
	loader := NewReservoirLoader(conv, zOffset, 10, 1, t.TempDir(), NewFileIoFactory())

	reader := &pc.MockLasReader{
		CRS: "EPSG:4978",
		Pts: []geom.Point64{
			{Vector: model.Vector{X: 4399228.288985, Y: 855784.797006, Z: 0}, R: 100, G: 100, B: 100, Intensity: 1, Classification: 1},
			{Vector: model.Vector{X: 4399238.288985, Y: 855784.797006, Z: 0}, R: 200, G: 200, B: 200, Intensity: 2, Classification: 2},
		},
	}

	result, err := loader.Run(reader, context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(result.tempFilePath) })

	if result.totalPoints != 2 {
		t.Errorf("expected 2 total points, got %d", result.totalPoints)
	}

	reader2, err := NewFilePointReader(result.tempFilePath)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}
	defer reader2.Close()

	if reader2.NumPoints() != 2 {
		t.Errorf("expected 2 points in temp file, got %d", reader2.NumPoints())
	}

	buf := make([]model.Point, 0, 2)
	buf, readErr := reader2.NextBatch(buf)
	if readErr != nil && readErr != io.EOF {
		t.Fatalf("failed to read points: %v", readErr)
	}

	if diff, err := utils.CompareWithTolerance(float64(buf[0].Z), 5.0, 1e-3); err != nil {
		t.Errorf("first point Z: diff %f above tolerance, expected ~5 got %f", diff, buf[0].Z)
	}
}

func TestReservoirLoader_MultipleWorkers(t *testing.T) {
	conv := test.GetTestCoordinateConverterFactory()

	// FIX: Scale dataset to ensure multiple batches fill worker thread slots
	numPoints := (pipelineBatchSize * 4) + 50
	loader := NewReservoirLoader(conv, nil, 100, 4, t.TempDir(), NewFileIoFactory())

	pts := make([]geom.Point64, numPoints)
	for i := range pts {
		pts[i] = geom.Point64{
			Vector: model.Vector{
				X: 4399228.288985 + float64(i),
				Y: 855784.797006 + float64(i),
				Z: float64(i),
			},
		}
	}
	reader := &pc.MockLasReader{
		CRS: "EPSG:4978",
		Pts: pts,
	}

	result, err := loader.Run(reader, context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(result.tempFilePath) })

	if result.totalPoints != numPoints {
		t.Errorf("expected %d total points, got %d", numPoints, result.totalPoints)
	}

	reader2, err := NewFilePointReader(result.tempFilePath)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}
	defer reader2.Close()

	if reader2.NumPoints() != numPoints {
		t.Errorf("expected %d points in temp file, got %d", numPoints, reader2.NumPoints())
	}
}
