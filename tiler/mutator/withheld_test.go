package mutator

import (
	"testing"

	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

func TestWithheldFilterRequiredAttributes(t *testing.T) {
	got := NewWithheldFilter().RequiredAttributes()
	if len(got) != 1 || !got.Has(model.AttrWithheld) {
		t.Fatalf("expected withheld required attribute, got %v", got)
	}
}

func TestWithheldFilterKeepsPointWhenAttributeMissing(t *testing.T) {
	_, keep := NewWithheldFilter().Mutate(geom.NewPoint(1, 2, 3, 1, 2, 3), model.AttributeView{}, model.IdentityTransform)
	if !keep {
		t.Fatal("expected point without withheld attribute to be kept")
	}
}

func TestWithheldFilterKeepsPointWhenWithheldFalse(t *testing.T) {
	_, keep := NewWithheldFilter().Mutate(geom.NewPoint(1, 2, 3, 1, 2, 3), withheldView(false), model.IdentityTransform)
	if !keep {
		t.Fatal("expected point with withheld=false to be kept")
	}
}

func TestWithheldFilterDropsPointWhenWithheldTrue(t *testing.T) {
	_, keep := NewWithheldFilter().Mutate(geom.NewPoint(1, 2, 3, 1, 2, 3), withheldView(true), model.IdentityTransform)
	if keep {
		t.Fatal("expected point with withheld=true to be dropped")
	}
}

func withheldView(withheld bool) model.AttributeView {
	summaries := []model.AttributeSummary{{
		Name: model.AttrWithheld,
		Type: model.AttributeBool,
	}}
	entries, size := model.AttributeLayout(summaries)
	values := make(model.AttributeValues, size)
	if withheld {
		values[0] = 1
	}
	return model.NewAttributeView(entries, values)
}
