package jsonschema

import (
	"errors"
	"fmt"

	v6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// conj accumulates the result of a conjunction (allOf-like) of subschema checks
// where some branches may be gated (ErrNotImplemented). A definite failure wins
// over a gated branch — a conjunction with any definite failure is invalid
// regardless of an unsupported sibling — so we keep scanning past the first
// gated branch and only report unsupported if nothing definitely failed. This
// makes the verdict independent of Go's map-iteration order.
type conj struct {
	failure     error // first definite *ValidationError, if any
	unsupported error // first ErrNotImplemented, if any
}

// add records one subschema result. Returns true if a definite failure has been
// seen, so the caller may stop early (the verdict is already decided).
func (c *conj) add(err error) bool {
	if err == nil {
		return c.failure != nil
	}
	if errors.Is(err, ErrNotImplemented) {
		if c.unsupported == nil {
			c.unsupported = err
		}
		return c.failure != nil
	}
	if c.failure == nil {
		c.failure = err
	}
	return true
}

// result returns the decided verdict: a definite failure if one occurred,
// otherwise the deferred unsupported, otherwise nil (all branches passed).
func (c *conj) result() error {
	if c.failure != nil {
		return c.failure
	}
	return c.unsupported
}

// validateObjectApplicators handles the object keywords that apply a subschema:
// properties, patternProperties, additionalProperties, propertyNames,
// dependentSchemas, and the draft-≤7 dependencies schema form. It mirrors v6's
// objValidate. Annotation tracking (for unevaluated*) is not built here — that
// keyword stays gated, and without it the evaluated-set does not affect the
// verdict.
func validateObjectApplicators(c *v6.Schema, obj map[string]any, path *refStack) error {
	var cj conj

	// The property-dependency keyword, across drafts. dependencies (draft ≤7)
	// was split in 2019-09 into dependentRequired (the []string presence form,
	// handled in validateObject) and dependentSchemas (the subschema form). Both
	// spellings are kept adjacent here because they are the same keyword: v6
	// populates Dependencies OR DependentSchemas depending on the schema's draft,
	// never both, and both trigger on "is pname present in obj".

	// dependencies (draft ≤7): []string is the required-property form (handled
	// here; dependentRequired is the disjoint 2019+ replacement), or *Schema.
	for pname, dep := range c.Dependencies {
		if _, ok := obj[pname]; !ok {
			continue
		}
		switch d := dep.(type) {
		case []string:
			for _, req := range d {
				if _, ok := obj[req]; !ok {
					if cj.add(&ValidationError{Msg: fmt.Sprintf("dependencies: %q requires %q", pname, req)}) {
						return cj.result()
					}
				}
			}
		case *v6.Schema:
			if cj.add(wrapOrPropagate(validate(d, obj, path), fmt.Sprintf("dependencies subschema for %q", pname))) {
				return cj.result()
			}
		}
	}

	// dependentSchemas (2019+): the draft-current spelling of the dependencies
	// subschema form above — if pname is present, the whole object must satisfy
	// the subschema.
	for pname, sub := range c.DependentSchemas {
		if _, ok := obj[pname]; !ok {
			continue
		}
		if cj.add(wrapOrPropagate(validate(sub, obj, path), fmt.Sprintf("dependentSchemas for %q", pname))) {
			return cj.result()
		}
	}

	for pname, pvalue := range obj {
		evaluated := false

		if sub, ok := c.Properties[pname]; ok {
			evaluated = true
			if cj.add(wrapOrPropagate(validate(sub, pvalue, path), fmt.Sprintf("property %q", pname))) {
				return cj.result()
			}
		}

		for regex, sub := range c.PatternProperties {
			if regex.MatchString(pname) {
				evaluated = true
				if cj.add(wrapOrPropagate(validate(sub, pvalue, path), fmt.Sprintf("patternProperties match for %q", pname))) {
					return cj.result()
				}
			}
		}

		if !evaluated && c.AdditionalProperties != nil {
			switch ap := c.AdditionalProperties.(type) {
			case bool:
				if !ap {
					if cj.add(&ValidationError{Msg: fmt.Sprintf("additionalProperties: %q not allowed", pname)}) {
						return cj.result()
					}
				}
			case *v6.Schema:
				if cj.add(wrapOrPropagate(validate(ap, pvalue, path), fmt.Sprintf("additionalProperties for %q", pname))) {
					return cj.result()
				}
			}
		}
	}

	if c.PropertyNames != nil {
		for pname := range obj {
			if cj.add(wrapOrPropagate(validate(c.PropertyNames, pname, path), fmt.Sprintf("propertyNames for %q", pname))) {
				return cj.result()
			}
		}
	}

	return cj.result()
}

// validateArrayApplicators handles items/prefixItems/additionalItems and
// contains/minContains/maxContains. It mirrors v6's arrValidate, covering both
// the draft-<2020 (Items + AdditionalItems) and 2020 (PrefixItems + Items2020)
// shapes.
func validateArrayApplicators(c *v6.Schema, arr []any, path *refStack) error {
	var cj conj
	evaluated := 0

	if c.PrefixItems != nil || c.Items2020 != nil {
		// Draft 2020 shape.
		evaluated = min(len(c.PrefixItems), len(arr))
		for i := 0; i < evaluated; i++ {
			if cj.add(wrapOrPropagate(validate(c.PrefixItems[i], arr[i], path), fmt.Sprintf("prefixItems[%d]", i))) {
				return cj.result()
			}
		}
		if c.Items2020 != nil {
			for i := evaluated; i < len(arr); i++ {
				if cj.add(wrapOrPropagate(validate(c.Items2020, arr[i], path), fmt.Sprintf("items[%d]", i))) {
					return cj.result()
				}
			}
		}
	} else if c.Items != nil {
		// Draft <2020 shape: Items is *Schema or []*Schema.
		switch items := c.Items.(type) {
		case *v6.Schema:
			for i, item := range arr {
				if cj.add(wrapOrPropagate(validate(items, item, path), fmt.Sprintf("items[%d]", i))) {
					return cj.result()
				}
			}
			evaluated = len(arr)
		case []*v6.Schema:
			evaluated = min(len(arr), len(items))
			for i := 0; i < evaluated; i++ {
				if cj.add(wrapOrPropagate(validate(items[i], arr[i], path), fmt.Sprintf("items[%d]", i))) {
					return cj.result()
				}
			}
		}
	}

	if c.AdditionalItems != nil {
		switch ai := c.AdditionalItems.(type) {
		case bool:
			if !ai && evaluated != len(arr) {
				if cj.add(&ValidationError{Msg: fmt.Sprintf("additionalItems: %d extra items not allowed", len(arr)-evaluated)}) {
					return cj.result()
				}
			}
		case *v6.Schema:
			for i := evaluated; i < len(arr); i++ {
				if cj.add(wrapOrPropagate(validate(ai, arr[i], path), fmt.Sprintf("additionalItems[%d]", i))) {
					return cj.result()
				}
			}
		}
	}

	if c.Contains != nil {
		// contains is not a conjunction — it has its own counting semantics and
		// propagates ErrNotImplemented directly, so it is not folded into cj.
		if err := validateContains(c, arr, path); err != nil {
			// A definite contains failure outranks a deferred unsupported sibling.
			if !errors.Is(err, ErrNotImplemented) {
				return err
			}
			cj.add(err)
		}
	}

	return cj.result()
}

// validateContains implements contains + minContains/maxContains. Default
// minContains is 1 (at least one item must match) unless minContains is set.
func validateContains(c *v6.Schema, arr []any, path *refStack) error {
	matched := 0
	for _, item := range arr {
		err := validate(c.Contains, item, path)
		if errors.Is(err, ErrNotImplemented) {
			return err
		}
		if err == nil {
			matched++
		}
	}

	wantMin := 1
	if c.MinContains != nil {
		wantMin = *c.MinContains
	}
	if matched < wantMin {
		return &ValidationError{Msg: fmt.Sprintf("contains: %d items match, want >= %d", matched, wantMin)}
	}
	if c.MaxContains != nil && matched > *c.MaxContains {
		return &ValidationError{Msg: fmt.Sprintf("contains: %d items match, want <= %d", matched, *c.MaxContains)}
	}
	return nil
}

// wrapOrPropagate passes ErrNotImplemented through unchanged (so a gated
// subschema surfaces as unsupported) and wraps any real validation failure with
// a locating message.
func wrapOrPropagate(err error, where string) error {
	if err == nil || errors.Is(err, ErrNotImplemented) {
		return err
	}
	return &ValidationError{Msg: where + ": " + err.Error()}
}
