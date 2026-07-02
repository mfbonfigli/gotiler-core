package pc

import (
	"fmt"
	"sync"

	"github.com/mfbonfigli/golaz"
	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/plugin"
	"github.com/mfbonfigli/gotiler-core/tiler/pointcloud"
)

func init() {
	factory := func(filename, crs string, opts plugin.ReaderOptions) (pointcloud.Reader, error) {
		return NewGoLasReader(filename, crs, opts.EightBitColor, opts.RequestedAttributes)
	}
	plugin.RegisterPointCloudReader(".las", factory)
	plugin.RegisterPointCloudReader(".laz", factory)
}

// GoLasReader wraps a golaz.Reader implementing the specific interface LasReader required by gotiler.
// golaz.Reader is not goroutine-safe, so all reads are serialised with mu while keeping a reusable
// scan buffer to avoid per-point allocations.
// lasExtraAttr is one requested extra-byte attribute, resolved once at
// construction so the per-point path does no name canonicalization.
type lasExtraAttr struct {
	descName   string // name as spelled in the LAS extra-byte descriptor
	outputName string // canonical requested name to emit
	typ        model.AttributeType
	// scaled is true when the descriptor defines a scale and/or offset: the
	// stored value is quantized and the actual value is raw*scale+offset,
	// emitted as float64 (typ is AttributeFloat64 in that case).
	scaled bool
	scale  float64
	offset float64
}

// Known vendor spellings (canonical form) of extra-byte fields carrying the
// same physical quantity. CanonicalAttributeName already folds case and
// whitespace, so only spellings that survive canonicalization need listing.
var (
	// Incidence angle (laser vector vs surface normal):
	// ASPRS "Incidence Angle", GeoCue/LP360 "True View Incidence Angle",
	// OPALS "_IncidenceAngle".
	lasIncidenceAngleNames = []string{"incidenceangle", "trueviewincidenceangle", "_incidenceangle"}
	// Pulse / echo width (return signal widening):
	// ASPRS "Pulse Width" / "Echo Width", RIEGL "Pulse width",
	// OPALS "EchoWidth", Terrasolid "Echo length".
	lasPulseWidthNames = []string{"pulsewidth", "echowidth", "echolength"}
)

// lasExtraByteAliases maps a canonical requested attribute name to the vendor
// spellings of the extra-byte field that carries it. Quantities whose vendor
// spellings all canonicalize to the request itself (e.g. "Amplitude" and
// "Reflectance", used verbatim by RIEGL, OPALS and Terrasolid) need no entry.
var lasExtraByteAliases = map[string][]string{
	"incidence_angle": lasIncidenceAngleNames,
	"incidenceangle":  lasIncidenceAngleNames,
	"pulse_width":     lasPulseWidthNames,
	"pulsewidth":      lasPulseWidthNames,
	"echo_width":      lasPulseWidthNames,
	"echowidth":       lasPulseWidthNames,
}

type GoLasReader struct {
	r             *golaz.Reader
	eightBitColor bool
	crs           string
	requested     map[string]string // source canonical name -> requested canonical name
	emit          lasStdEmitPlan    // requested standard fields, resolved at construction
	extraAttrs    []lasExtraAttr    // requested extra-byte attributes, resolved at construction
	mu            sync.Mutex
	scanBuf       golaz.Point
}

// NewGoLasReader returns a GoLasReader instance. If crs is empty the system will attempt to autodetect
// the CRS from the LAS metadata and return an error in case of issues.
// attrs lists the optional per-point attributes to emit; nil means none.
func NewGoLasReader(fileName string, crs string, eightBitColor bool, attrs model.Attributes) (*GoLasReader, error) {
	r, err := golaz.Open(fileName)
	if err != nil {
		return nil, err
	}
	if crs == "" {
		crs = r.CRS()
		if crs == "" {
			r.Close()
			return nil, fmt.Errorf("no CRS provided and was not possible to determine CRS from LAS file %s", fileName)
		}
	}
	f := &GoLasReader{
		r:             r,
		eightBitColor: eightBitColor,
		crs:           crs,
	}
	if len(attrs) > 0 {
		f.requested = buildRequestedMap(attrs)
		f.emit = buildStdEmitPlan(f.requested)
		f.extraAttrs = f.buildExtraAttrPlan()
	}
	return f, nil
}

