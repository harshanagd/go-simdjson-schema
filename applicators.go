package jsonschema

import (
	"errors"
	"fmt"

	v6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// validateApplicators runs the section-6 in-place applicators: not, allOf,
// anyOf, oneOf, and if/then/else. Each combines the boolean results of
// validating the instance against subschemas. Object/array applicators
// (properties, items, contains, …) are a separate later slice and remain gated.
//
// ErrNotImplemented from a subschema is propagated, never swallowed: an anyOf
// branch that uses an unimplemented keyword must surface as unsupported, not be
// silently treated as a non-match (which could flip the verdict).
func validateApplicators(c *v6.Schema, v any) error {
	if c.Not != nil {
		if err := validateNot(c.Not, v); err != nil {
			return err
		}
	}

	if len(c.AllOf) > 0 {
		if err := validateAllOf(c.AllOf, v); err != nil {
			return err
		}
	}

	if len(c.AnyOf) > 0 {
		if err := validateAnyOf(c.AnyOf, v); err != nil {
			return err
		}
	}

	if len(c.OneOf) > 0 {
		if err := validateOneOf(c.OneOf, v); err != nil {
			return err
		}
	}

	if c.If != nil {
		if err := validateIfThenElse(c, v); err != nil {
			return err
		}
	}

	return nil
}

func validateNot(sub *v6.Schema, v any) error {
	err := validate(sub, v)
	if errors.Is(err, ErrNotImplemented) {
		return err
	}
	if err == nil {
		return &ValidationError{Msg: "value must not match the `not` subschema"}
	}
	return nil
}

func validateAllOf(subs []*v6.Schema, v any) error {
	for i, sub := range subs {
		if err := validate(sub, v); err != nil {
			if errors.Is(err, ErrNotImplemented) {
				return err
			}
			return &ValidationError{Msg: fmt.Sprintf("value does not match allOf[%d]", i)}
		}
	}
	return nil
}

func validateAnyOf(subs []*v6.Schema, v any) error {
	for _, sub := range subs {
		err := validate(sub, v)
		if errors.Is(err, ErrNotImplemented) {
			return err
		}
		if err == nil {
			return nil
		}
	}
	return &ValidationError{Msg: "value does not match any anyOf subschema"}
}

func validateOneOf(subs []*v6.Schema, v any) error {
	matched := 0
	for _, sub := range subs {
		err := validate(sub, v)
		if errors.Is(err, ErrNotImplemented) {
			return err
		}
		if err == nil {
			matched++
		}
	}
	if matched != 1 {
		return &ValidationError{Msg: fmt.Sprintf("value matches %d oneOf subschemas, want exactly 1", matched)}
	}
	return nil
}

func validateIfThenElse(c *v6.Schema, v any) error {
	ifErr := validate(c.If, v)
	if errors.Is(ifErr, ErrNotImplemented) {
		return ifErr
	}
	if ifErr == nil {
		// `if` passed: `then` must pass (if present).
		if c.Then != nil {
			if err := validate(c.Then, v); err != nil {
				if errors.Is(err, ErrNotImplemented) {
					return err
				}
				return &ValidationError{Msg: "value matched `if` but not `then`"}
			}
		}
		return nil
	}
	// `if` failed: `else` must pass (if present).
	if c.Else != nil {
		if err := validate(c.Else, v); err != nil {
			if errors.Is(err, ErrNotImplemented) {
				return err
			}
			return &ValidationError{Msg: "value did not match `if` and failed `else`"}
		}
	}
	return nil
}
