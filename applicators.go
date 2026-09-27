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
func validateApplicators(c *v6.Schema, v any, path *refStack, es evalSet) error {
	if c.Not != nil {
		if err := validateNot(c.Not, v, path); err != nil {
			return err
		}
	}

	if len(c.AllOf) > 0 {
		if err := validateAllOf(c.AllOf, v, path, es); err != nil {
			return err
		}
	}

	if len(c.AnyOf) > 0 {
		if err := validateAnyOf(c.AnyOf, v, path, es); err != nil {
			return err
		}
	}

	if len(c.OneOf) > 0 {
		if err := validateOneOf(c.OneOf, v, path, es); err != nil {
			return err
		}
	}

	if c.If != nil {
		if err := validateIfThenElse(c, v, path, es); err != nil {
			return err
		}
	}

	return nil
}

// validateNot passes nil es: a `not` that passes means its subschema did NOT
// match, so nothing it touched counts as evaluated for the parent.
func validateNot(sub *v6.Schema, v any, path *refStack) error {
	err := validate(sub, v, path, nil)
	if errors.Is(err, ErrNotImplemented) {
		return err
	}
	if err == nil {
		return &ValidationError{Msg: "value must not match the `not` subschema"}
	}
	return nil
}

// validateAllOf passes es directly: every branch must pass, so every branch's
// evaluations count (a failure fails the whole node anyway, discarding nothing
// meaningful).
func validateAllOf(subs []*v6.Schema, v any, path *refStack, es evalSet) error {
	for i, sub := range subs {
		if err := validate(sub, v, path, es); err != nil {
			if errors.Is(err, ErrNotImplemented) {
				return err
			}
			return &ValidationError{Msg: fmt.Sprintf("value does not match allOf[%d]", i)}
		}
	}
	return nil
}

// validateAnyOf merges every matching branch's evaluations (branch validates
// into a scratch set, merged only on success so a failed branch contributes
// nothing).
func validateAnyOf(subs []*v6.Schema, v any, path *refStack, es evalSet) error {
	matched := false
	for _, sub := range subs {
		scratch := scratchFrom(es)
		err := validate(sub, v, path, scratch)
		if errors.Is(err, ErrNotImplemented) {
			return err
		}
		if err == nil {
			matched = true
			mergeScratch(es, scratch)
		}
	}
	if !matched {
		return &ValidationError{Msg: "value does not match any anyOf subschema"}
	}
	return nil
}

// validateOneOf merges the single matching branch's evaluations.
func validateOneOf(subs []*v6.Schema, v any, path *refStack, es evalSet) error {
	matched := 0
	var winner evalSet
	for _, sub := range subs {
		scratch := scratchFrom(es)
		err := validate(sub, v, path, scratch)
		if errors.Is(err, ErrNotImplemented) {
			return err
		}
		if err == nil {
			matched++
			winner = scratch
		}
	}
	if matched != 1 {
		return &ValidationError{Msg: fmt.Sprintf("value matches %d oneOf subschemas, want exactly 1", matched)}
	}
	mergeScratch(es, winner)
	return nil
}

// validateIfThenElse merges the `if` branch (when it matches) and the taken arm.
func validateIfThenElse(c *v6.Schema, v any, path *refStack, es evalSet) error {
	ifScratch := scratchFrom(es)
	ifErr := validate(c.If, v, path, ifScratch)
	if errors.Is(ifErr, ErrNotImplemented) {
		return ifErr
	}
	if ifErr == nil {
		// `if` matched: its evaluations count, and `then` must pass (if present).
		mergeScratch(es, ifScratch)
		if c.Then != nil {
			if err := validate(c.Then, v, path, es); err != nil {
				if errors.Is(err, ErrNotImplemented) {
					return err
				}
				return &ValidationError{Msg: "value matched `if` but not `then`"}
			}
		}
		return nil
	}
	// `if` failed: `else` must pass (if present). The failed `if` contributes
	// nothing (ifScratch discarded).
	if c.Else != nil {
		if err := validate(c.Else, v, path, es); err != nil {
			if errors.Is(err, ErrNotImplemented) {
				return err
			}
			return &ValidationError{Msg: "value did not match `if` and failed `else`"}
		}
	}
	return nil
}
