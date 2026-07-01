package writer

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"

	"github.com/mfbonfigli/gotiler-core/internal/utils"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/plugin"
	"github.com/mfbonfigli/gotiler-core/tiler/tree"
	"github.com/mfbonfigli/gotiler-core/version"
	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

// ---------------------------------------------------------------------------
// EXT_structural_metadata dynamic JSON builder
// ---------------------------------------------------------------------------

// attrSchemaDef maps a model attribute name to its EXT_structural_metadata
// schema definition. Add an entry here when introducing a new attribute type.
type attrSchemaDef struct {
	modelName     string // e.g. model.AttrIntensity
	schemaName    string // property key in the schema, e.g. "INTENSITY"
	primitiveName string // glTF primitive attribute name, e.g. "_INTENSITY"
	description   string
	componentType string // EXT_structural_metadata componentType, e.g. "UINT16"
}

// attrSchemaRegistry lists all optional attributes in declaration order.
var attrSchemaRegistry = []attrSchemaDef{
	{model.AttrIntensity, "INTENSITY", "_INTENSITY", "Laser intensity", "UINT16"},
	{model.AttrClassification, "CLASSIFICATION", "_CLASSIFICATION", "Point classification", "UINT16"},
	{model.AttrReturnNumber, "RETURN_NUMBER", "_RETURN_NUMBER", "Return number", "UINT8"},
	{model.AttrNumberOfReturns, "NUMBER_OF_RETURNS", "_NUMBER_OF_RETURNS", "Number of returns", "UINT8"},
}

// orderedJSONMap marshals as a JSON object with keys in insertion order.
type orderedJSONMap[V any] struct {
	keys []string
	data map[string]V
}

func newOrderedJSONMap[V any]() orderedJSONMap[V] {
	return orderedJSONMap[V]{data: make(map[string]V)}
}

func (m *orderedJSONMap[V]) add(key string, val V) {
	m.keys = append(m.keys, key)
	m.data[key] = val
}

func (m orderedJSONMap[V]) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(m.data[k])
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// JSON structs for EXT_structural_metadata.
type extMetaPropDef struct {
	Description   string `json:"description"`
	Type          string `json:"type"`
	ComponentType string `json:"componentType"`
	Required      bool   `json:"required"`
}

type extMetaClass struct {
	Name        string                         `json:"name"`
	Description string                         `json:"description"`
	Properties  orderedJSONMap[extMetaPropDef] `json:"properties"`
}

type extMetaSchema struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Version     string                  `json:"version"`
	Classes     map[string]extMetaClass `json:"classes"`
}

type extMetaPropAttrEntry struct {
	Attribute string `json:"attribute"`
}

type extMetaPropAttr struct {
	Class      string                               `json:"class"`
	Properties orderedJSONMap[extMetaPropAttrEntry] `json:"properties"`
}

type extMetadata struct {
	Schema             extMetaSchema     `json:"schema"`
	PropertyAttributes []extMetaPropAttr `json:"propertyAttributes"`
}

// buildGltfExtJson returns the serialised EXT_structural_metadata JSON for the
// given attribute set, or an empty string when no optional attributes are enabled.
func buildGltfExtJson(attrs model.Attributes) string {
	props := newOrderedJSONMap[extMetaPropDef]()
	propAttrs := newOrderedJSONMap[extMetaPropAttrEntry]()

	for _, def := range attrSchemaRegistry {
		if !attrs.Has(def.modelName) {
			continue
		}
		props.add(def.schemaName, extMetaPropDef{
			Description:   def.description,
			Type:          "SCALAR",
			ComponentType: def.componentType,
			Required:      true,
		})
		propAttrs.add(def.schemaName, extMetaPropAttrEntry{Attribute: def.primitiveName})
	}

	if len(props.keys) == 0 {
		return ""
	}

	meta := extMetadata{
		Schema: extMetaSchema{
			ID:          "pts_schema",
			Name:        "pts_schema",
			Description: "point cloud point attribute schema",
			Version:     "1.0.0",
			Classes: map[string]extMetaClass{
				"point": {
					Name:        "point",
					Description: "Properties of point cloud points",
					Properties:  props,
				},
			},
		},
		PropertyAttributes: []extMetaPropAttr{
			{Class: "point", Properties: propAttrs},
		},
	}

	b, _ := json.Marshal(meta)
	return string(b)
}

