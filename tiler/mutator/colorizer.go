package mutator

import (
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

// Color is an RGB color used by ColorGradientScale.
type Color struct {
	R uint8
	G uint8
	B uint8
}

// GradientInterpolationMode controls how colors are selected between gradient stops.
type GradientInterpolationMode int

const (
	// GradientInterpolationFlat selects the color of the lower stop for the interval.
	GradientInterpolationFlat GradientInterpolationMode = iota
	// GradientInterpolationLinear linearly interpolates colors between stops.
	GradientInterpolationLinear
)

// ColorGradientScale maps normalized values in [0, 1] to RGB colors.
// Nums must be sorted, strictly increasing, start at 0, and end at 1.
type ColorGradientScale struct {
	Nums              []float64
	Colors            []Color
	InterpolationMode GradientInterpolationMode
}

var (
	colorGradientRegistryMu sync.RWMutex
	colorGradientRegistry   = map[string]ColorGradientScale{
		"grayscale": {
			Nums:              []float64{0, 1},
			Colors:            []Color{{R: 0, G: 0, B: 0}, {R: 255, G: 255, B: 255}},
			InterpolationMode: GradientInterpolationLinear,
		},
		"heat": {
			Nums: []float64{0, 0.33, 0.66, 1},
			Colors: []Color{
				{R: 0, G: 0, B: 0},
				{R: 220, G: 0, B: 0},
				{R: 255, G: 220, B: 0},
				{R: 255, G: 255, B: 255},
			},
			InterpolationMode: GradientInterpolationLinear,
		},
		"viridis": {
			Nums: []float64{0, 0.25, 0.5, 0.75, 1},
			Colors: []Color{
				{R: 68, G: 1, B: 84},
				{R: 59, G: 82, B: 139},
				{R: 33, G: 145, B: 140},
				{R: 94, G: 201, B: 98},
				{R: 253, G: 231, B: 37},
			},
			InterpolationMode: GradientInterpolationLinear,
		},
		"las-classification": {
			Nums: []float64{
				0,
				1.0 / 255.0,
				2.0 / 255.0,
				3.0 / 255.0,
				4.0 / 255.0,
				5.0 / 255.0,
				6.0 / 255.0,
				7.0 / 255.0,
				8.0 / 255.0,
				9.0 / 255.0,
				10.0 / 255.0,
				11.0 / 255.0,
				12.0 / 255.0,
				13.0 / 255.0,
				14.0 / 255.0,
				15.0 / 255.0,
				16.0 / 255.0,
				17.0 / 255.0,
				18.0 / 255.0,
				19.0 / 255.0,
				1,
			},
			Colors: []Color{
				{R: 180, G: 180, B: 180}, // 0: Created, never classified
				{R: 220, G: 220, B: 220}, // 1: Unclassified
				{R: 139, G: 100, B: 60},  // 2: Ground
				{R: 170, G: 220, B: 120}, // 3: Low vegetation
				{R: 80, G: 180, B: 80},   // 4: Medium vegetation
				{R: 20, G: 120, B: 40},   // 5: High vegetation
				{R: 210, G: 70, B: 60},   // 6: Building
				{R: 220, G: 0, B: 220},   // 7: Low point / noise
				{R: 0, G: 200, B: 200},   // 8: Model key-point
				{R: 40, G: 120, B: 230},  // 9: Water
				{R: 90, G: 90, B: 90},    // 10: Rail
				{R: 55, G: 55, B: 55},    // 11: Road surface
				{R: 255, G: 210, B: 0},   // 12: Overlap
				{R: 255, G: 150, B: 60},  // 13: Wire guard
				{R: 255, G: 235, B: 80},  // 14: Wire conductor
				{R: 150, G: 80, B: 200},  // 15: Transmission tower
				{R: 255, G: 180, B: 120}, // 16: Wire connector
				{R: 180, G: 140, B: 90},  // 17: Bridge deck
				{R: 255, G: 60, B: 180},  // 18: High noise
				{R: 120, G: 120, B: 120}, // 19-255: Unknown / reserved
				{R: 120, G: 120, B: 120},
			},
			InterpolationMode: GradientInterpolationFlat,
		},
	}
)

// RegisterColorGradient registers or replaces a global gradient alias.
func RegisterColorGradient(alias string, gradient ColorGradientScale) error {
	if alias == "" {
		return fmt.Errorf("gradient alias cannot be empty")
	}
	if err := validateColorGradient(gradient); err != nil {
		return err
	}
	colorGradientRegistryMu.Lock()
	colorGradientRegistry[alias] = cloneColorGradient(gradient)
	colorGradientRegistryMu.Unlock()
	return nil
}

// RegisteredColorGradient returns a copy of the gradient registered for alias.
func RegisteredColorGradient(alias string) (ColorGradientScale, bool) {
	colorGradientRegistryMu.RLock()
	gradient, ok := colorGradientRegistry[alias]
	colorGradientRegistryMu.RUnlock()
	if !ok {
		return ColorGradientScale{}, false
	}
	return cloneColorGradient(gradient), true
}

// Colorizer colors points using a numeric per-point attribute.
type Colorizer struct {
	attribute string
	nums      []float64
	colors    []Color
	mode      GradientInterpolationMode
}

// NewColorizer creates a Colorizer from a registered gradient alias. The
// gradient stops are scaled so normalized 0 maps to minRange and 1 maps to maxRange.
func NewColorizer(attributeName string, minRange, maxRange float64, gradientAlias string) (*Colorizer, error) {
	gradient, ok := RegisteredColorGradient(gradientAlias)
	if !ok {
		return nil, fmt.Errorf("unknown color gradient %q", gradientAlias)
	}
	return NewColorizerWithGradient(attributeName, minRange, maxRange, gradient)
}

// NewColorizerWithGradient creates a Colorizer using an explicit gradient definition.
func NewColorizerWithGradient(attributeName string, minRange, maxRange float64, gradient ColorGradientScale) (*Colorizer, error) {
	attribute := model.CanonicalAttributeName(attributeName)
	if attribute == "" {
		return nil, fmt.Errorf("attribute name cannot be empty")
	}
	if !isFinite(minRange) || !isFinite(maxRange) {
		return nil, fmt.Errorf("colorizer range must be finite")
	}
	if minRange >= maxRange {
		return nil, fmt.Errorf("colorizer min range %v must be less than max range %v", minRange, maxRange)
	}
	if err := validateColorGradient(gradient); err != nil {
		return nil, err
	}
	nums := make([]float64, len(gradient.Nums))
	scale := maxRange - minRange
	for i, n := range gradient.Nums {
		nums[i] = minRange + n*scale
	}
	colors := append([]Color(nil), gradient.Colors...)
	return &Colorizer{
		attribute: attribute,
		nums:      nums,
		colors:    colors,
		mode:      gradient.InterpolationMode,
	}, nil
}

func (c *Colorizer) RequiredAttributes() model.Attributes {
	if c == nil {
		return nil
	}
	if colorizerPointField(c.attribute) {
		return nil
	}
	return model.NewAttributes(c.attribute)
}

func (c *Colorizer) MutateChunk(chunk PointChunk, localToGlobal model.Transform) []model.Point {
	if c == nil || len(chunk.Points) == 0 {
		return chunk.Points
	}
	// the field-vs-attribute decision, the attribute lookup and the type
	// dispatch are invariant across a chunk: resolve them once up front
	// instead of re-evaluating them for every point.
	if fieldValue := colorizerPointFieldGetter(c.attribute); fieldValue != nil {
		for i := range chunk.Points {
			value := fieldValue(chunk.Points[i])
			if !isFinite(value) {
				continue
			}
			c.colorizePoint(&chunk.Points[i], value)
		}
		return chunk.Points
	}
	layout := model.NewAttributeView(chunk.AttributeLayout, nil)
	attrIndex := layout.Index(c.attribute)
	if attrIndex < 0 {
		return chunk.Points
	}
	extract := attributeFloat64Extractor(layout.Type(attrIndex))
	if extract == nil {
		return chunk.Points
	}
	for i := range chunk.Points {
		value, ok := extract(chunk.AttributeView(i), attrIndex)
		if !ok {
			continue
		}
		c.colorizePoint(&chunk.Points[i], value)
	}
	return chunk.Points
}

func (c *Colorizer) Close() error {
	return nil
}

func (c *Colorizer) colorizePoint(pt *model.Point, value float64) {
	color := c.color(value)
	pt.R = color.R
	pt.G = color.G
	pt.B = color.B
}

func colorizerPointField(attribute string) bool {
	return colorizerPointFieldGetter(attribute) != nil
}

// colorizerPointFieldGetter returns a getter for the point field named by
// attribute, or nil if the attribute does not name a point field.
func colorizerPointFieldGetter(attribute string) func(model.Point) float64 {
	switch attribute {
	case "x":
		return func(pt model.Point) float64 { return float64(pt.X) }
	case "y":
		return func(pt model.Point) float64 { return float64(pt.Y) }
	case "z":
		return func(pt model.Point) float64 { return float64(pt.Z) }
	default:
		return nil
	}
}

func (c *Colorizer) color(value float64) Color {
	if value <= c.nums[0] {
		return c.colors[0]
	}
	last := len(c.nums) - 1
	if value >= c.nums[last] {
		return c.colors[last]
	}
	hi := sort.Search(len(c.nums), func(i int) bool {
		return c.nums[i] > value
	})
	lo := hi - 1
	if c.mode == GradientInterpolationFlat {
		return c.colors[lo]
	}
	den := c.nums[hi] - c.nums[lo]
	if den <= 0 {
		return c.colors[lo]
	}
	t := (value - c.nums[lo]) / den
	return interpolateColor(c.colors[lo], c.colors[hi], t)
}

// attributeFloat64Extractor returns a function that reads the i-th attribute
// of a view as a float64, or nil if the type is unsupported. The type dispatch
// happens once here so the returned extractor can run in per-point hot loops.
func attributeFloat64Extractor(typ model.AttributeType) func(attrs model.AttributeView, i int) (float64, bool) {
	switch typ {
	case model.AttributeInt8:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Int8(i)
			return float64(v), err == nil
		}
	case model.AttributeUint8:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Uint8(i)
			return float64(v), err == nil
		}
	case model.AttributeInt16:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Int16(i)
			return float64(v), err == nil
		}
	case model.AttributeUint16:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Uint16(i)
			return float64(v), err == nil
		}
	case model.AttributeInt32:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Int32(i)
			return float64(v), err == nil
		}
	case model.AttributeUint32:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Uint32(i)
			return float64(v), err == nil
		}
	case model.AttributeInt64:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Int64(i)
			return float64(v), err == nil
		}
	case model.AttributeUint64:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Uint64(i)
			return float64(v), err == nil
		}
	case model.AttributeBool:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Bool(i)
			if v {
				return 1, err == nil
			}
			return 0, err == nil
		}
	case model.AttributeFloat32:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Float32(i)
			return float64(v), err == nil && isFinite(float64(v))
		}
	case model.AttributeFloat64:
		return func(attrs model.AttributeView, i int) (float64, bool) {
			v, err := attrs.Float64(i)
			return v, err == nil && isFinite(v)
		}
	default:
		return nil
	}
}

