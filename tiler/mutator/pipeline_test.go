package mutator

import (
	"reflect"
	"testing"

	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

type discardMutator struct{}

func (p *discardMutator) RequiredAttributes() model.Attributes {
	return model.NewAttributes("classification", " intensity ")
}

func (p *discardMutator) Mutate(pt model.Point, attrs model.AttributeView, t model.Transform) (model.Point, bool) {
	return pt, false
}

func TestPipeline(t *testing.T) {
	p := NewPipeline(
		NewZOffset(1.5),
		NewZOffset(2.5),
	)
	actual, keep := p.Mutate(geom.NewPoint(1, 2, 3, 1, 2, 3), model.AttributeView{}, model.Transform{})
	expected := geom.NewPoint(1, 2, 7, 1, 2, 3)
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}
	if !keep {
		t.Errorf("expected keep to be true but is false")
	}
}

func TestPipelineRequiredAttributes(t *testing.T) {
	p := NewPipeline(
		NewZOffset(1.5),
		&discardMutator{},
	)
	got := p.RequiredAttributes()
	if !got.Has("classification") || !got.Has("intensity") {
		t.Fatalf("expected required attributes to include classification and intensity, got %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("expected de-duplicated required attributes, got %v", got)
	}
}

func TestPipelineDiscard(t *testing.T) {
	p := NewPipeline(
		NewZOffset(1.5),
		&discardMutator{},
		NewZOffset(2.5),
	)
	actual, keep := p.Mutate(geom.NewPoint(1, 2, 3, 1, 2, 3), model.AttributeView{}, model.Transform{})
	expected := geom.NewPoint(1, 2, 4.5, 1, 2, 3)
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}
	if keep {
		t.Errorf("expected point to be discarded but was not")
	}
}
