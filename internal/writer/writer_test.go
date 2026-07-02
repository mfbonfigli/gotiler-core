package writer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/mfbonfigli/gotiler-core/internal/testtree"
	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/plugin"
	"github.com/mfbonfigli/gotiler-core/tiler/tree"
	"github.com/mfbonfigli/gotiler-core/version"
)

type captureWriteCloser struct {
	*bytes.Buffer
	closeFn func([]byte)
}

type fakeGeometryEncoder struct{}

func (fakeGeometryEncoder) Write(n tree.Node, wp plugin.WriterProvider, prefix string) error {
	return nil
}

func (fakeGeometryEncoder) TilesetVersion() version.TilesetVersion {
	return version.TilesetVersion_1_1
}

func (fakeGeometryEncoder) ContentFilename() string {
	return "fake.glb"
}

func (w *captureWriteCloser) Close() error {
	w.closeFn(w.Bytes())
	return nil
}

func TestWriter(t *testing.T) {
	pt1 := &geom.LinkedPoint{
		Pt: geom.NewPoint(1, 2, 3, 4, 5, 6),
	}
	pt2 := &geom.LinkedPoint{
		Pt: geom.NewPoint(9, 10, 11, 12, 13, 14),
	}
	pt3 := &geom.LinkedPoint{
		Pt: geom.NewPoint(17, 18, 19, 20, 21, 22),
	}
	pt1.Next = pt2
	pt2.Next = pt3

	stream := geom.NewLinkedPointStream(pt1, 3)
	stream2 := geom.NewLinkedPointStream(pt2, 2)

	child := &testtree.MockNode{
		TotalNumPts: 2,
		Pts:         stream2,
	}
	root := &testtree.MockNode{
		TotalNumPts: 5,
		Pts:         stream,
		ChildNodes: [8]tree.Node{
			nil,
			child,
		},
	}

	w, err := NewWriter("base",
		WithNumWorkers(1),
		WithBufferRatio(10),
	)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	p := &MockProducer{}
	c := &MockConsumer{}
	w.producerFunc = func(wp plugin.WriterProvider) Producer {
		return p
	}
	w.consumerFunc = func() Consumer {
		return c
	}
	ctx := context.TODO()
	ctx = context.WithValue(ctx, "IS_TEST", true)
	err = w.Write(root, "base", ctx, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if p.Wc == nil {
		t.Errorf("empty work channel passed")
	} else {
		if c.Wc != p.Wc {
			t.Errorf("passed different work channel to consumer")
		}
	}
	if p.Ec == nil {
		t.Errorf("empty error channel passed")
	} else {
		if c.Ec != p.Ec {
			t.Errorf("passed different error channel to consumer")
		}
	}
}

func TestWriterWithCustomWriterProviderWritesTilesetAndContent(t *testing.T) {
	pt := &geom.LinkedPoint{
		Pt: geom.NewPoint(1, 2, 3, 4, 5, 6),
	}
	root := &testtree.MockNode{
		TotalNumPts: 1,
		Pts:         geom.NewLinkedPointStream(pt, 1),
		Bounds:      geom.NewBoundingBox(1, 2, 3, 4, 5, 6),
		GeomError:   0,
		Root:        true,
	}

	var mu sync.Mutex
	writes := map[string][]byte{}
	wp := func(filename string) (io.WriteCloser, error) {
		return &captureWriteCloser{
			Buffer: &bytes.Buffer{},
			closeFn: func(data []byte) {
				mu.Lock()
				defer mu.Unlock()
				writes[filename] = append([]byte(nil), data...)
			},
		}, nil
	}

	w, err := NewWriter("ignored",
		WithNumWorkers(1),
		WithEncoder(plugin.EncoderPNTS),
		WithWriterProvider(wp),
	)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if err := w.Write(root, "", context.TODO(), nil); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if len(writes["data/d.pnts"]) == 0 {
		t.Fatalf("expected content tile to be written through custom provider")
	}
	if len(writes["tileset.json"]) == 0 {
		t.Fatalf("expected tileset.json to be written through custom provider")
	}
}

func TestWriterSquashTilesetContent(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create test data structure
	pt1 := &geom.LinkedPoint{
		Pt: geom.NewPoint(1, 2, 3, 4, 5, 6),
	}
	pt2 := &geom.LinkedPoint{
		Pt: geom.NewPoint(9, 10, 11, 12, 13, 14),
	}
	pt1.Next = pt2

	stream := geom.NewLinkedPointStream(pt1, 2)
	stream2 := geom.NewLinkedPointStream(pt2, 1)

	// Create a mock tree with child nodes
	child := &testtree.MockNode{
		TotalNumPts: 1,
		Pts:         stream2,
		Bounds:      geom.NewBoundingBox(5, 6, 7, 8, 9, 10),
		GeomError:   10.0,
	}
	root := &testtree.MockNode{
		TotalNumPts: 2,
		Pts:         stream,
		Bounds:      geom.NewBoundingBox(1, 2, 3, 4, 5, 6),
		GeomError:   20.0,
		Root:        true,
		ChildNodes: [8]tree.Node{
			nil,
			child,
		},
	}

	// Test squash mode - generates single tileset.json
	w, err := NewWriter(tmpDir,
		WithNumWorkers(1),
		WithBufferRatio(10),
		WithEncoder(plugin.EncoderPNTS),
	)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}

	// Test that tileset is generated correctly
	tileset := w.buildTileTree(root, "")

	// Verify root tile properties
	if tileset.GeometricError != 20.0 {
		t.Errorf("expected geometric error 20.0, got %v", tileset.GeometricError)
	}

	if tileset.Refine != "ADD" {
		t.Errorf("expected refine to be ADD, got %v", tileset.Refine)
	}

	if tileset.Content == nil {
		t.Errorf("expected root tile to have content")
	} else if tileset.Content.Url != "data/d.pnts" {
		t.Errorf("expected content URL to be 'data/d.pnts', got %v", tileset.Content.Url)
	}

	// Verify bounding box
	expectedBox := []float64{1, 2, 3, 4, 5, 6, 0, 0, 0, 0, 0, 0}
	if len(tileset.BoundingVolume.Box) != len(expectedBox) {
		t.Errorf("expected bounding box length %d, got %d", len(expectedBox), len(tileset.BoundingVolume.Box))
	}

	// Verify children exist
	if len(tileset.Children) != 1 {
		t.Errorf("expected 1 child, got %d", len(tileset.Children))
	}

	// Verify child properties
	if len(tileset.Children) > 0 {
		childTile := tileset.Children[0]
		if childTile.GeometricError != 10.0 {
			t.Errorf("expected child geometric error 10.0, got %v", childTile.GeometricError)
		}

		if childTile.Content == nil {
			t.Errorf("expected child tile to have content")
		} else if childTile.Content.Url != "data/1d.pnts" {
			t.Errorf("expected child content URL to be 'data/1d.pnts', got %v", childTile.Content.Url)
		}
	}
}

