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
	// path is the $ref-cycle guard. It is a stack local (a chunked set that
	// grows by recursion, never to the heap — see ref.go) threaded through the
	// recursion by pointer. On a ref-free schema no chunk is ever filled, so it
	// costs one zero-valued struct on the frame and nothing else.
	var path refStack
	return validate(s.c, instance, &path, nil)
}

// validate checks v against the compiled schema c. path guards $ref cycles;
// parentES is the evaluated-set of the nearest ancestor unevaluated* owner at
// this same instance node (nil if none), which this node contributes its
// evaluations to.
func validate(c *v6.Schema, v any, path *refStack, parentES evalSet) error {
	// The gate is checked per schema, not just at the top level: an applicator
	// subschema may itself use a keyword we do not implement, and that must
	// surface as ErrNotImplemented rather than a silently-skipped assertion.
	if kw := usesUnimplemented(c); kw != "" {
		return fmt.Errorf("%w: %s", ErrNotImplemented, kw)
	}

	// Section 9: a node that owns an unevaluated* keyword tracks its OWN subtree's
	// evaluations in a fresh set (even when an ancestor set was passed down —
	// unevaluated* scope is per schema subtree), applies unevaluated* to that
	// set in phase 2, then merges its evaluations up into the parent set so an
	// outer unevaluated* also sees them. A non-owner uses the parent set directly.
	es := parentES
	owns := false
	if needsEvalSet(c) {
		if own := newEvalSet(v, c); own != nil {
			es = own
			owns = true
		}
	}

	// Section 7: references. v6 resolves $ref/$recursiveRef at compile time into
	// direct *Schema pointers, and DynamicRef.Ref is the lexically-nearest
	// (single-context) dynamic target — so following the pointer is the whole
	// job, with cycle detection via path. Genuinely multi-context $dynamicRef
	// (an Anchor whose resolution differs by runtime scope) is gated in
	// usesUnimplemented and never reaches here.
	if c.Ref != nil {
		if err := followRef(c.Ref, v, path, es); err != nil {
			return err
		}
	}
	if c.RecursiveRef != nil {
		if err := followRef(c.RecursiveRef, v, path, es); err != nil {
			return err
		}
	}
	if c.DynamicRef != nil && c.DynamicRef.Ref != nil {
		if err := followRef(c.DynamicRef.Ref, v, path, es); err != nil {
			return err
		}
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
		// Section: format assertion. v6 populates c.Format only when it has
		// decided this format must assert (draft default or WithFormatAssertion),
		// and Validate closes over the matching RFC check — so a non-nil return
		// is a format violation. Scoped to strings; format never applies to other
		// types.
		if c.Format != nil {
			if err := c.Format.Validate(tv); err != nil {
				return &ValidationError{Msg: "value does not match format " + c.Format.Name}
			}
		}
	case []any:
		if err := validateArray(c, tv, path, es); err != nil {
			return err
		}
	case map[string]any:
		if err := validateObject(c, tv, path, es); err != nil {
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
	if err := validateApplicators(c, v, path, es); err != nil {
		return err
	}

	// Section 9 phase 2: this node owns an unevaluated* keyword, and every other
	// keyword has now marked es. Apply unevaluated* to the members left unmarked,
	// then merge this subtree's evaluations up so an outer owner sees them too.
	if owns {
		if err := validateUnevaluated(c, v, path, es); err != nil {
			return err
		}
		mergeScratch(parentES, es)
	}
	return nil
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

func validateArray(c *v6.Schema, arr []any, path *refStack, es evalSet) error {
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
	return validateArrayApplicators(c, arr, path, es)
}

func validateObject(c *v6.Schema, obj map[string]any, path *refStack, es evalSet) error {
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
	return validateObjectApplicators(c, obj, path, es)
}

// validateUnevaluated is section-9 phase 2: after every other keyword has marked
// es, apply UnevaluatedProperties to each object property not marked, and
// UnevaluatedItems to each array index not marked. Subschema recursion descends
// into a child value, so it passes nil es (the child owns its own set).
func validateUnevaluated(c *v6.Schema, v any, path *refStack, es evalSet) error {
	switch tv := v.(type) {
	case map[string]any:
		if c.UnevaluatedProperties == nil {
			return nil
		}
		for pname, pvalue := range tv {
			if es.propEvaluated(pname) {
				continue
			}
			if err := validate(c.UnevaluatedProperties, pvalue, path, nil); err != nil {
				return wrapOrPropagate(err, fmt.Sprintf("unevaluatedProperties for %q", pname))
			}
			// unevaluatedProperties applied to this property, so it is now
			// evaluated — record it so an outer unevaluated* sees it too.
			es.markProp(pname)
		}
	case []any:
		if c.UnevaluatedItems == nil {
			return nil
		}
		for i, item := range tv {
			if es.itemEvaluated(i) {
				continue
			}
			if err := validate(c.UnevaluatedItems, item, path, nil); err != nil {
				return wrapOrPropagate(err, fmt.Sprintf("unevaluatedItems[%d]", i))
			}
			es.markItem(i)
		}
	}
	return nil
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
