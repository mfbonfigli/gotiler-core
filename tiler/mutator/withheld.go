package mutator

import "github.com/mfbonfigli/gotiler-core/tiler/model"

// WithheldFilter discards points whose withheld attribute is true.
type WithheldFilter struct{}

func NewWithheldFilter() *WithheldFilter {
	return &WithheldFilter{}
}

func (f *WithheldFilter) RequiredAttributes() model.Attributes {
	return model.NewAttributes(model.AttrWithheld)
}

func (f *WithheldFilter) Mutate(pt model.Point, attrs model.AttributeView, localToGlobal model.Transform) (model.Point, bool) {
	i := attrs.Index(model.AttrWithheld)
	if i < 0 {
		return pt, true
	}
	withheld, err := attrs.Bool(i)
	if err != nil {
		return pt, true
	}
	return pt, !withheld
}
