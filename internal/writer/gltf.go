package writer

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	"github.com/mfbonfigli/gotiler-core/internal/utils"
	"github.com/mfbonfigli/gotiler-core/tiler/encoding"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/plugin"
	"github.com/mfbonfigli/gotiler-core/tiler/tree"
	"github.com/mfbonfigli/gotiler-core/version"
	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

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

// gltfAttrByteStride is the stride used for generic vertex attribute data:
// every element starts on a 4-byte boundary as required by glTF, with zero
// padding after values narrower than 4 bytes.
const gltfAttrByteStride = 4

// appendGenericVertexAccessor appends a SCALAR vertex attribute to the document.
// raw holds count little-endian values already laid out with gltfAttrByteStride.
func appendGenericVertexAccessor(doc *gltf.Document, col encoding.AttributeColumn, raw []byte, count int) (int, error) {
	var componentType gltf.ComponentType
	switch encoding.GltfEffectiveType(col.Summary.Type) {
	case model.AttributeInt8:
		componentType = gltf.ComponentByte
	case model.AttributeUint8, model.AttributeBool:
		componentType = gltf.ComponentUbyte
	case model.AttributeInt16:
		componentType = gltf.ComponentShort
	case model.AttributeUint16:
		componentType = gltf.ComponentUshort
	case model.AttributeFloat32:
		// Covers float64 source attributes too: they are downcast to float32
		// when the column data is filled (glTF has no float64 component type).
		componentType = gltf.ComponentFloat
	default:
		return 0, fmt.Errorf("attribute %q type %q cannot be represented as a glTF vertex attribute", col.Summary.Name, col.Summary.Type)
	}

	buf := doc.Buffers[len(doc.Buffers)-1]
	for len(buf.Data)%4 != 0 {
		buf.Data = append(buf.Data, 0)
		buf.ByteLength++
	}
	byteOffset := len(buf.Data)

	buf.Data = append(buf.Data, raw...)
	buf.ByteLength += len(raw)

	bvIdx := len(doc.BufferViews)
	doc.BufferViews = append(doc.BufferViews, &gltf.BufferView{
		Buffer:     len(doc.Buffers) - 1,
		ByteOffset: byteOffset,
		ByteLength: len(raw),
		ByteStride: gltfAttrByteStride,
		Target:     gltf.TargetArrayBuffer,
	})

	accIdx := len(doc.Accessors)
	doc.Accessors = append(doc.Accessors, &gltf.Accessor{
		BufferView:    gltf.Index(bvIdx),
		ByteOffset:    0,
		ComponentType: componentType,
		Type:          gltf.AccessorScalar,
		Count:         count,
	})
	return accIdx, nil
}

// GltfEncoder writes a node data as Gltf/Glb binary file (3D Tiles 1.1 specs).
// Optional attributes are encoded using the EXT_structural_metadata GLTF extension.
type GltfEncoder struct {
	filename  string
	coordPool *utils.SlicePool[[3]float32]
	colorPool *utils.SlicePool[[3]uint8]
}

func (e *GltfEncoder) TilesetVersion() version.TilesetVersion {
	return version.TilesetVersion_1_1
}

func (e *GltfEncoder) ContentFilename() string {
	return e.filename
}

func NewGltfEncoder(filename string, attrs model.Attributes) *GltfEncoder {
	return &GltfEncoder{
		filename:  filename,
		coordPool: utils.NewSlicePool[[3]float32](defaultGltfBufferCap),
		colorPool: utils.NewSlicePool[[3]uint8](defaultGltfBufferCap),
	}
}

func (e *GltfEncoder) Write(node tree.Node, wp plugin.WriterProvider, prefix string) error {
	columns := encoding.AttributeColumns(node, encoding.GltfVertexSupportsType)
	extJsonStr, err := encoding.BuildGltfMetadataJSON(columns)
	if err != nil {
		return err
	}

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

	// One stride-4 packed column per attribute; values narrower than 4 bytes
	// keep their zero padding from the fresh allocation.
	columnData := make([][]byte, len(columns))
	for i := range columns {
		columnData[i] = make([]byte, n*gltfAttrByteStride)
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
		for j, col := range columns {
			b := encoding.ColumnBytes(pt, col)
			if b == nil {
				continue
			}
			if col.Summary.Type == model.AttributeFloat64 {
				// glTF has no float64 accessor component type, so float64
				// attributes are downcast to float32 here (lossy: ~7
				// significant digits) rather than dropped from the output.
				v := math.Float64frombits(binary.LittleEndian.Uint64(b))
				binary.LittleEndian.PutUint32(columnData[j][i*gltfAttrByteStride:], math.Float32bits(float32(v)))
				continue
			}
			copy(columnData[j][i*gltfAttrByteStride:], b)
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
	for i, col := range columns {
		accIdx, err := appendGenericVertexAccessor(doc, col, columnData[i], n)
		if err != nil {
			return err
		}
		gltfAttrs[encoding.AttributePrimitiveName(col.Summary.Name)] = accIdx
	}

	var primExts gltf.Extensions
	if extJsonStr != "" {
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

	if extJsonStr != "" {
		doc.Extensions = gltf.Extensions{
			"EXT_structural_metadata": json.RawMessage(extJsonStr),
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
