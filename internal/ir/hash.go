package ir

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

// Hash computes a deterministic Merkle hash of the Package.
// The hash is over the package name and the sorted hashes of all types.
func (p *Package) Hash() [32]byte {
	h := sha256.New()
	h.Write([]byte("pkg\x00"))
	h.Write([]byte(p.Name))
	h.Write([]byte{0})

	// Sort type hashes for determinism (not dependent on insertion order).
	type nameHash struct {
		name string
		hash [32]byte
	}
	entries := make([]nameHash, 0, len(p.Types))
	for name, t := range p.Types {
		entries = append(entries, nameHash{name, t.Hash()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})
	for _, e := range entries {
		h.Write([]byte(e.name))
		h.Write([]byte{0})
		h.Write(e.hash[:])
	}

	var out [32]byte
	h.Sum(out[:0])
	return out
}

// hashCache provides lazy caching for computed hashes.
type hashCache struct {
	once sync.Once
	val  [32]byte
}

func (c *hashCache) get(compute func() [32]byte) [32]byte {
	c.once.Do(func() {
		c.val = compute()
	})
	return c.val
}

// Hash computes a deterministic Merkle hash of the Type.
// The hash includes the type's kind, name, description, kind-specific
// children (as their hashes), and constraints.
func (t *Type) Hash() [32]byte {
	return t.cache.get(func() [32]byte {
		h := sha256.New()
		h.Write([]byte("type\x00"))
		writeString(h, t.Name)
		writeUint8(h, uint8(t.Kind))
		writeString(h, t.Description)

		switch t.Kind {
		case KindStruct:
			// Fields are order-dependent (declaration order matters).
			for _, f := range t.Fields {
				fh := f.Hash()
				h.Write(fh[:])
			}

		case KindEnum:
			writeString(h, t.EnumType)
			for _, v := range t.EnumValues {
				writeString(h, fmt.Sprint(v))
			}

		case KindUnion:
			writeString(h, t.Discriminator)
			// Variants sorted by name for determinism.
			type vh struct {
				name string
				hash [32]byte
			}
			vhs := make([]vh, len(t.Variants))
			for i, v := range t.Variants {
				vhs[i] = vh{v.Name, v.Hash()}
			}
			sort.Slice(vhs, func(i, j int) bool {
				return vhs[i].name < vhs[j].name
			})
			for _, v := range vhs {
				h.Write(v.hash[:])
			}

		case KindList:
			if t.Items != nil {
				ih := t.Items.Hash()
				h.Write(ih[:])
			}

		case KindMap:
			if t.MapValue != nil {
				mh := t.MapValue.Hash()
				h.Write(mh[:])
			}

		case KindScalar:
			writeString(h, t.ScalarType)

		case KindRef:
			writeString(h, t.RefName)

		case KindNullable:
			if t.Inner != nil {
				ih := t.Inner.Hash()
				h.Write(ih[:])
			}
		}

		// Constraints sorted by keyword for determinism.
		writeConstraints(h, t.Constraints)

		var out [32]byte
		h.Sum(out[:0])
		return out
	})
}

// Hash computes a deterministic hash of the Field.
func (f *Field) Hash() [32]byte {
	h := sha256.New()
	h.Write([]byte("field\x00"))
	writeString(h, f.Name)
	writeString(h, f.JSONName)
	writeString(h, f.Description)
	if f.Required {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}

	th := f.Type.Hash()
	h.Write(th[:])

	writeConstraints(h, f.Constraints)

	var out [32]byte
	h.Sum(out[:0])
	return out
}

// Hash computes a deterministic hash of the TypeRef.
func (r *TypeRef) Hash() [32]byte {
	h := sha256.New()
	h.Write([]byte("ref\x00"))
	if r.Name != "" {
		writeString(h, r.Name)
	} else if r.Inline != nil {
		ih := r.Inline.Hash()
		h.Write(ih[:])
	}

	var out [32]byte
	h.Sum(out[:0])
	return out
}

// Hash computes a deterministic hash of the Variant.
func (v *Variant) Hash() [32]byte {
	h := sha256.New()
	h.Write([]byte("variant\x00"))
	writeString(h, v.Name)
	writeString(h, v.Discriminator)
	th := v.TypeRef.Hash()
	h.Write(th[:])

	var out [32]byte
	h.Sum(out[:0])
	return out
}

// Hash computes a deterministic hash of the Constraint.
func (c *Constraint) Hash() [32]byte {
	h := sha256.New()
	h.Write([]byte("constraint\x00"))
	writeString(h, c.Keyword)
	writeString(h, fmt.Sprint(c.Value))

	var out [32]byte
	h.Sum(out[:0])
	return out
}

// writeString writes a null-terminated string to the hasher.
func writeString(h interface{ Write([]byte) (int, error) }, s string) {
	h.Write([]byte(s))
	h.Write([]byte{0})
}

// writeUint8 writes a single byte to the hasher.
func writeUint8(h interface{ Write([]byte) (int, error) }, v uint8) {
	h.Write([]byte{v})
}

// writeConstraints writes sorted constraint hashes to the hasher.
func writeConstraints(h interface{ Write([]byte) (int, error) }, cs []Constraint) {
	if len(cs) == 0 {
		return
	}
	// Sort by keyword for determinism.
	type ch struct {
		keyword string
		hash    [32]byte
	}
	sorted := make([]ch, len(cs))
	for i, c := range cs {
		sorted[i] = ch{c.Keyword, c.Hash()}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].keyword < sorted[j].keyword
	})
	// Write count then hashes.
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(len(sorted)))
	h.Write(buf[:])
	for _, c := range sorted {
		h.Write(c.hash[:])
	}
}
