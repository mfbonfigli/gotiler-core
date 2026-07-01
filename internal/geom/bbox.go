package geom

type BoundingBox struct {
	Xmin, Xmax, Ymin, Ymax, Zmin, Zmax float64
}

// Constructor to properly initialize a boundingBox struct computing the mids
func NewBoundingBox(Xmin, Xmax, Ymin, Ymax, Zmin, Zmax float64) BoundingBox {
	bbox := BoundingBox{
		Xmin: Xmin,
		Xmax: Xmax,
		Ymin: Ymin,
		Ymax: Ymax,
		Zmin: Zmin,
		Zmax: Zmax,
	}
	return bbox
}

// AsCesiumBox returns the bounding box expressed according to the cesium "box" format
func (b BoundingBox) Center() (x, y, z float64) {
	return (b.Xmax + b.Xmin) / 2, (b.Ymax + b.Ymin) / 2, (b.Zmax + b.Zmin) / 2
}

// AsCesiumBox returns the bounding box expressed according to the cesium "box" format
func (b BoundingBox) AsCesiumBox() [12]float64 {
	xMid, yMid, zMid := b.Center()
	return [12]float64{
		xMid, yMid, zMid,
		(b.Xmax - xMid), 0, 0,
		0, (b.Ymax - yMid), 0,
		0, 0, (b.Zmax - zMid),
	}
}