func TestWriterGECorrection(t *testing.T) {
	tmpDir := t.TempDir()

	pt1 := &geom.LinkedPoint{Pt: geom.NewPoint(1, 2, 3, 4, 5, 6)}
	pt2 := &geom.LinkedPoint{Pt: geom.NewPoint(9, 10, 11, 12, 13, 14)}
	pt1.Next = pt2
	stream := geom.NewLinkedPointStream(pt1, 2)
	stream2 := geom.NewLinkedPointStream(pt2, 1)

	child := &testtree.MockNode{TotalNumPts: 1, Pts: stream2, Bounds: geom.NewBoundingBox(5, 6, 7, 8, 9, 10), GeomError: 10.0}
	root := &testtree.MockNode{TotalNumPts: 2, Pts: stream, Bounds: geom.NewBoundingBox(1, 2, 3, 4, 5, 6), GeomError: 20.0, Root: true, ChildNodes: [8]tree.Node{nil, child}}

	w, err := NewWriter(tmpDir,
		WithEncoder(plugin.EncoderPNTS),
		WithGECorrection(2.0),
	)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}

	tileset := w.buildTileTree(root, "")
	if tileset.GeometricError != 40.0 {
		t.Errorf("expected root GeometricError 40.0, got %v", tileset.GeometricError)
	}
	if len(tileset.Children) > 0 && tileset.Children[0].GeometricError != 20.0 {
		t.Errorf("expected child GeometricError 20.0, got %v", tileset.Children[0].GeometricError)
	}
}

func TestWriterWithProducerError(t *testing.T) {
	pt1 := &geom.LinkedPoint{
		Pt: geom.NewPoint(1, 2, 3, 4, 5, 6),
	}
	pt2 := &geom.LinkedPoint{
		Pt: geom.NewPoint(9, 10, 11, 12, 13, 14),
	}
	pt3 := &geom.LinkedPoint{
		Pt: geom.NewPoint(17, 18, 19, 20, 21, 22),
	}
	pt1.Next = pt2
	pt2.Next = pt3

	stream := geom.NewLinkedPointStream(pt1, 3)
	stream2 := geom.NewLinkedPointStream(pt2, 2)

	child := &testtree.MockNode{
		TotalNumPts: 2,
		Pts:         stream2,
	}
	root := &testtree.MockNode{
		TotalNumPts: 5,
		Pts:         stream,
		ChildNodes: [8]tree.Node{
			nil,
			child,
		},
	}

	w, err := NewWriter("base",
		WithNumWorkers(1),
		WithBufferRatio(10),
	)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	p := &MockProducer{
		Err: fmt.Errorf("mock error"),
	}
	c := &MockConsumer{}
	w.producerFunc = func(wp plugin.WriterProvider) Producer {
		return p
	}
	w.consumerFunc = func() Consumer {
		return c
	}
	err = w.Write(root, "base", context.TODO(), nil)
	if err == nil {
		t.Errorf("expected error but got none")
	}
	if p.Wc == nil {
		t.Errorf("empty work channel passed")
	} else {
		if c.Wc != p.Wc {
			t.Errorf("passed different work channel to consumer")
		}
	}
	if p.Ec == nil {
		t.Errorf("empty error channel passed")
	} else {
		if c.Ec != p.Ec {
			t.Errorf("passed different error channel to consumer")
		}
	}
}

