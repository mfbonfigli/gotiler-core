package pc

import (
	"testing"

	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

func TestExtraByteAsFloat64(t *testing.T) {
	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{uint8(200), 200, true},
		{int8(-5), -5, true},
		{uint16(60000), 60000, true},
		{int16(-30000), -30000, true},
		{uint32(4000000000), 4000000000, true},
		{int32(-2000000000), -2000000000, true},
		{uint64(1 << 40), 1 << 40, true},
		{int64(-(1 << 40)), -(1 << 40), true},
		{float32(1.5), 1.5, true},
		{float64(-2.25), -2.25, true},
		{"not a scalar", 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, ok := extraByteAsFloat64(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("extraByteAsFloat64(%v (%T)): got (%v, %v) want (%v, %v)", c.in, c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestBuildRequestedMapAliases(t *testing.T) {
	cases := []struct {
		name      string
		requested []string
		want      map[string]string
	}{
		{
			name:      "plain names map to themselves",
			requested: []string{"Intensity", "gps_time"},
			want: map[string]string{
				"intensity": "intensity",
				"gps_time":  "gps_time",
			},
		},
		{
			name:      "amplitude and reflectance match via canonicalization alone",
			requested: []string{"Amplitude", "Reflectance"},
			want: map[string]string{
				"amplitude":   "amplitude",
				"reflectance": "reflectance",
			},
		},
		{
			name:      "incidence angle matches all vendor spellings",
			requested: []string{"incidence_angle"},
			want: map[string]string{
				"incidence_angle":        "incidence_angle",
				"incidenceangle":         "incidence_angle",
				"trueviewincidenceangle": "incidence_angle",
				"_incidenceangle":        "incidence_angle",
			},
		},
		{
			name:      "ASPRS spelling of incidence angle also expands",
			requested: []string{"Incidence Angle"},
			want: map[string]string{
				"incidenceangle":         "incidenceangle",
				"trueviewincidenceangle": "incidenceangle",
				"_incidenceangle":        "incidenceangle",
			},
		},
		{
			name:      "pulse width matches echo width and echo length spellings",
			requested: []string{"pulse_width"},
			want: map[string]string{
				"pulse_width": "pulse_width",
				"pulsewidth":  "pulse_width",
				"echowidth":   "pulse_width",
				"echolength":  "pulse_width",
			},
		},
		{
			name:      "colliding requests: earlier request wins the shared variants",
			requested: []string{"pulse_width", "echo_width"},
			want: map[string]string{
				"pulse_width": "pulse_width",
				"pulsewidth":  "pulse_width",
				"echowidth":   "pulse_width",
				"echolength":  "pulse_width",
				"echo_width":  "echo_width",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildRequestedMap(model.NewAttributes(c.requested...))
			if len(got) != len(c.want) {
				t.Fatalf("map size: got %d (%v) want %d (%v)", len(got), got, len(c.want), c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("requested[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestGoLasReaderEmitsRequestedAttributes reads a real LAS fixture and checks
// that requested standard attributes are emitted with the right names/types.
func TestGoLasReaderEmitsRequestedAttributes(t *testing.T) {
	r, err := NewGoLasReader("testdata/las-12-pf1.las", "EPSG:32633", false,
		model.NewAttributes("Intensity", "classification", "gps_time", "scan_angle", "point_source_id", "not_present_attr"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer r.Close()

	pt, err := r.GetNext()
	if err != nil {
		t.Fatalf("GetNext: %v", err)
	}
	want := map[string]model.AttributeType{
		"intensity":       model.AttributeUint16,
		"classification":  model.AttributeUint8,
		"gps_time":        model.AttributeFloat64,
		"scan_angle":      model.AttributeFloat64,
		"point_source_id": model.AttributeUint16,
	}
	got := map[string]model.AttributeType{}
	for _, a := range pt.Attributes {
		got[a.Name] = a.Type
		if a.Value == nil {
			t.Errorf("attribute %q has nil value", a.Name)
		}
	}
	for name, typ := range want {
		if got[name] != typ {
			t.Errorf("attribute %q: got type %q want %q (attrs: %v)", name, got[name], typ, pt.Attributes)
		}
	}
	if len(got) != len(want) {
		t.Errorf("expected %d attributes, got %d: %v", len(want), len(got), pt.Attributes)
	}
}

// TestGoLasReaderNoAttributesRequested ensures the reader emits nothing when
// no attributes are requested.
func TestGoLasReaderNoAttributesRequested(t *testing.T) {
	r, err := NewGoLasReader("testdata/las-12-pf1.las", "EPSG:32633", false, nil)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer r.Close()
	pt, err := r.GetNext()
	if err != nil {
		t.Fatalf("GetNext: %v", err)
	}
	if pt.Attributes != nil {
		t.Errorf("expected no attributes, got %v", pt.Attributes)
	}
}
