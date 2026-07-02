package pc

import (
	"fmt"
	"io"
	"sync/atomic"

	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/plugin"
	"github.com/mfbonfigli/gotiler-core/tiler/pointcloud"
)

// CombinedPointCloudReader enables reading a a list of files as if they were a single one
// the files MUST have the same properties (SRID, etc)
type CombinedPointCloudReader struct {
	currentReader atomic.Int32
	readers       []pointcloud.Reader
	numPts        int
	crs           string
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
	return r, nil
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
