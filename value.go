package jsonschema

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// ValidationError describes why an instance failed validation. It is
// deliberately minimal for now; v6-style keyword-location detail is a later
// refinement.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return "jsonschema: " + e.Msg }

// equals reports whether two decoded JSON values are equal per JSON Schema
// semantics: numbers compare by mathematical value (1 == 1.0), arrays and
// objects compare structurally, everything else by Go equality.
func equals(a, b any) bool {
	an, bn := isNumber(a), isNumber(b)
	if an || bn {
		if !an || !bn {
			return false
		}
		ra, oka := ratOf(a)
		rb, okb := ratOf(b)
		if !oka || !okb {
			return false
		}
		return ra.Cmp(rb) == 0
	}

	switch av := a.(type) {
	case nil:
		return b == nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !equals(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, va := range av {
			vb, ok := bv[k]
			if !ok || !equals(va, vb) {
				return false
			}
		}
		return true
	}
	return false
}

// hasDuplicate reports whether any two array elements are equal (for
// uniqueItems). O(n^2) structural comparison; a hashing fast path is a later
// optimisation.
func hasDuplicate(arr []any) bool {
	for i := 0; i < len(arr); i++ {
		for j := i + 1; j < len(arr); j++ {
			if equals(arr[i], arr[j]) {
				return true
			}
		}
	}
	return false
}

// ratOf returns the exact rational value of a JSON number. It reads json.Number
// textually (exact for any literal) and falls back to float64 for the default
// decode path. Non-finite floats return false.
func ratOf(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(string(n))
		return r, ok
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, false
		}
		// Recover the shortest decimal that round-trips to this float, not the
		// exact IEEE-754 value: a caller who decoded 0.1 with stock json.Unmarshal
		// means 1/10, and SetFloat64 would give 3602879701896397/36028797018963968
		// — a wrong verdict on decimal bounds/multipleOf (numbers.md).
		r, ok := new(big.Rat).SetString(strconv.FormatFloat(n, 'g', -1, 64))
		return r, ok
	case int:
		return new(big.Rat).SetInt64(int64(n)), true
	}
	return nil, false
}

// isNumber reports whether v is a JSON number (not a boolean).
func isNumber(v any) bool {
	switch v.(type) {
	case json.Number, float64, int:
		return true
	}
	return false
}

// isIntegral reports whether a JSON number has zero fractional part, so `1.0`
// counts as an integer while `1.1` does not. It reads json.Number textually to
// avoid the float64 rounding that would make 1e300 spuriously "integral"; for a
// plain float64 it falls back to a fractional-part check.
func isIntegral(v any) bool {
	switch n := v.(type) {
	case json.Number:
		s := string(n)
		if !strings.ContainsAny(s, ".eE") {
			return true
		}
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			return false
		}
		return r.IsInt()
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return false
		}
		return n == math.Trunc(n)
	case int:
		return true
	}
	return false
}
