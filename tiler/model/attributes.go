package model

import (
	"fmt"
	"strings"
)

// Standard optional per-point attribute names.
const (
	AttrIntensity       = "intensity"
	AttrClassification  = "classification"
	AttrReturnNumber    = "return_number"
	AttrNumberOfReturns = "number_of_returns"
)

// knownAttributes is the registry of currently supported optional attribute names.
var knownAttributes = map[string]struct{}{
	AttrIntensity:       {},
	AttrClassification:  {},
	AttrReturnNumber:    {},
	AttrNumberOfReturns: {},
}

// Attributes is a set of optional per-point attribute names to include in output tiles.
// The zero value (nil map) means no optional attributes are exported.
// Use DefaultAttributes() to get the default set with all supported attributes enabled.
type Attributes map[string]struct{}

// NewAttributes creates an Attributes set containing the given names.
func NewAttributes(names ...string) Attributes {
	a := make(Attributes, len(names))
	for _, n := range names {
		a[n] = struct{}{}
	}
	return a
}

// DefaultAttributes returns an Attributes set with all currently supported optional attributes.
func DefaultAttributes() Attributes {
	return NewAttributes(AttrIntensity, AttrClassification)
}

// Has reports whether the named attribute is in the set.
func (a Attributes) Has(name string) bool {
	_, ok := a[name]
	return ok
}

// ParseAttributes converts a slice of attribute name strings into an Attributes set.
// Returns an error for any unrecognised name.
func ParseAttributes(attrs []string) (Attributes, error) {
	for _, a := range attrs {
		if strings.TrimSpace(strings.ToLower(a)) == "none" {
			return make(Attributes), nil
		}
	}
	set := make(Attributes, len(attrs))
	for _, a := range attrs {
		name := strings.TrimSpace(strings.ToLower(a))
		if _, ok := knownAttributes[name]; !ok {
			return nil, fmt.Errorf("unknown attribute %q: supported values are %q, %q, %q, %q",
				a, AttrIntensity, AttrClassification, AttrReturnNumber, AttrNumberOfReturns)
		}
		set[name] = struct{}{}
	}
	return set, nil
}