func (f *GoLasReader) NumberOfPoints() int {
	return int(f.r.NumPoints())
}

func (f *GoLasReader) GetCRS() string {
	return f.crs
}

func (f *GoLasReader) Reset() error {
	return f.r.Reset()
}

func (f *GoLasReader) Close() {
	f.r.Close()
}

func (f *GoLasReader) GetNext() (geom.Point64, error) {
	f.mu.Lock()
	err := f.r.Scan(&f.scanBuf)
	if err != nil {
		f.mu.Unlock()
		return geom.Point64{}, err
	}
	x, y, z := f.scanBuf.X, f.scanBuf.Y, f.scanBuf.Z
	red, green, blue, _ := f.scanBuf.RGB()
	attrs := f.attributesForPointLocked()
	f.mu.Unlock()

	var corr uint16 = 256
	if f.eightBitColor {
		corr = 1
	}
	return geom.Point64{
		Vector: model.Vector{
			X: x,
			Y: y,
			Z: z,
		},
		R:          uint8(red / corr),
		G:          uint8(green / corr),
		B:          uint8(blue / corr),
		Attributes: attrs,
	}, nil
}

// buildRequestedMap maps every source attribute name that should be emitted to
// the requested (output) name. Each requested name matches itself plus any
// known vendor spelling of the same quantity (see lasExtraByteAliases); on
// collision the earlier request wins.
func buildRequestedMap(attrs model.Attributes) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	requested := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		outputName := model.CanonicalAttributeName(attr)
		if outputName == "" {
			continue
		}
		if _, taken := requested[outputName]; !taken {
			requested[outputName] = outputName
		}
		for _, alias := range lasExtraByteAliases[outputName] {
			if _, taken := requested[alias]; !taken {
				requested[alias] = outputName
			}
		}
	}
	return requested
}

// buildExtraAttrPlan resolves the requested extra-byte attributes against the
// file's descriptors once, so the per-point path needs no descriptor scan or
// name canonicalization.
func (f *GoLasReader) buildExtraAttrPlan() []lasExtraAttr {
	if len(f.requested) == 0 {
		return nil
	}
	var plan []lasExtraAttr
	planned := make(map[string]struct{})
	for _, desc := range f.r.ExtraByteDescriptors() {
		t, ok := lasExtraByteType(desc.DataType)
		if !ok {
			continue
		}
		outputName, ok := f.requested[model.CanonicalAttributeName(desc.Name)]
		if !ok {
			continue
		}
		// A file may carry several vendor spellings aliased to the same
		// requested name; the first matching descriptor wins.
		if _, ok := planned[outputName]; ok {
			continue
		}
		planned[outputName] = struct{}{}
		ea := lasExtraAttr{descName: desc.Name, outputName: outputName, typ: t}
		if desc.HasScale || desc.HasOffset {
			ea.scaled = true
			ea.typ = model.AttributeFloat64
			ea.scale = 1.0
			if desc.HasScale {
				ea.scale = desc.Scale
			}
			if desc.HasOffset {
				ea.offset = desc.Offset
			}
		}
		plan = append(plan, ea)
	}
	return plan
}

// lasStdEmitPlan records which standard LAS point fields were requested. It is
// resolved once at construction so the per-point path does no map lookups:
// attributesForPointLocked runs on the (serial) producer goroutine while the
// reader mutex is held, so per-point work here directly stretches the reading
// phase's wall time. Standard fields have no aliases, so the emitted name is
// always the canonical source name itself.
type lasStdEmitPlan struct {
	intensity                   bool
	classification              bool
	returnNumber                bool
	numberOfReturns             bool
	scanDirectionFlag           bool
	edgeOfFlightLine            bool
	classificationFlags         bool
	synthetic                   bool
	keyPoint                    bool
	withheld                    bool
	overlap                     bool
	userData                    bool
	pointSourceID               bool
	scanAngle                   bool
	gpsTime                     bool
	nir                         bool
	scannerChannel              bool
	wavePacketDescriptorIndex   bool
	waveformDataOffset          bool
	waveformPacketSize          bool
	returnPointWaveformLocation bool
	waveDirection               bool
}