func TestWriterWithConsumerError(t *testing.T) {
	pt1 := &geom.LinkedPoint{
		Pt: geom.NewPoint(1, 2, 3, 4, 5, 6),
	}
	pt2 := &geom.LinkedPoint{
		Pt: geom.NewPoint(9, 10, 11, 12, 13, 14),
	}
	pt3 := &geom.LinkedPoint{
		Pt: geom.NewPoint(17, 18, 19, 20, 21, 22),
	}
	pt1.Next = pt2
	pt2.Next = pt3

	stream := geom.NewLinkedPointStream(pt1, 3)
	stream2 := geom.NewLinkedPointStream(pt2, 2)

	child := &testtree.MockNode{
		TotalNumPts: 2,
		Pts:         stream2,
	}
	root := &testtree.MockNode{
		TotalNumPts: 5,
		Pts:         stream,
		ChildNodes: [8]tree.Node{
			nil,
			child,
		},
	}

	w, err := NewWriter("base",
		WithNumWorkers(1),
		WithBufferRatio(10),
	)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	p := &MockProducer{}
	c := &MockConsumer{
		Err: fmt.Errorf("mock error"),
	}
	w.producerFunc = func(wp plugin.WriterProvider) Producer {
		return p
	}
	w.consumerFunc = func() Consumer {
		return c
	}
	err = w.Write(root, "base", context.TODO(), nil)
	if err == nil {
		t.Errorf("expected error but got none")
	}
	if p.Wc == nil {
		t.Errorf("empty work channel passed")
	} else {
		if c.Wc != p.Wc {
			t.Errorf("passed different work channel to consumer")
		}
	}
	if p.Ec == nil {
		t.Errorf("empty error channel passed")
	} else {
		if c.Ec != p.Ec {
			t.Errorf("passed different error channel to consumer")
		}
	}
}

func TestWriterEncoderSelection(t *testing.T) {
	w, err := NewWriter("base",
		WithNumWorkers(1),
		WithBufferRatio(10),
		WithEncoder(plugin.EncoderPNTS),
	)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if w.tilesetVersion != version.TilesetVersion_1_0 {
		t.Errorf("unexpected tileset version")
	}
	if w.contentFilename != pntsFilename {
		t.Errorf("unexpected content filename %q", w.contentFilename)
	}
	c := w.consumerFunc()
	if _, success := (c.(*StandardConsumer).encoder).(*PntsEncoder); success != true {
		t.Errorf("unexpected geometry encoder for tileset version 1.0")
	}
	w, err = NewWriter("base",
		WithNumWorkers(1),
		WithBufferRatio(10),
		WithEncoder(plugin.EncoderGLB),
	)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if w.tilesetVersion != version.TilesetVersion_1_1 {
		t.Errorf("unexpected tileset version")
	}
	if w.contentFilename != glbFilename {
		t.Errorf("unexpected content filename %q", w.contentFilename)
	}
	c = w.consumerFunc()
	if _, success := (c.(*StandardConsumer).encoder).(*GltfEncoder); success != true {
		t.Errorf("unexpected geometry encoder for tileset version 1.1")
	}
}

func TestWriterUsesRegisteredGeometryEncoder(t *testing.T) {
	const fakeEncoderID = "test-fake-glb"
	plugin.RegisterGeometryEncoder(fakeEncoderID, func(attrs model.Attributes) plugin.GeometryEncoder {
		return fakeGeometryEncoder{}
	})

	w, err := NewWriter("base",
		WithEncoder(fakeEncoderID),
	)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if w.tilesetVersion != version.TilesetVersion_1_1 {
		t.Errorf("unexpected tileset version")
	}
	if w.contentFilename != "fake.glb" {
		t.Errorf("unexpected content filename %q", w.contentFilename)
	}

	c := w.consumerFunc()
	if _, success := (c.(*StandardConsumer).encoder).(fakeGeometryEncoder); !success {
		t.Errorf("expected injected geometry encoder for tileset version 1.1")
	}
}
