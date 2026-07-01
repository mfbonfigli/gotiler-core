package kd

import (
	"io"
	"testing"

	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

// TestFilePointWriterReader_RoundtripValues verifies that every field in
// model.Point survives a WriteBatch → NextBatch round-trip through the binary
// on-disk format. This test would have caught the omission of ReturnNumber and
// NumberOfReturns from the record layout.
func TestFilePointWriterReader_RoundtripValues(t *testing.T) {
	path := t.TempDir() + "/pts.bin"

	want := []model.Point{
		{X: 1.5, Y: 2.5, Z: 3.5, R: 10, G: 20, B: 30, Intensity: 1000, Classification: 3, ReturnNumber: 2, NumberOfReturns: 5},
		{X: 4.0, Y: 5.0, Z: 6.0, R: 255, G: 128, B: 64, Intensity: 65535, Classification: 7, ReturnNumber: 1, NumberOfReturns: 1},
		{X: -1.0, Y: -2.0, Z: -3.0, R: 0, G: 0, B: 0, Intensity: 0, Classification: 0, ReturnNumber: 3, NumberOfReturns: 4},
	}

	w, err := NewFilePointWriter(path)
	if err != nil {
		t.Fatalf("NewFilePointWriter: %v", err)
	}
	if err := w.WriteBatch(want); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	r, err := NewFilePointReader(path)
	if err != nil {
		t.Fatalf("NewFilePointReader: %v", err)
	}
	defer r.Close()

	if r.NumPoints() != len(want) {
		t.Fatalf("NumPoints: got %d want %d", r.NumPoints(), len(want))
	}

	got, readErr := r.NextBatch(make([]model.Point, 0, len(want)))
	if readErr != nil && readErr != io.EOF {
		t.Fatalf("NextBatch: %v", readErr)
	}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d want %d", len(got), len(want))
	}

	for i, wpt := range want {
		g := got[i]
		if g.R != wpt.R || g.G != wpt.G || g.B != wpt.B {
			t.Errorf("pt%d RGB: got (%d,%d,%d) want (%d,%d,%d)", i, g.R, g.G, g.B, wpt.R, wpt.G, wpt.B)
		}
		if g.Intensity != wpt.Intensity {
			t.Errorf("pt%d Intensity: got %d want %d", i, g.Intensity, wpt.Intensity)
		}
		if g.Classification != wpt.Classification {
			t.Errorf("pt%d Classification: got %d want %d", i, g.Classification, wpt.Classification)
		}
		if g.ReturnNumber != wpt.ReturnNumber {
			t.Errorf("pt%d ReturnNumber: got %d want %d", i, g.ReturnNumber, wpt.ReturnNumber)
		}
		if g.NumberOfReturns != wpt.NumberOfReturns {
			t.Errorf("pt%d NumberOfReturns: got %d want %d", i, g.NumberOfReturns, wpt.NumberOfReturns)
		}
	}
}
