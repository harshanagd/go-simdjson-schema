package jsonschema

import "math/big"

// Kind is the JSON type of an Instance node, as JSON Schema's `type` keyword
// distinguishes them. It is the discriminator the evaluator switches on instead
// of a Go type switch.
type Kind uint8

const (
	KindNull Kind = iota
	KindBool
	KindString
	KindNumber
	KindArray
	KindObject
	// KindUnknown is a value that is none of the six JSON types — e.g. an
	// off-contract Go type (int64, float32, json.RawMessage) that reached the
	// evaluator. It matches no `type` and carries no number, so such a value hits
	// no type-scoped keyword, exactly as the pre-seam code did (its number arm
	// was guarded by isNumber, so an unrecognised type fell through untouched).
	KindUnknown
)

// Instance is one JSON value under the validation cursor. A backend (the
// any-backed adapter today, a go-simdjson tape adapter later, or a caller's own)
// implements it; the evaluator names only this interface and holds no knowledge
// of the underlying representation.
//
// The migration to this seam is incremental (section-10 spec): Interface() is a
// transitional escape hatch that hands back the raw underlying value so a site
// not yet converted can keep switching on it. Each converted site drops its
// Interface() call; when the last one is gone, Interface() is removed and the
// tape backend — which cannot produce a raw any without materialising — becomes
// viable.
type Instance interface {
	// Kind reports the JSON type of this node — the discriminator the evaluator
	// switches on instead of a Go type switch.
	Kind() Kind
	// Bool returns the boolean value; valid only when Kind() == KindBool.
	Bool() bool
	// StringBytes returns the string value as bytes (borrowed, unescaped on the
	// tape); valid only when Kind() == KindString.
	StringBytes() []byte
	// Number returns the numeric value carrier; ok is false when this node is
	// not a number. Valid regardless of Kind (ok gates it).
	Number() (NumVal, bool)
	// Equal reports whether this node equals a decoded schema-side JSON value
	// (const/enum/uniqueItems), per JSON Schema equality: numbers by value,
	// arrays/objects structurally. other is a plain decoded value (map/slice/
	// scalar) from the compiled schema, never an Instance — so a tape backend
	// compares its region against it without materialising.
	Equal(other any) bool

	// Interface returns the underlying decoded value (map[string]any, []any,
	// string, bool, json.Number, float64, int, or nil). TRANSITIONAL — see the
	// type doc; do not add new callers.
	Interface() any
}

// NumVal carries a JSON number across the seam, preserving exactness (numbers.md
// / R4). The any backend holds the source value (json.Number/float64/int); the
// tape backend will hold the source bytes. Rat and IsIntegral are the two reads
// the evaluator needs.
type NumVal struct {
	v any // json.Number | float64 | int (any backend)
}

// Rat returns the exact rational value, or ok=false for a non-finite float.
func (n NumVal) Rat() (*big.Rat, bool) { return ratOf(n.v) }

// IsIntegral reports whether the number has zero fractional part (1.0 is
// integral, 1.1 is not).
func (n NumVal) IsIntegral() bool { return isIntegral(n.v) }

// wrap adapts a decoded any value to an Instance. It is the single construction
// point the evaluator uses while converting sites; it moves to the anyjson
// sub-package once the core no longer wraps child values itself.
func wrap(v any) Instance { return anyInstance{v} }

// anyInstance is the any-backed Instance.
type anyInstance struct{ v any }

func (a anyInstance) Interface() any { return a.v }

func (a anyInstance) Kind() Kind {
	switch a.v.(type) {
	case nil:
		return KindNull
	case bool:
		return KindBool
	case string:
		return KindString
	case []any:
		return KindArray
	case map[string]any:
		return KindObject
	}
	// The number types encoding/json produces (json.Number/float64/int). Guard
	// with isNumber so an off-contract type does not masquerade as a number —
	// it maps to KindUnknown and hits no type-scoped keyword, matching the
	// pre-seam behaviour.
	if isNumber(a.v) {
		return KindNumber
	}
	return KindUnknown
}

func (a anyInstance) Bool() bool { b, _ := a.v.(bool); return b }

func (a anyInstance) StringBytes() []byte {
	if s, ok := a.v.(string); ok {
		return []byte(s)
	}
	return nil
}

func (a anyInstance) Number() (NumVal, bool) {
	if isNumber(a.v) {
		return NumVal{a.v}, true
	}
	return NumVal{}, false
}

func (a anyInstance) Equal(other any) bool { return equals(a.v, other) }
