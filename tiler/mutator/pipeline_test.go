package mutator

import (
	"reflect"
	"testing"

	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

type discardMutator struct{}

func (p *discardMutator) Mutate(pt model.Point, attrs []model.Attribute, t model.Transform) (model.Point, []model.Attribute, bool) {
	return pt, attrs, false
}

func TestPipeline(t *testing.T) {
	p := NewPipeline(
		NewZOffset(1.5),
		NewZOffset(2.5),
	)
	actual, _, keep := p.Mutate(geom.NewPoint(1, 2, 3, 1, 2, 3), nil, model.Transform{})
	expected := geom.NewPoint(1, 2, 7, 1, 2, 3)
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}
	if !keep {
		t.Errorf("expected keep to be true but is false")
	}
}

func TestPipelineDiscard(t *testing.T) {
	p := NewPipeline(
		NewZOffset(1.5),
		&discardMutator{},
		NewZOffset(2.5),
	)
	actual, _, keep := p.Mutate(geom.NewPoint(1, 2, 3, 1, 2, 3), nil, model.Transform{})
	expected := geom.NewPoint(1, 2, 4.5, 1, 2, 3)
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}
	if keep {
		t.Errorf("expected point to be discarded but was not")
	}
}