// ---------------------------------------------------------------------------
// GltfEncoder
// ---------------------------------------------------------------------------

// Default pool capacity for glTF buffers — sized large enough to cover
// a typical tile while small enough not to waste memory on tiny tiles.
const defaultGltfBufferCap = 200000

// srgbToLinear converts an sRGB uint8 channel value to a linear-light uint8 value
// using the standard gamma 2.2 approximation. Computed once at init, used as a
// lookup table in the per-point loop to avoid calling math.Pow per point.
var srgbToLinear [256]uint8

func init() {
	for i := range 256 {
		srgbToLinear[i] = uint8(math.Pow(float64(i)/255.0, 2.2) * 255.0)
	}
}

// appendUint16VertexAccessor appends a SCALAR UNSIGNED_SHORT vertex attribute to the
// document. Each element is stored with stride=4 (uint16 value + 2 zero padding bytes)
// so that every element lands on a 4-byte boundary, satisfying the glTF requirement
// that all vertex attribute data must be aligned to 4-byte boundaries.
func appendUint16VertexAccessor(doc *gltf.Document, data []uint16) int {
	buf := doc.Buffers[len(doc.Buffers)-1]

	// Align buffer start to 4-byte boundary.
	for len(buf.Data)%4 != 0 {
		buf.Data = append(buf.Data, 0)
		buf.ByteLength++
	}
	byteOffset := len(buf.Data)

	// Write each uint16 with 2 zero padding bytes (stride=4).
	byteLen := len(data) * 4
	raw := make([]byte, byteLen)
	for i, v := range data {
		binary.LittleEndian.PutUint16(raw[i*4:], v)
	}
	buf.Data = append(buf.Data, raw...)
	buf.ByteLength += byteLen

	bvIdx := len(doc.BufferViews)
	doc.BufferViews = append(doc.BufferViews, &gltf.BufferView{
		Buffer:     len(doc.Buffers) - 1,
		ByteOffset: byteOffset,
		ByteLength: byteLen,
		ByteStride: 4,
		Target:     gltf.TargetArrayBuffer,
	})

	accIdx := len(doc.Accessors)
	doc.Accessors = append(doc.Accessors, &gltf.Accessor{
		BufferView:    gltf.Index(bvIdx),
		ByteOffset:    0,
		ComponentType: gltf.ComponentUshort,
		Type:          gltf.AccessorScalar,
		Count:         len(data),
	})
	return accIdx
}

// appendUint8VertexAccessor appends a SCALAR UNSIGNED_BYTE vertex attribute to the
// document. Each element is stored with stride=4 (uint8 value + 3 zero padding bytes)
// so that every element lands on a 4-byte boundary, satisfying the glTF alignment requirement.
func appendUint8VertexAccessor(doc *gltf.Document, data []uint8) int {
	buf := doc.Buffers[len(doc.Buffers)-1]

	// Align buffer start to 4-byte boundary.
	for len(buf.Data)%4 != 0 {
		buf.Data = append(buf.Data, 0)
		buf.ByteLength++
	}
	byteOffset := len(buf.Data)

	// Write each uint8 with 3 zero padding bytes (stride=4).
	byteLen := len(data) * 4
	raw := make([]byte, byteLen)
	for i, v := range data {
		raw[i*4] = v
	}
	buf.Data = append(buf.Data, raw...)
	buf.ByteLength += byteLen

	bvIdx := len(doc.BufferViews)
	doc.BufferViews = append(doc.BufferViews, &gltf.BufferView{
		Buffer:     len(doc.Buffers) - 1,
		ByteOffset: byteOffset,
		ByteLength: byteLen,
		ByteStride: 4,
		Target:     gltf.TargetArrayBuffer,
	})

	accIdx := len(doc.Accessors)
	doc.Accessors = append(doc.Accessors, &gltf.Accessor{
		BufferView:    gltf.Index(bvIdx),
		ByteOffset:    0,
		ComponentType: gltf.ComponentUbyte,
		Type:          gltf.AccessorScalar,
		Count:         len(data),
	})
	return accIdx
}

