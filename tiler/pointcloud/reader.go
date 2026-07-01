package pointcloud

import "github.com/mfbonfigli/gotiler-core/tiler/geom"

// Reader reads point-cloud points and source CRS metadata.
type Reader interface {
	NumberOfPoints() int
	GetNext() (geom.Point64, error)
	GetCRS() string
	Reset() error
	Close()
}
