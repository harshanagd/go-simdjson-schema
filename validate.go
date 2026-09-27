package jsonschema

import (
	"fmt"
	"math/big"
	"unicode/utf8"

	v6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// Validate reports whether the instance satisfies the schema. It returns nil on
// success, ErrNotImplemented if the schema uses a keyword this evaluator does
// not yet cover, or a *ValidationError describing the first failure.
//
// The instance should be decoded with encoding/json using UseNumber so that
// integer/number distinctions survive. Accepted number types are json.Number,
// float64, and int — the types encoding/json produces; a struct-sourced int64
// or float32 is not recognised as a number here (unlike v6). float64 instances
// are accepted; decimal assertions on that path recover the shortest
// round-tripping decimal (value.go).
func (s *Schema) Validate(instance any) error {
	return validate(s.c, instance)
}

func validate(c *v6.Schema, v any) error {
	// The gate is checked per schema, not just at the top level: an applicator
	// subschema may itself use a keyword we do not implement, and that must
	// surface as ErrNotImplemented rather than a silently-skipped assertion.
	if kw := usesUnimplemented(c); kw != "" {
		return fmt.Errorf("%w: %s", ErrNotImplemented, kw)
	}

	if c.Bool != nil {
		if *c.Bool {
			return nil
		}
		return &ValidationError{Msg: "false schema always fails"}
	}

	// Section 1: type-agnostic assertions.
	if c.Types != nil && !typesMatch(c.Types, v) {
		return &ValidationError{Msg: "value is not of the required type(s)"}
	}
	if c.Const != nil && !equals(v, *c.Const) {
		return &ValidationError{Msg: "value does not equal const"}
	}
	if c.Enum != nil && !containsEqual(c.Enum.Values, v) {
		return &ValidationError{Msg: "value not in enum"}
	}

	// Sections 2-5 are type-scoped: each family only applies to its type. These
	// do NOT return early — applicators (section 6) run regardless of type and
	// in addition to the type-scoped keywords.
	switch tv := v.(type) {
	case string:
		if err := validateString(c, tv); err != nil {
			return err
		}
	case []any:
		if err := validateArray(c, tv); err != nil {
			return err
		}
	case map[string]any:
		if err := validateObject(c, tv); err != nil {
			return err
		}
	default:
		if isNumber(v) {
			if err := validateNumber(c, v); err != nil {
				return err
			}
		}
	}

	// Section 6: applicators (not / allOf / anyOf / oneOf / if-then-else).
	return validateApplicators(c, v)
}

// typesMatch reports whether the instance satisfies the schema's `type` set.
// v6 exposes the set only as strings (ToStrings), so we match on those.
func typesMatch(t *v6.Types, v any) bool {
	for _, name := range t.ToStrings() {
		if matchesType(name, v) {
			return true
		}
	}
	return false
}

func matchesType(name string, v any) bool {
	switch name {
	case "null":
		return v == nil
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "number":
		return isNumber(v)
	case "integer":
		return isNumber(v) && isIntegral(v)
	}
	return false
}

func validateString(c *v6.Schema, str string) error {
	if c.MinLength != nil || c.MaxLength != nil {
		n := utf8.RuneCountInString(str)
		if c.MinLength != nil && n < *c.MinLength {
			return &ValidationError{Msg: fmt.Sprintf("string length %d < minLength %d", n, *c.MinLength)}
		}
		if c.MaxLength != nil && n > *c.MaxLength {
			return &ValidationError{Msg: fmt.Sprintf("string length %d > maxLength %d", n, *c.MaxLength)}
		}
	}
	if c.Pattern != nil && !c.Pattern.MatchString(str) {
		return &ValidationError{Msg: "string does not match pattern"}
	}
	return nil
}

func validateArray(c *v6.Schema, arr []any) error {
	if c.MinItems != nil && len(arr) < *c.MinItems {
		return &ValidationError{Msg: fmt.Sprintf("array length %d < minItems %d", len(arr), *c.MinItems)}
	}
	if c.MaxItems != nil && len(arr) > *c.MaxItems {
		return &ValidationError{Msg: fmt.Sprintf("array length %d > maxItems %d", len(arr), *c.MaxItems)}
	}
	if c.UniqueItems && hasDuplicate(arr) {
		return &ValidationError{Msg: "array items are not unique"}
	}
	// Array applicator subschemas (items / prefixItems / additionalItems /
	// contains / minContains / maxContains).
	return validateArrayApplicators(c, arr)
}

func validateObject(c *v6.Schema, obj map[string]any) error {
	if c.MinProperties != nil && len(obj) < *c.MinProperties {
		return &ValidationError{Msg: fmt.Sprintf("object has %d properties < minProperties %d", len(obj), *c.MinProperties)}
	}
	if c.MaxProperties != nil && len(obj) > *c.MaxProperties {
		return &ValidationError{Msg: fmt.Sprintf("object has %d properties > maxProperties %d", len(obj), *c.MaxProperties)}
	}
	for _, name := range c.Required {
		if _, ok := obj[name]; !ok {
			return &ValidationError{Msg: fmt.Sprintf("missing required property %q", name)}
		}
	}
	// dependentRequired: if a named property is present, its dependencies must be too.
	for name, deps := range c.DependentRequired {
		if _, ok := obj[name]; !ok {
			continue
		}
		for _, dep := range deps {
			if _, ok := obj[dep]; !ok {
				return &ValidationError{Msg: fmt.Sprintf("property %q requires %q", name, dep)}
			}
		}
	}
	// Object applicator subschemas (properties / patternProperties /
	// additionalProperties / propertyNames / dependentSchemas / dependencies).
	return validateObjectApplicators(c, obj)
}

func validateNumber(c *v6.Schema, v any) error {
	if c.Minimum == nil && c.Maximum == nil && c.ExclusiveMinimum == nil &&
		c.ExclusiveMaximum == nil && c.MultipleOf == nil {
		return nil
	}
	r, ok := ratOf(v)
	if !ok {
		return &ValidationError{Msg: "number is not finite"}
	}
	if c.Minimum != nil && r.Cmp(c.Minimum) < 0 {
		return &ValidationError{Msg: "number < minimum"}
	}
	if c.Maximum != nil && r.Cmp(c.Maximum) > 0 {
		return &ValidationError{Msg: "number > maximum"}
	}
	if c.ExclusiveMinimum != nil && r.Cmp(c.ExclusiveMinimum) <= 0 {
		return &ValidationError{Msg: "number <= exclusiveMinimum"}
	}
	if c.ExclusiveMaximum != nil && r.Cmp(c.ExclusiveMaximum) >= 0 {
		return &ValidationError{Msg: "number >= exclusiveMaximum"}
	}
	if c.MultipleOf != nil && !isMultipleOf(r, c.MultipleOf) {
		return &ValidationError{Msg: "number is not a multiple of multipleOf"}
	}
	return nil
}

// isMultipleOf reports whether r is an integer multiple of factor, using exact
// rational arithmetic so decimals like 0.0001 do not drift.
func isMultipleOf(r, factor *big.Rat) bool {
	if factor.Sign() == 0 {
		return false
	}
	q := new(big.Rat).Quo(r, factor)
	return q.IsInt()
}