// GltfEncoder writes a node data as Gltf/Glb binary file (3D Tiles 1.1 specs).
// Optional attributes are encoded using the EXT_structural_metadata GLTF extension.
type GltfEncoder struct {
	filename            string
	extJsonStr          string // cached EXT_structural_metadata JSON, constant for the encoder's lifetime
	coordPool           *utils.SlicePool[[3]float32]
	colorPool           *utils.SlicePool[[3]uint8]
	intensityPool       *utils.SlicePool[uint16]
	classPool           *utils.SlicePool[uint16]
	returnNumPool       *utils.SlicePool[uint8]
	numReturnsPool      *utils.SlicePool[uint8]
	writeIntensity      bool
	writeClassification bool
	writeReturnNumber   bool
	writeNumReturns     bool
}

func (e *GltfEncoder) TilesetVersion() version.TilesetVersion {
	return version.TilesetVersion_1_1
}

func (e *GltfEncoder) ContentFilename() string {
	return e.filename
}

func NewGltfEncoder(filename string, attrs model.Attributes) *GltfEncoder {
	return &GltfEncoder{
		filename:            filename,
		extJsonStr:          buildGltfExtJson(attrs),
		writeIntensity:      attrs.Has(model.AttrIntensity),
		writeClassification: attrs.Has(model.AttrClassification),
		writeReturnNumber:   attrs.Has(model.AttrReturnNumber),
		writeNumReturns:     attrs.Has(model.AttrNumberOfReturns),
		coordPool:           utils.NewSlicePool[[3]float32](defaultGltfBufferCap),
		colorPool:           utils.NewSlicePool[[3]uint8](defaultGltfBufferCap),
		intensityPool:       utils.NewSlicePool[uint16](defaultGltfBufferCap),
		classPool:           utils.NewSlicePool[uint16](defaultGltfBufferCap),
		returnNumPool:       utils.NewSlicePool[uint8](defaultGltfBufferCap),
		numReturnsPool:      utils.NewSlicePool[uint8](defaultGltfBufferCap),
	}
}