func buildStdEmitPlan(requested map[string]string) lasStdEmitPlan {
	has := func(name string) bool {
		_, ok := requested[name]
		return ok
	}
	return lasStdEmitPlan{
		intensity:                   has(model.AttrIntensity),
		classification:              has(model.AttrClassification),
		returnNumber:                has(model.AttrReturnNumber),
		numberOfReturns:             has(model.AttrNumberOfReturns),
		scanDirectionFlag:           has("scan_direction_flag"),
		edgeOfFlightLine:            has("edge_of_flight_line"),
		classificationFlags:         has("classification_flags"),
		synthetic:                   has("synthetic"),
		keyPoint:                    has("key_point"),
		withheld:                    has("withheld"),
		overlap:                     has("overlap"),
		userData:                    has("user_data"),
		pointSourceID:               has("point_source_id"),
		scanAngle:                   has("scan_angle"),
		gpsTime:                     has("gps_time"),
		nir:                         has("nir"),
		scannerChannel:              has("scanner_channel"),
		wavePacketDescriptorIndex:   has("wave_packet_descriptor_index"),
		waveformDataOffset:          has("waveform_data_offset"),
		waveformPacketSize:          has("waveform_packet_size"),
		returnPointWaveformLocation: has("return_point_waveform_location"),
		waveDirection:               has("waveform_x_t") || has("waveform_y_t") || has("waveform_z_t"),
	}
}

