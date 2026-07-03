package pc

import (
	"encoding/binary"
	"fmt"
	"math"
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
	// packed layout position within the point's AttributeValues
	blobOff  int
	blobSize int
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
	schema        []model.AttributeDescriptor
	plan          lasEncodePlan  // byte offsets of requested standard fields, resolved at construction
	extraAttrs    []lasExtraAttr // requested extra-byte attributes, resolved at construction
	arena         model.AttributeValuesArena
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
		f.extraAttrs = f.buildExtraAttrPlan()
		f.buildSchema()
	}
	return f, nil
}

// AttributeSchema implements pointcloud.Reader.
func (f *GoLasReader) AttributeSchema() []model.AttributeDescriptor {
	return f.schema
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
	attrs := f.encodeAttributesLocked()
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

// lasEncodePlan holds the packed-value byte offset of every requested
// standard LAS point field, or -1 when the field is not part of the schema
// (not requested, or not carried by the file's point format). It is resolved
// once at construction so the per-point path is straight-line binary encoding:
// encodeAttributesLocked runs on the serial producer goroutine while the
// reader mutex is held, so per-point work there directly stretches the reading
// phase's wall time.
type lasEncodePlan struct {
	size                        int
	intensity                   int
	classification              int
	returnNumber                int
	numberOfReturns             int
	scanDirectionFlag           int
	edgeOfFlightLine            int
	classificationFlags         int
	synthetic                   int
	keyPoint                    int
	withheld                    int
	overlap                     int
	userData                    int
	pointSourceID               int
	scanAngle                   int
	gpsTime                     int
	nir                         int
	scannerChannel              int
	wavePacketDescriptorIndex   int
	waveformDataOffset          int
	waveformPacketSize          int
	returnPointWaveformLocation int
	waveformXT                  int
	waveformYT                  int
	waveformZT                  int
}

// lasFormatCaps reports which optional field groups a LAS point data format
// carries, mirroring the LAS 1.4 specification (and golaz's decode table).
func lasFormatCaps(pf uint8) (gps, color, nir, wave, extended bool) {
	switch pf {
	case 1:
		return true, false, false, false, false
	case 2:
		return false, true, false, false, false
	case 3:
		return true, true, false, false, false
	case 4:
		return true, false, false, true, false
	case 5:
		return true, true, false, true, false
	case 6:
		return true, false, false, false, true
	case 7:
		return true, true, false, false, true
	case 8:
		return true, true, true, false, true
	case 9:
		return true, false, false, true, true
	case 10:
		return true, true, true, true, true
	default:
		return false, false, false, false, false
	}
}

// buildSchema resolves the reader's attribute schema and packed layout:
// requested standard fields the point format carries (in a fixed emission
// order), followed by the requested extra-byte attributes. Must run after
// buildExtraAttrPlan.
func (f *GoLasReader) buildSchema() {
	p := &f.plan
	gps, _, nir, wave, extended := lasFormatCaps(f.r.Header().PointDataFormat)

	type stdField struct {
		name string
		typ  model.AttributeType
		ok   bool
		off  *int
	}
	fields := []stdField{
		{model.AttrIntensity, model.AttributeUint16, true, &p.intensity},
		{model.AttrClassification, model.AttributeUint8, true, &p.classification},
		{model.AttrReturnNumber, model.AttributeUint8, true, &p.returnNumber},
		{model.AttrNumberOfReturns, model.AttributeUint8, true, &p.numberOfReturns},
		{"scan_direction_flag", model.AttributeBool, true, &p.scanDirectionFlag},
		{"edge_of_flight_line", model.AttributeBool, true, &p.edgeOfFlightLine},
		{"classification_flags", model.AttributeUint8, true, &p.classificationFlags},
		{"synthetic", model.AttributeBool, true, &p.synthetic},
		{"key_point", model.AttributeBool, true, &p.keyPoint},
		{"withheld", model.AttributeBool, true, &p.withheld},
		{"overlap", model.AttributeBool, true, &p.overlap},
		{"user_data", model.AttributeUint8, true, &p.userData},
		{"point_source_id", model.AttributeUint16, true, &p.pointSourceID},
		{"scan_angle", model.AttributeFloat64, true, &p.scanAngle},
		{"gps_time", model.AttributeFloat64, gps, &p.gpsTime},
		{"nir", model.AttributeUint16, nir, &p.nir},
		{"scanner_channel", model.AttributeUint8, extended, &p.scannerChannel},
		{"wave_packet_descriptor_index", model.AttributeUint8, wave, &p.wavePacketDescriptorIndex},
		{"waveform_data_offset", model.AttributeUint64, wave, &p.waveformDataOffset},
		{"waveform_packet_size", model.AttributeUint32, wave, &p.waveformPacketSize},
		{"return_point_waveform_location", model.AttributeFloat32, wave, &p.returnPointWaveformLocation},
		{"waveform_x_t", model.AttributeFloat32, wave, &p.waveformXT},
		{"waveform_y_t", model.AttributeFloat32, wave, &p.waveformYT},
		{"waveform_z_t", model.AttributeFloat32, wave, &p.waveformZT},
	}

	cursor := 0
	for i := range fields {
		fd := &fields[i]
		*fd.off = -1
		if !fd.ok {
			continue
		}
		if _, req := f.requested[fd.name]; !req {
			continue
		}
		size, _ := model.AttributeTypeSize(fd.typ)
		f.schema = append(f.schema, model.AttributeDescriptor{Name: fd.name, Type: fd.typ})
		*fd.off = cursor
		cursor += size
	}
	for i := range f.extraAttrs {
		ea := &f.extraAttrs[i]
		size, _ := model.AttributeTypeSize(ea.typ)
		f.schema = append(f.schema, model.AttributeDescriptor{Name: ea.outputName, Type: ea.typ})
		ea.blobOff = cursor
		ea.blobSize = size
		cursor += size
	}
	p.size = cursor
}

func b2u8(v bool) byte {
	if v {
		return 1
	}
	return 0
}

// encodeAttributesLocked encodes the requested attributes of the point in
// scanBuf into a packed AttributeValues buffer laid out per f.schema.
// Must be called with f.mu held.
func (f *GoLasReader) encodeAttributesLocked() model.AttributeValues {
	p := &f.plan
	if p.size == 0 {
		return nil
	}
	blob := f.arena.Alloc(p.size)
	if p.intensity >= 0 {
		binary.LittleEndian.PutUint16(blob[p.intensity:], f.scanBuf.Intensity)
	}
	if p.classification >= 0 {
		blob[p.classification] = f.scanBuf.Classification
	}
	if p.returnNumber >= 0 {
		blob[p.returnNumber] = f.scanBuf.ReturnNumber
	}
	if p.numberOfReturns >= 0 {
		blob[p.numberOfReturns] = f.scanBuf.NumberOfReturns
	}
	if p.scanDirectionFlag >= 0 {
		blob[p.scanDirectionFlag] = b2u8(f.scanBuf.ScanDirectionFlag)
	}
	if p.edgeOfFlightLine >= 0 {
		blob[p.edgeOfFlightLine] = b2u8(f.scanBuf.EdgeOfFlightLine)
	}
	if p.classificationFlags >= 0 {
		blob[p.classificationFlags] = f.scanBuf.ClassificationFlags
	}
	if p.synthetic >= 0 {
		blob[p.synthetic] = f.scanBuf.ClassificationFlags & 1
	}
	if p.keyPoint >= 0 {
		blob[p.keyPoint] = (f.scanBuf.ClassificationFlags >> 1) & 1
	}
	if p.withheld >= 0 {
		blob[p.withheld] = (f.scanBuf.ClassificationFlags >> 2) & 1
	}
	if p.overlap >= 0 {
		blob[p.overlap] = (f.scanBuf.ClassificationFlags >> 3) & 1
	}
	if p.userData >= 0 {
		blob[p.userData] = f.scanBuf.UserData
	}
	if p.pointSourceID >= 0 {
		binary.LittleEndian.PutUint16(blob[p.pointSourceID:], f.scanBuf.PointSourceID)
	}
	if p.scanAngle >= 0 {
		binary.LittleEndian.PutUint64(blob[p.scanAngle:], math.Float64bits(f.scanBuf.ScanAngleDegrees))
	}
	if p.gpsTime >= 0 {
		if v, ok := f.scanBuf.GPSTime(); ok {
			binary.LittleEndian.PutUint64(blob[p.gpsTime:], math.Float64bits(v))
		}
	}
	if p.nir >= 0 {
		if v, ok := f.scanBuf.NIR(); ok {
			binary.LittleEndian.PutUint16(blob[p.nir:], v)
		}
	}
	if p.scannerChannel >= 0 {
		if v, ok := f.scanBuf.ScannerChannel(); ok {
			blob[p.scannerChannel] = v
		}
	}
	if p.wavePacketDescriptorIndex >= 0 {
		if v, ok := f.scanBuf.WavePacketDescriptorIndex(); ok {
			blob[p.wavePacketDescriptorIndex] = v
		}
	}
	if p.waveformDataOffset >= 0 {
		if v, ok := f.scanBuf.WaveformDataOffset(); ok {
			binary.LittleEndian.PutUint64(blob[p.waveformDataOffset:], v)
		}
	}
	if p.waveformPacketSize >= 0 {
		if v, ok := f.scanBuf.WaveformPacketSize(); ok {
			binary.LittleEndian.PutUint32(blob[p.waveformPacketSize:], v)
		}
	}
	if p.returnPointWaveformLocation >= 0 {
		if v, ok := f.scanBuf.ReturnPointWaveformLocation(); ok {
			binary.LittleEndian.PutUint32(blob[p.returnPointWaveformLocation:], math.Float32bits(v))
		}
	}
	if p.waveformXT >= 0 || p.waveformYT >= 0 || p.waveformZT >= 0 {
		if x, y, z, ok := f.scanBuf.WaveDirection(); ok {
			if p.waveformXT >= 0 {
				binary.LittleEndian.PutUint32(blob[p.waveformXT:], math.Float32bits(x))
			}
			if p.waveformYT >= 0 {
				binary.LittleEndian.PutUint32(blob[p.waveformYT:], math.Float32bits(y))
			}
			if p.waveformZT >= 0 {
				binary.LittleEndian.PutUint32(blob[p.waveformZT:], math.Float32bits(z))
			}
		}
	}
	for i := range f.extraAttrs {
		ea := &f.extraAttrs[i]
		value, err := f.r.ExtraByte(&f.scanBuf, ea.descName)
		if err != nil {
			continue // leave zeros
		}
		if ea.scaled {
			raw, ok := extraByteAsFloat64(value)
			if !ok {
				continue
			}
			binary.LittleEndian.PutUint64(blob[ea.blobOff:], math.Float64bits(raw*ea.scale+ea.offset))
			continue
		}
		// golaz boxes the extra-byte scalar; decode it via the generic encoder.
		_ = model.EncodeAttributeValue(blob[ea.blobOff:ea.blobOff+ea.blobSize], ea.typ, value)
	}
	return blob
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
