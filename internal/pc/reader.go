package pc

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/plugin"
	"github.com/mfbonfigli/gotiler-core/tiler/pointcloud"
)

// attrRemapEntry copies one attribute value from a sub-reader's packed layout
// to the combined layout.
type attrRemapEntry struct {
	srcOff int
	dstOff int
	size   int
}

// CombinedPointCloudReader enables reading a a list of files as if they were a single one
// the files MUST have the same properties (SRID, etc)
type CombinedPointCloudReader struct {
	currentReader atomic.Int32
	readers       []pointcloud.Reader
	numPts        int
	crs           string
	schema        []model.AttributeDescriptor
	schemaSize    int
	// remaps[i] translates reader i's packed values to the combined schema
	// layout; nil when reader i's schema already matches (the common case).
	remaps  [][]attrRemapEntry
	remapMu sync.Mutex
	arena   model.AttributeValuesArena
}

// NewCombinedPointCloudReader creates a new file reader for the files passed as input. If crs is the empty string, the
// reader will autodetect the CRS from the input files, however an error is returned if the CRS is not consistent across
// all of them or if it's not found in the files.
// attrs lists the optional per-point attributes to emit; nil means none.
func NewCombinedPointCloudReader(files []string, crs string, eightBitColor bool, attrs model.Attributes) (*CombinedPointCloudReader, error) {
	r := &CombinedPointCloudReader{}
	crsProvided := crs != ""
	readerOpts := plugin.ReaderOptions{EightBitColor: eightBitColor, RequestedAttributes: attrs}
	for _, f := range files {
		factory, ok := plugin.PointCloudReaderFactoryFor(f)
		if !ok {
			continue
		}

		fr, err := factory(f, crs, readerOpts)
		if err != nil {
			return nil, err
		}

		r.numPts += fr.NumberOfPoints()
		r.readers = append(r.readers, fr)
		if !crsProvided {
			if crs != "" && crs != fr.GetCRS() {
				return nil, fmt.Errorf("no CRS was provided and inconsistent CRS were detected:\n%s\n\n and\n\n%s", crs, fr.GetCRS())
			}
			crs = fr.GetCRS()
		}
	}
	if len(r.readers) == 0 {
		return nil, fmt.Errorf("no supported point cloud files found; registered extensions: %v", plugin.SupportedPointCloudExtensions())
	}
	r.crs = crs
	if err := r.buildAttributeRemaps(); err != nil {
		return nil, err
	}
	return r, nil
}

// buildAttributeRemaps adopts the first reader's schema as the combined schema
// and precomputes, for every other reader, how its packed values map onto it.
// Attributes a file does not carry are left as zero values for its points; a
// same-name attribute with a different type across files is an error.
func (m *CombinedPointCloudReader) buildAttributeRemaps() error {
	m.schema = m.readers[0].AttributeSchema()
	combined, size, err := model.AttributeSchemaLayout(m.schema)
	if err != nil {
		return err
	}
	m.schemaSize = size
	m.remaps = make([][]attrRemapEntry, len(m.readers))
	for i, reader := range m.readers {
		sub := reader.AttributeSchema()
		if schemasEqual(m.schema, sub) {
			continue // passthrough
		}
		subLayout, _, err := model.AttributeSchemaLayout(sub)
		if err != nil {
			return err
		}
		byName := make(map[string]model.AttributeLayoutEntry, len(subLayout))
		for _, e := range subLayout {
			byName[e.Name] = e
		}
		remap := []attrRemapEntry{} // non-nil marks "needs remapping" even when empty
		for _, dst := range combined {
			src, ok := byName[dst.Name]
			if !ok {
				continue // attribute absent from this file: stays zero
			}
			if src.Type != dst.Type {
				return fmt.Errorf("attribute %q has type %q in one input file and %q in another", dst.Name, src.Type, dst.Type)
			}
			remap = append(remap, attrRemapEntry{srcOff: src.Offset, dstOff: dst.Offset, size: dst.Size})
		}
		m.remaps[i] = remap
	}
	return nil
}

func schemasEqual(a, b []model.AttributeDescriptor) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AttributeSchema implements pointcloud.Reader. The combined schema is the
// first file's schema; points from files lacking one of its attributes carry
// zero values for it.
func (m *CombinedPointCloudReader) AttributeSchema() []model.AttributeDescriptor {
	return m.schema
}

func (m *CombinedPointCloudReader) NumberOfPoints() int {
	return m.numPts
}

func (m *CombinedPointCloudReader) GetCRS() string {
	return m.crs
}

func (m *CombinedPointCloudReader) GetNext() (geom.Point64, error) {
	for {
		currReader := int(m.currentReader.Load())
		if currReader >= len(m.readers) {
			return geom.Point64{}, io.EOF
		}
		pt, err := m.readers[currReader].GetNext()
		if err != nil {
			// try to move on to the next reader
			m.currentReader.CompareAndSwap(int32(currReader), int32(currReader)+1)
			continue
		}
		if remap := m.remaps[currReader]; remap != nil {
			// This file's schema differs from the combined one: rewrite the
			// packed values into the combined layout (rare path).
			m.remapMu.Lock()
			blob := m.arena.Alloc(m.schemaSize)
			m.remapMu.Unlock()
			for _, e := range remap {
				copy(blob[e.dstOff:e.dstOff+e.size], pt.Attributes[e.srcOff:e.srcOff+e.size])
			}
			pt.Attributes = blob
		}
		return pt, nil
	}
}

func (m *CombinedPointCloudReader) Reset() error {
	m.currentReader.Store(0)
	for _, r := range m.readers {
		if err := r.Reset(); err != nil {
			return err
		}
	}
	return nil
}

func (m *CombinedPointCloudReader) Close() {
	for _, r := range m.readers {
		r.Close()
	}
}
