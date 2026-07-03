package pointcloud

import (
	"github.com/mfbonfigli/gotiler-core/tiler/geom"
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

// Reader reads point-cloud points and source CRS metadata.
type Reader interface {
	NumberOfPoints() int
	GetNext() (geom.Point64, error)
	GetCRS() string
	Reset() error
	Close()
	// AttributeSchema describes the per-point attributes this reader emits and
	// the packed layout of Point64.Attributes: values are stored contiguously,
	// little-endian, in schema order (see model.AttributeSchemaLayout). The
	// schema is fixed for the reader's lifetime; nil means no attributes are
	// emitted. Fields a reader cannot provide for some points must be written
	// as zero values.
	AttributeSchema() []model.AttributeDescriptor
}
