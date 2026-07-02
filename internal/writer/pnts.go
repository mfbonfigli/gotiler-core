package writer

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/mfbonfigli/gotiler-core/internal/utils"
	"github.com/mfbonfigli/gotiler-core/tiler/encoding"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/plugin"
	"github.com/mfbonfigli/gotiler-core/tiler/tree"
	"github.com/mfbonfigli/gotiler-core/version"
)

// heuristic to preallocate buffers capacity
const maxPointsPerPntsTileHint = 100_000

// size of write buffer
const bufferSize = 256 * 1024

// totalBytesPerPoint = 12 (xyz) + 3 (rgb) + 2 (intensity) + 1 (classification) = 18
const totalBytesPerPoint = 18

// pntsBufPool provides reusable byte buffers for building pnts binary content.
var pntsBufPool = utils.NewSlicePool[byte](maxPointsPerPntsTileHint * totalBytesPerPoint)

// PntsEncoder writes a node data as Pnts file (3D Tiles 1.0 specs)
type PntsEncoder struct {
	filename string
}

func (e *PntsEncoder) TilesetVersion() version.TilesetVersion {
	return version.TilesetVersion_1_0
}

func (e *PntsEncoder) ContentFilename() string {
	return e.filename
}

func NewPntsEncoder(filename string, attrs model.Attributes) *PntsEncoder {
	return &PntsEncoder{filename: filename}
}

func (e *PntsEncoder) Write(node tree.Node, wp plugin.WriterProvider, prefix string) error {
	return e.writeGeneric(node, wp, prefix, encoding.AttributeColumns(node, encoding.PntsSupportsType))
}

func (e *PntsEncoder) generateFeatureTable(numPoints int) ([]byte, int) {
	featureTableStr := e.generateFeatureTableJsonContent(numPoints, 0)
	return []byte(featureTableStr), len(featureTableStr)
}

func (e *PntsEncoder) writePntsHeader(numPoints, featureTableLen, batchTableLen, batchBodyLen int, wr io.Writer) error {
	_, err := wr.Write([]byte("pnts")) // magic
	if err != nil {
		return err
	}
	err = utils.WriteIntAs4ByteNumber(1, wr) // version number
	if err != nil {
		return err
	}
	positionBytesLen := 4 * 3 * numPoints
	// Total file length: header + feature table (JSON + binary) + batch table (JSON + binary)
	err = utils.WriteIntAs4ByteNumber(28+featureTableLen+positionBytesLen+numPoints*3+batchTableLen+batchBodyLen, wr)
	if err != nil {
		return err
	}
	err = utils.WriteIntAs4ByteNumber(featureTableLen, wr)
	if err != nil {
		return err
	}
	err = utils.WriteIntAs4ByteNumber(positionBytesLen+numPoints*3, wr) // feature table binary: positions + colors
	if err != nil {
		return err
	}
	err = utils.WriteIntAs4ByteNumber(batchTableLen, wr)
	if err != nil {
		return err
	}
	err = utils.WriteIntAs4ByteNumber(batchBodyLen, wr)
	if err != nil {
		return err
	}
	return nil
}

func (e *PntsEncoder) writeTable(tableBytes []byte, wr io.Writer) error {
	_, err := wr.Write(tableBytes)
	return err
}

// generateFeatureTableJsonContent returns the feature table JSON padded to a 4-byte boundary.
func (e *PntsEncoder) generateFeatureTableJsonContent(pointNo int, spaceNo int) string {
	s := fmt.Sprintf(`{"POINTS_LENGTH":%d,"POSITION":{"byteOffset":0},"RGB":{"byteOffset":%d}}%s`,
		pointNo,
		pointNo*12,
		strings.Repeat(" ", spaceNo),
	)
	if pad := len(s) % 4; pad != 0 {
		return e.generateFeatureTableJsonContent(pointNo, 4-pad)
	}
	return s
}

