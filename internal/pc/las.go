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
		return NewGoLasReader(filename, crs, opts.EightBitColor)
	}
	plugin.RegisterPointCloudReader(".las", factory)
	plugin.RegisterPointCloudReader(".laz", factory)
}

// GoLasReader wraps a golaz.Reader implementing the specific interface LasReader required by gotiler.
// golaz.Reader is not goroutine-safe, so all reads are serialised with mu while keeping a reusable
// scan buffer to avoid per-point allocations.
type GoLasReader struct {
	r             *golaz.Reader
	eightBitColor bool
	crs           string
	mu            sync.Mutex
	scanBuf       golaz.Point
}

// NewGoLasReader returns a GoLasReader instance. If crs is empty the system will attempt to autodetect
// the CRS from the LAS metadata and return an error in case of issues.
func NewGoLasReader(fileName string, crs string, eightBitColor bool) (*GoLasReader, error) {
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
	return &GoLasReader{
		r:             r,
		eightBitColor: eightBitColor,
		crs:           crs,
	}, nil
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
	intensity := f.scanBuf.Intensity
	classification := f.scanBuf.Classification
	returnNumber := f.scanBuf.ReturnNumber
	numberOfReturns := f.scanBuf.NumberOfReturns
	red, green, blue, _ := f.scanBuf.RGB()
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
		R:               uint8(red / corr),
		G:               uint8(green / corr),
		B:               uint8(blue / corr),
		Intensity:       intensity,
		Classification:  classification,
		ReturnNumber:    returnNumber,
		NumberOfReturns: numberOfReturns,
	}, nil
}
