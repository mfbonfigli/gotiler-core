package writer

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/mfbonfigli/gotiler-core/internal/utils"
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
	attrs    model.Attributes
}

func (e *PntsEncoder) TilesetVersion() version.TilesetVersion {
	return version.TilesetVersion_1_0
}

func (e *PntsEncoder) ContentFilename() string {
	return e.filename
}

func NewPntsEncoder(filename string, attrs model.Attributes) *PntsEncoder {
	return &PntsEncoder{filename: filename, attrs: attrs}
}

func (e *PntsEncoder) Write(node tree.Node, wp plugin.WriterProvider, prefix string) error {
	pts, err := node.Points()
	if err != nil {
		return err
	}
	defer pts.Close()

	n := pts.Len()

	// Feature table (positions + colors — always present)
	featureTableBytes, featureTableLen := e.generateFeatureTable(n)

	// Batch body layout: [intensity n×2] [classification n×1] [return_number n×1] [number_of_returns n×1]
	intensitySize := 0
	if e.attrs.Has(model.AttrIntensity) {
		intensitySize = n * 2
	}
	classOffset := intensitySize
	classSize := 0
	if e.attrs.Has(model.AttrClassification) {
		classSize = n
	}
	returnNumOffset := classOffset + classSize
	returnNumSize := 0
	if e.attrs.Has(model.AttrReturnNumber) {
		returnNumSize = n
	}
	numReturnsOffset := returnNumOffset + returnNumSize
	numReturnsSize := 0
	if e.attrs.Has(model.AttrNumberOfReturns) {
		numReturnsSize = n
	}
	batchBodyLen := intensitySize + classSize + returnNumSize + numReturnsSize

	// Batch table JSON (empty if no attributes selected)
	batchTableBytes, batchTableLen := e.generateBatchTable(n, intensitySize, classSize)

	// Write binary content to file
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

	err = e.writePntsHeader(n, featureTableLen, batchTableLen, batchBodyLen, wr)
	if err != nil {
		return err
	}
	err = e.writeTable(featureTableBytes, wr)
	if err != nil {
		return err
	}

	coordsEnd := n * 12
	colorsEnd := coordsEnd + n*3

	bufPtr := pntsBufPool.GetWithMinCapacity(colorsEnd + batchBodyLen)
	defer pntsBufPool.Put(bufPtr)
	buf := *bufPtr

	writeIntensity := e.attrs.Has(model.AttrIntensity)
	writeClassification := e.attrs.Has(model.AttrClassification)
	writeReturnNumber := e.attrs.Has(model.AttrReturnNumber)
	writeNumReturns := e.attrs.Has(model.AttrNumberOfReturns)
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
		if writeIntensity {
			binary.LittleEndian.PutUint16(buf[colorsEnd+i*2:], pt.Intensity)
		}
		if writeClassification {
			buf[colorsEnd+classOffset+i] = pt.Classification
		}
		if writeReturnNumber {
			buf[colorsEnd+returnNumOffset+i] = pt.ReturnNumber
		}
		if writeNumReturns {
			buf[colorsEnd+numReturnsOffset+i] = pt.NumberOfReturns
		}
	}

	// Write feature body: positions + colors
	if _, err := wr.Write(buf[:coordsEnd]); err != nil {
		return err
	}
	if _, err := wr.Write(buf[coordsEnd:colorsEnd]); err != nil {
		return err
	}

	// Write batch table JSON (between feature body and batch body per pnts spec)
	err = e.writeTable(batchTableBytes, wr)
	if err != nil {
		return err
	}

	// Write batch body (only the enabled attribute bytes)
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

func (e *PntsEncoder) generateFeatureTable(numPoints int) ([]byte, int) {
	featureTableStr := e.generateFeatureTableJsonContent(numPoints, 0)
	return []byte(featureTableStr), len(featureTableStr)
}

// generateBatchTable returns the batch table JSON and its length.
// intensitySize and classSize are the byte sizes of those sections (0 when disabled).
func (e *PntsEncoder) generateBatchTable(numPoints, intensitySize, classSize int) ([]byte, int) {
	if !e.attrs.Has(model.AttrIntensity) && !e.attrs.Has(model.AttrClassification) &&
		!e.attrs.Has(model.AttrReturnNumber) && !e.attrs.Has(model.AttrNumberOfReturns) {
		return nil, 0
	}
	s := e.generateBatchTableJsonContent(numPoints, intensitySize, classSize, 0)
	return []byte(s), len(s)
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
	err = utils.WriteIntAs4ByteNumber(28+featureTableLen+positionBytesLen+numPoints*3, wr)
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

// generateBatchTableJsonContent returns the batch table JSON padded to a 4-byte boundary.
// intensitySize and classSize are the byte sizes of those sections (used to derive offsets).
func (e *PntsEncoder) generateBatchTableJsonContent(n, intensitySize, classSize, spaceNo int) string {
	returnNumOffset := intensitySize + classSize
	numReturnsOffset := returnNumOffset + n // only non-zero when return_number is enabled
	if !e.attrs.Has(model.AttrReturnNumber) {
		numReturnsOffset = returnNumOffset
	}
	var entries []string
	if e.attrs.Has(model.AttrIntensity) {
		entries = append(entries, `"INTENSITY":{"byteOffset":0,"componentType":"UNSIGNED_SHORT","type":"SCALAR"}`)
	}
	if e.attrs.Has(model.AttrClassification) {
		entries = append(entries, fmt.Sprintf(`"CLASSIFICATION":{"byteOffset":%d,"componentType":"UNSIGNED_BYTE","type":"SCALAR"}`, intensitySize))
	}
	if e.attrs.Has(model.AttrReturnNumber) {
		entries = append(entries, fmt.Sprintf(`"RETURN_NUMBER":{"byteOffset":%d,"componentType":"UNSIGNED_BYTE","type":"SCALAR"}`, returnNumOffset))
	}
	if e.attrs.Has(model.AttrNumberOfReturns) {
		entries = append(entries, fmt.Sprintf(`"NUMBER_OF_RETURNS":{"byteOffset":%d,"componentType":"UNSIGNED_BYTE","type":"SCALAR"}`, numReturnsOffset))
	}
	s := fmt.Sprintf("{%s}%s", strings.Join(entries, ",\n\t"), strings.Repeat(" ", spaceNo))
	if pad := len(s) % 4; pad != 0 {
		return e.generateBatchTableJsonContent(n, intensitySize, classSize, 4-pad)
	}
	return s
}