func (e *GltfEncoder) Write(node tree.Node, wp plugin.WriterProvider, prefix string) error {
	pts, err := node.Points()
	if err != nil {
		return err
	}
	defer pts.Close()

	doc := gltf.NewDocument()
	doc.Asset = gltf.Asset{
		Generator: "gotiler",
		Version:   "2.0",
	}

	n := pts.Len()

	coordsPtr := e.coordPool.GetWithMinCapacity(n)
	coords := *coordsPtr
	defer e.coordPool.Put(coordsPtr)

	colorsPtr := e.colorPool.GetWithMinCapacity(n)
	colors := *colorsPtr
	defer e.colorPool.Put(colorsPtr)

	var intensities []uint16
	if e.writeIntensity {
		intensitiesPtr := e.intensityPool.GetWithMinCapacity(n)
		intensities = *intensitiesPtr
		defer e.intensityPool.Put(intensitiesPtr)
	}

	var classifications []uint16
	if e.writeClassification {
		classificationsPtr := e.classPool.GetWithMinCapacity(n)
		classifications = *classificationsPtr
		defer e.classPool.Put(classificationsPtr)
	}

	var returnNums []uint8
	if e.writeReturnNumber {
		returnNumsPtr := e.returnNumPool.GetWithMinCapacity(n)
		returnNums = *returnNumsPtr
		defer e.returnNumPool.Put(returnNumsPtr)
	}

	var numReturns []uint8
	if e.writeNumReturns {
		numReturnsPtr := e.numReturnsPool.GetWithMinCapacity(n)
		numReturns = *numReturnsPtr
		defer e.numReturnsPool.Put(numReturnsPtr)
	}

	for i := 0; i < n; i++ {
		pt, err := pts.Next()
		if err != nil {
			return err
		}
		coords[i][0] = pt.X
		coords[i][1] = pt.Y
		coords[i][2] = pt.Z

		// LAS colors are typically in the sRGB space, however GLTF specs require
		// COLOR_0 for meshes to be in the linear RGB space, hence we need to convert
		// the colors back to linear RGB
		colors[i][0] = srgbToLinear[pt.R]
		colors[i][1] = srgbToLinear[pt.G]
		colors[i][2] = srgbToLinear[pt.B]
		if e.writeIntensity {
			intensities[i] = pt.Intensity
		}
		if e.writeClassification {
			classifications[i] = uint16(pt.Classification)
		}
		if e.writeReturnNumber {
			returnNums[i] = pt.ReturnNumber
		}
		if e.writeNumReturns {
			numReturns[i] = pt.NumberOfReturns
		}
	}

	// Write position + color interleaved. Custom attributes are written as separate
	// non-interleaved accessors so each one starts at a 4-byte-aligned buffer offset,
	// satisfying the glTF MESH_PRIMITIVE_ACCESSOR_UNALIGNED requirement.
	gltfAttrs, err := modeler.WritePrimitiveAttributes(doc,
		modeler.PrimitiveAttribute{Name: gltf.POSITION, Data: coords[:n]},
		modeler.PrimitiveAttribute{Name: gltf.COLOR_0, Data: colors[:n]},
	)
	if err != nil {
		return err
	}
	if e.writeIntensity {
		gltfAttrs["_INTENSITY"] = appendUint16VertexAccessor(doc, intensities[:n])
	}
	if e.writeClassification {
		gltfAttrs["_CLASSIFICATION"] = appendUint16VertexAccessor(doc, classifications[:n])
	}
	if e.writeReturnNumber {
		gltfAttrs["_RETURN_NUMBER"] = appendUint8VertexAccessor(doc, returnNums[:n])
	}
	if e.writeNumReturns {
		gltfAttrs["_NUMBER_OF_RETURNS"] = appendUint8VertexAccessor(doc, numReturns[:n])
	}

	var primExts gltf.Extensions
	if e.extJsonStr != "" {
		primExts = gltf.Extensions{
			"EXT_structural_metadata": json.RawMessage(`{"propertyAttributes": [0]}`),
		}
	}

	// When featureId.attribute and featureId.texture are both undefined, the feature ID
	// for each vertex is the vertex index. featureCount must match the vertex count.
	doc.Meshes = []*gltf.Mesh{{
		Name: "PointCloud",
		Primitives: []*gltf.Primitive{{
			Mode:       gltf.PrimitivePoints,
			Attributes: gltfAttrs,
			Extensions: primExts,
		}},
	}}
	// glTF is Y-up; Cesium is Z-up — rotation required.
	doc.Nodes = []*gltf.Node{
		{
			Name:   "PointCloud",
			Mesh:   gltf.Index(0),
			Matrix: [16]float64{1, 0, 0, 0, 0, 0, -1, 0, 0, 1, 0, 0, 0, 0, 0, 1},
		},
	}
	doc.Scenes[0].Nodes = append(doc.Scenes[0].Nodes, 0)

	if e.extJsonStr != "" {
		doc.Extensions = gltf.Extensions{
			"EXT_structural_metadata": json.RawMessage(e.extJsonStr),
		}
		doc.ExtensionsUsed = []string{"EXT_structural_metadata"}
	}

	w, err := wp(prefix + e.filename)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = w.Close()
		}
	}()

	enc := gltf.NewEncoder(w)
	enc.AsBinary = true
	if err := enc.Encode(doc); err != nil {
		return err
	}
	closed = true
	return w.Close()
}