func (e *PntsEncoder) writeGeneric(node tree.Node, wp plugin.WriterProvider, prefix string, columns []encoding.AttributeColumn) error {
	pts, err := node.Points()
	if err != nil {
		return err
	}
	defer pts.Close()

	n := pts.Len()
	featureTableBytes, featureTableLen := e.generateFeatureTable(n)

	// Batch body layout: one contiguous column per attribute, in column order.
	attrOffsets := make([]int, len(columns))
	batchBodyLen := 0
	for i, col := range columns {
		attrOffsets[i] = batchBodyLen
		batchBodyLen += n * col.Size
	}

	batchTableBytes, batchTableLen := e.generateGenericBatchTable(columns, attrOffsets)

	f, err := wp(prefix + e.filename)
	if err != nil {
		return err
	}
	wr := bufio.NewWriterSize(f, bufferSize)
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()

	if err := e.writePntsHeader(n, featureTableLen, batchTableLen, batchBodyLen, wr); err != nil {
		return err
	}
	if err := e.writeTable(featureTableBytes, wr); err != nil {
		return err
	}

	coordsEnd := n * 12
	colorsEnd := coordsEnd + n*3
	bufPtr := pntsBufPool.GetWithMinCapacity(colorsEnd + batchBodyLen)
	defer pntsBufPool.Put(bufPtr)
	buf := *bufPtr

	for i := 0; i < n; i++ {
		pt, err := pts.Next()
		if err != nil {
			return err
		}
		coordsOff := i * 12
		binary.LittleEndian.PutUint32(buf[coordsOff:], math.Float32bits(pt.X))
		binary.LittleEndian.PutUint32(buf[coordsOff+4:], math.Float32bits(pt.Y))
		binary.LittleEndian.PutUint32(buf[coordsOff+8:], math.Float32bits(pt.Z))
		colorsOff := coordsEnd + i*3
		buf[colorsOff] = pt.R
		buf[colorsOff+1] = pt.G
		buf[colorsOff+2] = pt.B

		// The batch table binary and the packed point values share the same
		// little-endian scalar encoding, so columns are filled with plain copies.
		for j, col := range columns {
			off := colorsEnd + attrOffsets[j] + i*col.Size
			if b := encoding.ColumnBytes(pt, col); b != nil {
				copy(buf[off:off+col.Size], b)
			} else {
				clear(buf[off : off+col.Size])
			}
		}
	}

	if _, err := wr.Write(buf[:coordsEnd]); err != nil {
		return err
	}
	if _, err := wr.Write(buf[coordsEnd:colorsEnd]); err != nil {
		return err
	}
	if err := e.writeTable(batchTableBytes, wr); err != nil {
		return err
	}
	if batchBodyLen > 0 {
		if _, err := wr.Write(buf[colorsEnd : colorsEnd+batchBodyLen]); err != nil {
			return err
		}
	}
	if err := wr.Flush(); err != nil {
		return err
	}
	closed = true
	return f.Close()
}

func (e *PntsEncoder) generateGenericBatchTable(columns []encoding.AttributeColumn, offsets []int) ([]byte, int) {
	if len(columns) == 0 {
		return nil, 0
	}
	s := e.generateGenericBatchTableJsonContent(columns, offsets, 0)
	return []byte(s), len(s)
}

func (e *PntsEncoder) generateGenericBatchTableJsonContent(columns []encoding.AttributeColumn, offsets []int, spaceNo int) string {
	entries := make([]string, 0, len(columns))
	for i, col := range columns {
		componentType, _ := encoding.PntsComponentType(col.Summary.Type)
		entries = append(entries, fmt.Sprintf(`"%s":{"byteOffset":%d,"componentType":"%s","type":"SCALAR"}`,
			encoding.AttributeOutputName(col.Summary.Name),
			offsets[i],
			componentType,
		))
	}
	s := fmt.Sprintf("{%s}%s", strings.Join(entries, ",\n\t"), strings.Repeat(" ", spaceNo))
	if pad := len(s) % 4; pad != 0 {
		return e.generateGenericBatchTableJsonContent(columns, offsets, 4-pad)
	}
	return s
}
