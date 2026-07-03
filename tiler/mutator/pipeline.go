package mutator

import (
	"github.com/mfbonfigli/gotiler-core/tiler/model"
)

// Pipeline is a mutator that applies all registered mutators sequentially
// and returns the result as output
type Pipeline struct {
	mutators []Mutator
}

func NewPipeline(m ...Mutator) *Pipeline {
	return &Pipeline{
		mutators: m,
	}
}

func (p *Pipeline) Mutate(pt model.Point, attrs model.AttributeView, localToGlobal model.Transform) (model.Point, bool) {
	for _, m := range p.mutators {
		keep := true
		pt, keep = m.Mutate(pt, attrs, localToGlobal)
		if !keep {
			return pt, false
		}
	}
	return pt, true
}
