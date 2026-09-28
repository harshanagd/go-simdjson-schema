package jsonschema

import (
	"reflect"

	v6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// refChunk is the number of entries held in one refStack node. Almost every
// schema's active $ref chain fits in a single chunk, so lookup is a contiguous
// scan over one small array; deeper nesting spills into a further chunk
// allocated as a stack local in the recursion frame that needs it.
const refChunk = 16

// refKey identifies a (schema, instance-node) pair on the active $ref path. The
// instance is part of the key because a cycle is only a true infinite loop when
// the SAME schema re-validates the SAME instance node — re-entering a schema at
// a deeper instance node (a recursive tree walking into a child) is legitimate
// and must proceed. instPtr is the backing pointer of a composite instance
// (map/slice); scalars have no address and cannot recurse, so they key as 0.
type refKey struct {
	schema  *v6.Schema
	instPtr uintptr
}

// refStack is the cycle-guard set for the active $ref-resolution path. Its
// extend operation (with) returns an EXTENDED COPY rather than mutating in
// place: a child recursion is handed the extended set, and when it returns the
// parent's set is unchanged — so sibling subschemas never see each other's
// entries, only descendants do. Sibling-independence is therefore a property of
// the type, not of caller pop-discipline (there is no pop to forget).
//
// It grows on demand yet stays entirely on the stack: the first refChunk entries
// live in this node's array, and when that fills, with returns a fresh chunk
// linked back through prev — a stack local in the recursion frame that needs it.
// Growth is by recursion, not reallocation: no heap allocation and no fixed cap.
type refStack struct {
	seen [refChunk]refKey
	n    int
	prev *refStack
}

// has reports whether k is already on the active path, scanning this chunk and
// any older ones. n is tiny (real ref nesting is a handful deep), so the linear
// scan is effectively O(1) and beats a heap map on both constants and allocs.
func (r *refStack) has(k refKey) bool {
	for s := r; s != nil; s = s.prev {
		for i := 0; i < s.n; i++ {
			if s.seen[i] == k {
				return true
			}
		}
	}
	return false
}

// full reports whether the current chunk is at capacity.
func (r *refStack) full() bool { return r.n == refChunk }

// with returns r extended by k. If the current chunk has room it returns a copy
// of this chunk with k appended; if full, it returns a fresh chunk linked back
// to r through prev. Either way the result is a value the caller holds as a
// stack local — the parent's set is never mutated, so siblings do not see k.
func (r *refStack) with(k refKey) refStack {
	if !r.full() {
		c := *r // copy this chunk; parent untouched
		c.seen[c.n] = k
		c.n++
		return c
	}
	return refStack{seen: [refChunk]refKey{k}, n: 1, prev: r}
}

// instancePtr returns a stable identity for a composite instance node, or 0 for
// a scalar. Two composite values with the same backing pointer are the same
// node; scalars can never form a $ref cycle (a ref does not descend, and a
// scalar has no children to recurse into), so they need no identity.
func instancePtr(v any) uintptr {
	switch v.(type) {
	case map[string]any, []any:
		return reflect.ValueOf(v).Pointer()
	default:
		return 0
	}
}

// followRef validates v against target, guarding against cycles. This is pure
// cycle policy: if (target, v) is already on the path the ref is a true cycle
// and this sub-path succeeds vacuously (v6 does the same — re-entering the same
// node imposes no further constraint); otherwise recurse with the path extended
// by the pair. All set mechanics (growth, spill, sibling-independence) live in
// refStack.with. child is addressed here to keep the hot path threading a
// pointer; the copy happens only on this rare ref-follow, not per validate call.
func followRef(target *v6.Schema, v Instance, path *refStack, es evalSet, dr dref) error {
	k := refKey{schema: target, instPtr: instancePtr(v.Interface())}
	if path.has(k) {
		return nil
	}
	child := path.with(k)
	return validate(target, v, &child, es, dr)
}
