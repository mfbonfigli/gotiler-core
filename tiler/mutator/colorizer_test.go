package mutator

import (
	"reflect"
	"testing"

	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

func TestRegisterColorGradientUsesExactAliasAndCopiesDefinition(t *testing.T) {
	gradient := ColorGradientScale{
		Nums:              []float64{0, 1},
		Colors:            []Color{{R: 1}, {R: 2}},
		InterpolationMode: GradientInterpolationFlat,
	}
	if err := RegisterColorGradient(" Test Scale ", gradient); err != nil {
		t.Fatalf("RegisterColorGradient: %v", err)
	}
	gradient.Colors[0].R = 99

	got, ok := RegisteredColorGradient(" Test Scale ")
	if !ok {
		t.Fatal("expected registered gradient")
	}
	if got.Colors[0].R != 1 {
		t.Fatalf("expected registry to copy gradient, got %v", got.Colors[0].R)
	}
	got.Colors[0].R = 42
	again, _ := RegisteredColorGradient(" Test Scale ")
	if again.Colors[0].R != 1 {
		t.Fatalf("expected lookup to return a copy, got %v", again.Colors[0].R)
	}
	if _, ok := RegisteredColorGradient("testscale"); ok {
		t.Fatal("expected aliases to match exactly")
	}
}

func TestRegisterColorGradientRejectsInvalidDefinitions(t *testing.T) {
	cases := []ColorGradientScale{
		{Nums: []float64{0}, Colors: []Color{{}}, InterpolationMode: GradientInterpolationFlat},
		{Nums: []float64{0, 1}, Colors: []Color{{}}, InterpolationMode: GradientInterpolationFlat},
		{Nums: []float64{0.1, 1}, Colors: []Color{{}, {}}, InterpolationMode: GradientInterpolationFlat},
		{Nums: []float64{0, 0.5}, Colors: []Color{{}, {}}, InterpolationMode: GradientInterpolationFlat},
		{Nums: []float64{0, 0.5, 0.5, 1}, Colors: []Color{{}, {}, {}, {}}, InterpolationMode: GradientInterpolationFlat},
		{Nums: []float64{0, 1}, Colors: []Color{{}, {}}, InterpolationMode: GradientInterpolationMode(99)},
	}
	for i, c := range cases {
		if err := RegisterColorGradient("bad-gradient", c); err == nil {
			t.Fatalf("case %d: expected invalid gradient to be rejected", i)
		}
	}
}

func TestNewColorizerRequiredAttributes(t *testing.T) {
	c, err := NewColorizerWithGradient(" IntensitY ", 0, 1, testLinearGradient())
	if err != nil {
		t.Fatalf("NewColorizerWithGradient: %v", err)
	}
	got := c.RequiredAttributes()
	if len(got) != 1 || !got.Has(model.AttrIntensity) {
		t.Fatalf("expected intensity required attribute, got %v", got)
	}
}

func TestNewColorizerPointCoordinateRequiresNoAttributes(t *testing.T) {
	c, err := NewColorizerWithGradient(" X ", 0, 1, testLinearGradient())
	if err != nil {
		t.Fatalf("NewColorizerWithGradient: %v", err)
	}
	if got := c.RequiredAttributes(); len(got) != 0 {
		t.Fatalf("expected no required attributes for point coordinate colorizer, got %v", got)
	}
}

func TestColorizerFlatInterpolation(t *testing.T) {
	gradient := ColorGradientScale{
		Nums: []float64{0, 0.3, 0.6, 1},
		Colors: []Color{
			{R: 255, G: 0, B: 0},
			{R: 0, G: 0, B: 255},
			{R: 255, G: 255, B: 0},
			{R: 0, G: 255, B: 0},
		},
		InterpolationMode: GradientInterpolationFlat,
	}
	c, err := NewColorizerWithGradient(model.AttrIntensity, 10, 20, gradient)
	if err != nil {
		t.Fatalf("NewColorizerWithGradient: %v", err)
	}

	cases := []struct {
		value uint16
		want  Color
	}{
		{9, Color{R: 255}},
		{10, Color{R: 255}},
		{12, Color{R: 255}},
		{13, Color{B: 255}},
		{15, Color{B: 255}},
		{16, Color{R: 255, G: 255}},
		{20, Color{G: 255}},
		{21, Color{G: 255}},
	}

	for _, tc := range cases {
		pt, keep := c.Mutate(geom.NewPoint(0, 0, 0, 1, 2, 3), colorizerView(model.AttrIntensity, model.AttributeUint16, tc.value), model.IdentityTransform)
		if !keep {
			t.Fatalf("value %d: expected point to be kept", tc.value)
		}
		got := Color{R: pt.R, G: pt.G, B: pt.B}
		if got != tc.want {
			t.Fatalf("value %d: got color %+v want %+v", tc.value, got, tc.want)
		}
	}
}

func TestColorizerLinearInterpolation(t *testing.T) {
	c, err := NewColorizerWithGradient(model.AttrIntensity, 0, 10, testLinearGradient())
	if err != nil {
		t.Fatalf("NewColorizerWithGradient: %v", err)
	}
	pt, keep := c.Mutate(geom.NewPoint(0, 0, 0, 1, 2, 3), colorizerView(model.AttrIntensity, model.AttributeFloat32, float32(5)), model.IdentityTransform)
	if !keep {
		t.Fatal("expected point to be kept")
	}
	got := Color{R: pt.R, G: pt.G, B: pt.B}
	want := Color{R: 128, G: 128, B: 128}
	if got != want {
		t.Fatalf("got color %+v want %+v", got, want)
	}
}

func TestColorizerUsesPointCoordinates(t *testing.T) {
	cases := []struct {
		attribute string
		point     model.Point
		want      Color
	}{
		{
			attribute: "x",
			point:     geom.NewPoint(0, 99, 99, 1, 2, 3),
			want:      Color{},
		},
		{
			attribute: "y",
			point:     geom.NewPoint(99, 5, 99, 1, 2, 3),
			want:      Color{R: 128, G: 128, B: 128},
		},
		{
			attribute: "z",
			point:     geom.NewPoint(99, 99, 10, 1, 2, 3),
			want:      Color{R: 255, G: 255, B: 255},
		},
	}
	for _, tc := range cases {
		c, err := NewColorizerWithGradient(tc.attribute, 0, 10, testLinearGradient())
		if err != nil {
			t.Fatalf("%s: NewColorizerWithGradient: %v", tc.attribute, err)
		}
		pt, keep := c.Mutate(tc.point, model.AttributeView{}, model.IdentityTransform)
		if !keep {
			t.Fatalf("%s: expected point to be kept", tc.attribute)
		}
		got := Color{R: pt.R, G: pt.G, B: pt.B}
		if got != tc.want {
			t.Fatalf("%s: got color %+v want %+v", tc.attribute, got, tc.want)
		}
	}
}

func TestColorizerUsesRegisteredGradientAlias(t *testing.T) {
	if err := RegisterColorGradient("Alias Gradient", testLinearGradient()); err != nil {
		t.Fatalf("RegisterColorGradient: %v", err)
	}
	c, err := NewColorizer(model.AttrIntensity, 0, 10, "Alias Gradient")
	if err != nil {
		t.Fatalf("NewColorizer: %v", err)
	}
	pt, keep := c.Mutate(geom.NewPoint(0, 0, 0, 1, 2, 3), colorizerView(model.AttrIntensity, model.AttributeUint8, uint8(10)), model.IdentityTransform)
	if !keep {
		t.Fatal("expected point to be kept")
	}
	got := Color{R: pt.R, G: pt.G, B: pt.B}
	want := Color{R: 255, G: 255, B: 255}
	if got != want {
		t.Fatalf("got color %+v want %+v", got, want)
	}
}

func TestColorizerLASClassificationGradient(t *testing.T) {
	c, err := NewColorizer(model.AttrClassification, 0, 255, "las-classification")
	if err != nil {
		t.Fatalf("NewColorizer: %v", err)
	}
	cases := []struct {
		class uint8
		want  Color
	}{
		{0, Color{R: 180, G: 180, B: 180}},
		{2, Color{R: 139, G: 100, B: 60}},
		{5, Color{R: 20, G: 120, B: 40}},
		{6, Color{R: 210, G: 70, B: 60}},
		{9, Color{R: 40, G: 120, B: 230}},
		{18, Color{R: 255, G: 60, B: 180}},
		{19, Color{R: 120, G: 120, B: 120}},
		{255, Color{R: 120, G: 120, B: 120}},
	}
	for _, tc := range cases {
		pt, keep := c.Mutate(geom.NewPoint(0, 0, 0, 1, 2, 3), colorizerView(model.AttrClassification, model.AttributeUint8, tc.class), model.IdentityTransform)
		if !keep {
			t.Fatalf("class %d: expected point to be kept", tc.class)
		}
		got := Color{R: pt.R, G: pt.G, B: pt.B}
		if got != tc.want {
			t.Fatalf("class %d: got color %+v want %+v", tc.class, got, tc.want)
		}
	}
}

func TestColorizerLeavesPointUnchangedWhenAttributeMissing(t *testing.T) {
	c, err := NewColorizerWithGradient(model.AttrIntensity, 0, 10, testLinearGradient())
	if err != nil {
		t.Fatalf("NewColorizerWithGradient: %v", err)
	}
	input := geom.NewPoint(0, 0, 0, 10, 20, 30)
	got, keep := c.Mutate(input, colorizerView(model.AttrClassification, model.AttributeUint8, uint8(5)), model.IdentityTransform)
	if !keep {
		t.Fatal("expected point to be kept")
	}
	if !reflect.DeepEqual(got, input) {
		t.Fatalf("expected unchanged point, got %+v want %+v", got, input)
	}
}

func TestColorizerSupportsAllAttributeTypes(t *testing.T) {
	c, err := NewColorizerWithGradient("value", 0, 1, testLinearGradient())
	if err != nil {
		t.Fatalf("NewColorizerWithGradient: %v", err)
	}
	cases := []struct {
		typ   model.AttributeType
		value any
	}{
		{model.AttributeInt8, int8(1)},
		{model.AttributeUint8, uint8(1)},
		{model.AttributeInt16, int16(1)},
		{model.AttributeUint16, uint16(1)},
		{model.AttributeInt32, int32(1)},
		{model.AttributeUint32, uint32(1)},
		{model.AttributeInt64, int64(1)},
		{model.AttributeUint64, uint64(1)},
		{model.AttributeBool, true},
		{model.AttributeFloat32, float32(1)},
		{model.AttributeFloat64, float64(1)},
	}
	for _, tc := range cases {
		pt, keep := c.Mutate(geom.NewPoint(0, 0, 0, 1, 2, 3), colorizerView("value", tc.typ, tc.value), model.IdentityTransform)
		if !keep {
			t.Fatalf("type %s: expected point to be kept", tc.typ)
		}
		got := Color{R: pt.R, G: pt.G, B: pt.B}
		want := Color{R: 255, G: 255, B: 255}
		if got != want {
			t.Fatalf("type %s: got color %+v want %+v", tc.typ, got, want)
		}
	}
}

func testLinearGradient() ColorGradientScale {
	return ColorGradientScale{
		Nums:              []float64{0, 1},
		Colors:            []Color{{R: 0, G: 0, B: 0}, {R: 255, G: 255, B: 255}},
		InterpolationMode: GradientInterpolationLinear,
	}
}

func colorizerView(name string, typ model.AttributeType, value any) model.AttributeView {
	summaries := []model.AttributeSummary{{
		Name: model.CanonicalAttributeName(name),
		Type: typ,
	}}
	entries, size := model.AttributeLayout(summaries)
	values := make(model.AttributeValues, size)
	if err := model.EncodeAttributeValue(values[entries[0].Offset:entries[0].Offset+entries[0].Size], typ, value); err != nil {
		panic(err)
	}
	return model.NewAttributeView(entries, values)
}
