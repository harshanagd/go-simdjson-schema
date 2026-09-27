package jsonschema

import v6 "github.com/santhosh-tekuri/jsonschema/v6"

// evalSet records which members of ONE instance node have been evaluated by an
// applicator, so unevaluatedProperties/unevaluatedItems can apply to the
// remainder. The evaluator, applicators, and ref-following talk to this seam and
// never name a concrete container. It is created only for a node that has an
// unevaluated* keyword (nil otherwise — the common path allocates nothing).
//
// mapEvalSet is the heap-backed backend over map[string]any. A later tapeEvalSet
// (a stack-resident bitset keyed by tape ordinal) swaps in behind this seam with
// no evaluator change.
type evalSet interface {
	markProp(name string)
	markItem(i int)
	propEvaluated(name string) bool
	itemEvaluated(i int) bool
}

// newEvalSet returns a tracker for node v when the schema owns the unevaluated*
// keyword that applies to v's container type — unevaluatedProperties for an
// object, unevaluatedItems for an array. It returns nil otherwise, so a schema
// with only unevaluatedItems validating an object (or vice versa) allocates
// nothing.
func newEvalSet(v any, c *v6.Schema) evalSet {
	switch v.(type) {
	case map[string]any:
		if c.UnevaluatedProperties != nil {
			return &mapEvalSet{}
		}
	case []any:
		if c.UnevaluatedItems != nil {
			return &mapEvalSet{}
		}
	}
	return nil
}

// needsEvalSet reports whether the schema owns an unevaluated* keyword at all.
// The container-type match is decided in newEvalSet.
func needsEvalSet(c *v6.Schema) bool {
	return c.UnevaluatedProperties != nil || c.UnevaluatedItems != nil
}

type mapEvalSet struct {
	props map[string]struct{}
	items map[int]struct{}
}

func (m *mapEvalSet) markProp(name string) {
	if m.props == nil {
		m.props = make(map[string]struct{})
	}
	m.props[name] = struct{}{}
}

func (m *mapEvalSet) markItem(i int) {
	if m.items == nil {
		m.items = make(map[int]struct{})
	}
	m.items[i] = struct{}{}
}

func (m *mapEvalSet) propEvaluated(name string) bool {
	_, ok := m.props[name]
	return ok
}

func (m *mapEvalSet) itemEvaluated(i int) bool {
	_, ok := m.items[i]
	return ok
}

// mergeInto copies this set's marks into dst. Used by branch merge-on-success:
// a branch validates into a scratch set, which is merged only if the branch
// succeeds so a failed branch contributes no evaluations.
func (m *mapEvalSet) mergeInto(dst evalSet) {
	for name := range m.props {
		dst.markProp(name)
	}
	for i := range m.items {
		dst.markItem(i)
	}
}

// scratchFrom returns a fresh scratch set of the same backend as parent, for a
// branch whose marks must be discarded on failure. Returns nil when parent is
// nil (no unevaluated* in scope, so nothing to track).
func scratchFrom(parent evalSet) evalSet {
	if parent == nil {
		return nil
	}
	return &mapEvalSet{}
}

// mergeScratch merges a successful branch's scratch marks into the parent set.
// No-op when either is nil.
func mergeScratch(parent, scratch evalSet) {
	if parent == nil || scratch == nil {
		return
	}
	if ms, ok := scratch.(*mapEvalSet); ok {
		ms.mergeInto(parent)
	}
}

// markProp / markItem are nil-safe helpers: they no-op when no unevaluated* is
// in scope (es == nil), so applicators call them unconditionally.
func markProp(es evalSet, name string) {
	if es != nil {
		es.markProp(name)
	}
}

func markItem(es evalSet, i int) {
	if es != nil {
		es.markItem(i)
	}
}