func (f *GoLasReader) attributesForPointLocked() []model.Attribute {
	if len(f.requested) == 0 {
		return nil
	}
	attrs := make([]model.Attribute, 0, len(f.requested))
	e := &f.emit
	if e.intensity {
		attrs = append(attrs, model.Attribute{Name: model.AttrIntensity, Type: model.AttributeUint16, Value: f.scanBuf.Intensity})
	}
	if e.classification {
		attrs = append(attrs, model.Attribute{Name: model.AttrClassification, Type: model.AttributeUint8, Value: f.scanBuf.Classification})
	}
	if e.returnNumber {
		attrs = append(attrs, model.Attribute{Name: model.AttrReturnNumber, Type: model.AttributeUint8, Value: f.scanBuf.ReturnNumber})
	}
	if e.numberOfReturns {
		attrs = append(attrs, model.Attribute{Name: model.AttrNumberOfReturns, Type: model.AttributeUint8, Value: f.scanBuf.NumberOfReturns})
	}
	if e.scanDirectionFlag {
		attrs = append(attrs, model.Attribute{Name: "scan_direction_flag", Type: model.AttributeBool, Value: f.scanBuf.ScanDirectionFlag})
	}
	if e.edgeOfFlightLine {
		attrs = append(attrs, model.Attribute{Name: "edge_of_flight_line", Type: model.AttributeBool, Value: f.scanBuf.EdgeOfFlightLine})
	}
	if e.classificationFlags {
		attrs = append(attrs, model.Attribute{Name: "classification_flags", Type: model.AttributeUint8, Value: f.scanBuf.ClassificationFlags})
	}
	if e.synthetic {
		attrs = append(attrs, model.Attribute{Name: "synthetic", Type: model.AttributeBool, Value: f.scanBuf.ClassificationFlags&1 != 0})
	}
	if e.keyPoint {
		attrs = append(attrs, model.Attribute{Name: "key_point", Type: model.AttributeBool, Value: f.scanBuf.ClassificationFlags&2 != 0})
	}
	if e.withheld {
		attrs = append(attrs, model.Attribute{Name: "withheld", Type: model.AttributeBool, Value: f.scanBuf.ClassificationFlags&4 != 0})
	}
	if e.overlap {
		attrs = append(attrs, model.Attribute{Name: "overlap", Type: model.AttributeBool, Value: f.scanBuf.ClassificationFlags&8 != 0})
	}
	if e.userData {
		attrs = append(attrs, model.Attribute{Name: "user_data", Type: model.AttributeUint8, Value: f.scanBuf.UserData})
	}
	if e.pointSourceID {
		attrs = append(attrs, model.Attribute{Name: "point_source_id", Type: model.AttributeUint16, Value: f.scanBuf.PointSourceID})
	}
	if e.scanAngle {
		attrs = append(attrs, model.Attribute{Name: "scan_angle", Type: model.AttributeFloat64, Value: f.scanBuf.ScanAngleDegrees})
	}
	if e.gpsTime {
		if gpsTime, ok := f.scanBuf.GPSTime(); ok {
			attrs = append(attrs, model.Attribute{Name: "gps_time", Type: model.AttributeFloat64, Value: gpsTime})
		}
	}
	if e.nir {
		if nir, ok := f.scanBuf.NIR(); ok {
			attrs = append(attrs, model.Attribute{Name: "nir", Type: model.AttributeUint16, Value: nir})
		}
	}
	if e.scannerChannel {
		if scannerChannel, ok := f.scanBuf.ScannerChannel(); ok {
			attrs = append(attrs, model.Attribute{Name: "scanner_channel", Type: model.AttributeUint8, Value: scannerChannel})
		}
	}
	if e.wavePacketDescriptorIndex {
		if waveIdx, ok := f.scanBuf.WavePacketDescriptorIndex(); ok {
			attrs = append(attrs, model.Attribute{Name: "wave_packet_descriptor_index", Type: model.AttributeUint8, Value: waveIdx})
		}
	}
	if e.waveformDataOffset {
		if waveOffset, ok := f.scanBuf.WaveformDataOffset(); ok {
			attrs = append(attrs, model.Attribute{Name: "waveform_data_offset", Type: model.AttributeUint64, Value: waveOffset})
		}
	}
	if e.waveformPacketSize {
		if waveSize, ok := f.scanBuf.WaveformPacketSize(); ok {
			attrs = append(attrs, model.Attribute{Name: "waveform_packet_size", Type: model.AttributeUint32, Value: waveSize})
		}
	}
	if e.returnPointWaveformLocation {
		if waveLoc, ok := f.scanBuf.ReturnPointWaveformLocation(); ok {
			attrs = append(attrs, model.Attribute{Name: "return_point_waveform_location", Type: model.AttributeFloat32, Value: waveLoc})
		}
	}
	if e.waveDirection {
		if waveX, waveY, waveZ, ok := f.scanBuf.WaveDirection(); ok {
			if _, ok := f.requested["waveform_x_t"]; ok {
				attrs = append(attrs, model.Attribute{Name: "waveform_x_t", Type: model.AttributeFloat32, Value: waveX})
			}
			if _, ok := f.requested["waveform_y_t"]; ok {
				attrs = append(attrs, model.Attribute{Name: "waveform_y_t", Type: model.AttributeFloat32, Value: waveY})
			}
			if _, ok := f.requested["waveform_z_t"]; ok {
				attrs = append(attrs, model.Attribute{Name: "waveform_z_t", Type: model.AttributeFloat32, Value: waveZ})
			}
		}
	}
	for _, ea := range f.extraAttrs {
		value, err := f.r.ExtraByte(&f.scanBuf, ea.descName)
		if err != nil {
			continue
		}
		if ea.scaled {
			raw, ok := extraByteAsFloat64(value)
			if !ok {
				continue
			}
			value = raw*ea.scale + ea.offset
		}
		attrs = append(attrs, model.Attribute{Name: ea.outputName, Type: ea.typ, Value: value})
	}
	return attrs
}

// extraByteAsFloat64 converts a raw extra-byte scalar (any of the ten LAS 1.4
// scalar types as returned by golaz) to float64 for scale/offset application.
func extraByteAsFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case uint8:
		return float64(n), true
	case int8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case int16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// lasExtraByteType maps a LAS 1.4 extra-byte data type to a model attribute type.
// Type 0 (undocumented raw bytes) and the deprecated array types (11-30) are
// not representable as scalars and are rejected.
func lasExtraByteType(t golaz.ExtraByteType) (model.AttributeType, bool) {
	switch t {
	case 1:
		return model.AttributeUint8, true
	case 2:
		return model.AttributeInt8, true
	case 3:
		return model.AttributeUint16, true
	case 4:
		return model.AttributeInt16, true
	case 5:
		return model.AttributeUint32, true
	case 6:
		return model.AttributeInt32, true
	case 7:
		return model.AttributeUint64, true
	case 8:
		return model.AttributeInt64, true
	case 9:
		return model.AttributeFloat32, true
	case 10:
		return model.AttributeFloat64, true
	default:
		return "", false
	}
}
