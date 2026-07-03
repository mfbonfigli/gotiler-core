package model

import "fmt"

// AttributeDescriptor describes one per-point attribute emitted by a reader:
// its canonical name and scalar type. A reader's schema (ordered descriptor
// list) defines the packed layout of the AttributeValues it attaches to every
// point: values are stored contiguously, little-endian, in schema order.
type AttributeDescriptor struct {
	Name string
	Type AttributeType
}

// AttributeSchemaLayout computes the packed value layout of a reader schema.
// All descriptors must have a sized type. The returned total is the byte size
// of one point's packed AttributeValues.
func AttributeSchemaLayout(schema []AttributeDescriptor) (entries []AttributeLayoutEntry, total int, err error) {
	for i, desc := range schema {
		size, ok := AttributeTypeSize(desc.Type)
		if !ok {
			return nil, 0, fmt.Errorf("attribute %q: type %q has no defined size", desc.Name, desc.Type)
		}
		entries = append(entries, AttributeLayoutEntry{
			Name:        desc.Name,
			Type:        desc.Type,
			Offset:      total,
			Size:        size,
			SourceIndex: i,
		})
		total += size
	}
	return entries, total, nil
}

// AttributeValuesArena hands out per-point AttributeValues buffers carved from
// large shared blocks, amortizing one heap allocation over thousands of
// points. Returned buffers are zero-initialized. Not safe for concurrent use:
// callers must synchronize externally (readers typically already hold a mutex
// in GetNext).
type AttributeValuesArena struct {
	block []byte
	off   int
}

// arenaBlockSize balances allocation amortization against how long a block
// stays pinned by in-flight points that reference sub-slices of it.
const arenaBlockSize = 64 * 1024

// Alloc returns a zeroed n-byte AttributeValues buffer.
func (a *AttributeValuesArena) Alloc(n int) AttributeValues {
	if n <= 0 {
		return nil
	}
	if a.off+n > len(a.block) {
		size := arenaBlockSize
		if n > size {
			size = n
		}
		a.block = make([]byte, size)
		a.off = 0
	}
	out := a.block[a.off : a.off+n : a.off+n]
	a.off += n
	return out
}

// AttributeView is a typed window over one point's packed attribute values.
// It is a cheap value type (two slice headers): construct it per point without
// allocation. Setters write through to the underlying buffer.
type AttributeView struct {
	entries []AttributeLayoutEntry
	data    AttributeValues
}

// NewAttributeView builds a view over data using the given layout entries
// (see AttributeLayout / AttributeSchemaLayout).
func NewAttributeView(entries []AttributeLayoutEntry, data AttributeValues) AttributeView {
	return AttributeView{entries: entries, data: data}
}

// Len returns the number of attributes in the view.
func (v AttributeView) Len() int { return len(v.entries) }

// Name returns the canonical name of the i-th attribute.
func (v AttributeView) Name(i int) string { return v.entries[i].Name }

// Type returns the type of the i-th attribute.
func (v AttributeView) Type(i int) AttributeType { return v.entries[i].Type }

// Index returns the index of the named attribute (canonical name), or -1.
func (v AttributeView) Index(name string) int {
	for i := range v.entries {
		if v.entries[i].Name == name {
			return i
		}
	}
	return -1
}

// Value decodes and returns the i-th attribute value. The scalar is boxed into
// an any: convenient, but do not use it in per-point hot paths that care about
// allocations.
func (v AttributeView) Value(i int) (any, error) {
	e := &v.entries[i]
	if len(v.data) < e.Offset+e.Size {
		return ZeroAttributeValue(e.Type), nil
	}
	return DecodeAttributeValue(v.data[e.Offset:e.Offset+e.Size], e.Type)
}

// SetValue encodes val as the i-th attribute value, writing through to the
// underlying buffer. The value must be convertible to the attribute's type.
func (v AttributeView) SetValue(i int, val any) error {
	e := &v.entries[i]
	if len(v.data) < e.Offset+e.Size {
		return fmt.Errorf("attribute %q: packed values buffer too small", e.Name)
	}
	return EncodeAttributeValue(v.data[e.Offset:e.Offset+e.Size], e.Type, val)
}