func interpolateColor(a, b Color, t float64) Color {
	return Color{
		R: interpolateChannel(a.R, b.R, t),
		G: interpolateChannel(a.G, b.G, t),
		B: interpolateChannel(a.B, b.B, t),
	}
}

func interpolateChannel(a, b uint8, t float64) uint8 {
	v := float64(a) + (float64(b)-float64(a))*t
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(math.Round(v))
}

func validateColorGradient(gradient ColorGradientScale) error {
	if len(gradient.Nums) != len(gradient.Colors) {
		return fmt.Errorf("gradient has %d numeric stops and %d colors", len(gradient.Nums), len(gradient.Colors))
	}
	if len(gradient.Nums) < 2 {
		return fmt.Errorf("gradient must contain at least two stops")
	}
	switch gradient.InterpolationMode {
	case GradientInterpolationFlat, GradientInterpolationLinear:
	default:
		return fmt.Errorf("unsupported gradient interpolation mode %d", gradient.InterpolationMode)
	}
	for i, n := range gradient.Nums {
		if !isFinite(n) || n < 0 || n > 1 {
			return fmt.Errorf("gradient stop %d is %v, expected a finite value in [0, 1]", i, n)
		}
		if i > 0 && n <= gradient.Nums[i-1] {
			return fmt.Errorf("gradient stops must be strictly increasing")
		}
	}
	if gradient.Nums[0] != 0 {
		return fmt.Errorf("gradient first stop must be 0")
	}
	if gradient.Nums[len(gradient.Nums)-1] != 1 {
		return fmt.Errorf("gradient last stop must be 1")
	}
	return nil
}

func cloneColorGradient(gradient ColorGradientScale) ColorGradientScale {
	return ColorGradientScale{
		Nums:              append([]float64(nil), gradient.Nums...),
		Colors:            append([]Color(nil), gradient.Colors...),
		InterpolationMode: gradient.InterpolationMode,
	}
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
