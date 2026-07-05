package mutator

import "github.com/mfbonfigli/gotiler-core/tiler/model"

// ZOffset is a mutator that shifts the points vertically for the given offset
type ZOffset struct {
	Offset float32
}

func NewZOffset(offset float32) *ZOffset {
	return &ZOffset{
		Offset: offset,
	}
}

func (z *ZOffset) RequiredAttributes() model.Attributes {
	return nil
}

func (z *ZOffset) Mutate(pt model.Point, attrs model.AttributeView, localToGlobal model.Transform) (model.Point, bool) {
	pt.Z += z.Offset
	return pt, true
}
