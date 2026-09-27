package jsonschema

import (
	"reflect"
	"strconv"

	v6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// Section 7b: anchored $dynamicRef / $recursiveRef resolution over v6's exported
// compiled graph — the fork-free "N1" path (see .kiro/specs section-7b).
//
// v6 resolves these at validate time against a runtime dynamic scope, using an
// unexported per-resource dynamicAnchors map and an unexported resource pointer
// that our evaluator cannot reach. N1 rebuilds the equivalent from EXPORTED
// fields (Schema.ID marks resource boundaries; Schema.DynamicAnchor marks the
// anchors) and maintains its own scope stack. The T1 spike verified the
// reconstruction is byte-exact against v6's map on the 8-resource 2020-12
// metaschema.

// dynAnchors is a compiled schema's dynamic-anchor index: for each resource
// (keyed by its resource-root schema) the map from $dynamicAnchor name to the
// schema declaring it, mirroring v6's per-resource dynamicAnchors. It is built
// once per top-level Compile and shared read-only across validations.
type dynAnchors struct {
	// byResource[resRoot][name] = the schema carrying $dynamicAnchor:name within
	// the resource rooted at resRoot. Keyed by the resource-root *v6.Schema so
	// two resources sharing an anchor name stay distinct (the metaschema has
	// eight "meta" anchors, one per meta/* resource).
	byResource map[*v6.Schema]map[string]*v6.Schema
	// resourceRoots maps every reachable schema to the resource root it belongs
	// to, so a scope entry (any schema entered via a ref) resolves to its
	// resource for the anchor lookup.
	resourceRoots map[*v6.Schema]*v6.Schema
}

// put records that resource res declares $dynamicAnchor:name on schema sch,
// creating the per-resource map on first use.
func (da *dynAnchors) put(res *v6.Schema, name string, sch *v6.Schema) {
	m := da.byResource[res]
	if m == nil {
		m = map[string]*v6.Schema{}
		da.byResource[res] = m
	}
	m[name] = sch
}

// buildDynAnchors reconstructs the dynamic-scope resolution index from two
// sources, each used for what it can do reliably:
//
//   - resourceRoots (the scope partition: every reachable schema → its resource
//     root) is built by walking the compiled *Schema graph over EXPORTED fields.
//     This reaches every resource root reachable via $ref, including the
//     $recursiveAnchor roots, whose exported RecursiveAnchor flag is all the
//     $recursiveRef path needs.
//   - byResource (per-resource $dynamicAnchor name → *Schema) is built by
//     walking the RAW DOCUMENT structurally as v6 does (reaching un-$ref'd
//     $defs, which the compiled graph drops) and force-compiling each anchor
//     pointer on the same compiler to bridge to the exact compiled node. This is
//     the $dynamicRef path.
//
// Two sources because force-compiling a bare sub-pointer can misfire on a node
// that relies on inherited dialect (e.g. $recursiveAnchor:true read under the
// wrong draft), whereas the exported-graph flag is always correct — and the
// exported graph cannot see un-$ref'd $defs dynamic anchors, whereas the
// document walk can. Returns nil when the schema uses neither $dynamicAnchor nor
// $recursiveAnchor anywhere, so the mechanism is inert for the common schema.
func buildDynAnchors(root *v6.Schema, doc any, c *v6.Compiler) *dynAnchors {
	da := &dynAnchors{
		byResource:    map[*v6.Schema]map[string]*v6.Schema{},
		resourceRoots: map[*v6.Schema]*v6.Schema{},
	}
	// Pass A — exported-graph walk: partition every reachable schema into its
	// resource root (a node with a non-empty ID opens a resource), and collect
	// any $dynamicAnchor carried by a REACHABLE node (this covers remote/loaded
	// resources such as a $ref'd metaschema, whose anchors sit on reachable
	// resource roots). This drives the runtime scope for both $dynamicRef and
	// $recursiveRef.
	anyRecursive := collectReachable(root, da)

	// Pass B — document walk + force-compile: add $dynamicAnchors the exported
	// graph cannot reach (un-$ref'd $defs in the main document).
	bridgeDocAnchors(doc, c, da)

	if !anyRecursive && len(da.byResource) == 0 {
		return nil
	}
	return da
}

// collectReachable walks the compiled graph over exported fields, recording each
// schema's resource root and any reachable $dynamicAnchor into da, and reports
// whether any resource root carries $recursiveAnchor:true. The two closures are
// inline mutual recursion over v6.Schema's exported fields (a field may be a
// *Schema, or a slice/map/interface holding them) — there is no non-recursive
// way to walk an arbitrary struct shape.
func collectReachable(root *v6.Schema, da *dynAnchors) bool {
	into := da.resourceRoots
	seen := map[*v6.Schema]bool{}
	schPtrType := reflect.TypeOf((*v6.Schema)(nil))
	anyRecursive := false
	var visitSch func(s, res *v6.Schema)
	var visitVal func(fv reflect.Value, res *v6.Schema)
	visitSch = func(s, res *v6.Schema) {
		if s == nil {
			return
		}
		if s.ID != "" {
			res = s
		}
		if seen[s] {
			return
		}
		seen[s] = true
		into[s] = res
		if s.RecursiveAnchor {
			anyRecursive = true
		}
		if s.DynamicAnchor != "" && res != nil {
			da.put(res, s.DynamicAnchor, s)
		}
		rv := reflect.ValueOf(s).Elem()
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			if rt.Field(i).PkgPath != "" {
				continue // unexported — external code cannot reach it
			}
			visitVal(rv.Field(i), res)
		}
	}
	visitVal = func(fv reflect.Value, res *v6.Schema) {
		switch fv.Kind() {
		case reflect.Pointer:
			if fv.Type() == schPtrType && !fv.IsNil() {
				visitSch(fv.Interface().(*v6.Schema), res)
			}
		case reflect.Slice, reflect.Array:
			for j := 0; j < fv.Len(); j++ {
				visitVal(fv.Index(j), res)
			}
		case reflect.Map:
			for _, k := range fv.MapKeys() {
				visitVal(fv.MapIndex(k), res)
			}
		case reflect.Interface:
			if !fv.IsNil() {
				visitVal(fv.Elem(), res)
			}
		}
	}
	visitSch(root, nil)
	return anyRecursive
}

// docAnchor is one $dynamicAnchor found by the raw-document walk: the JSON
// pointer to its enclosing resource root, the pointer to the anchor node itself,
// and the anchor name.
type docAnchor struct{ resPtr, anchorPtr, name string }

// collectDocAnchors walks the raw decoded document as v6's collectResources does
// — descending through every subschema position (including un-$ref'd $defs) and
// tracking $id resource boundaries — and returns the $dynamicAnchor locations it
// found plus the set of resource-root pointers. Pure: it compiles nothing.
func collectDocAnchors(doc any) (anchors []docAnchor, resPtrs map[string]bool) {
	resPtrs = map[string]bool{"": true} // the document root is always a resource

	var walk func(node any, ptr, resPtr string)
	walk = func(node any, ptr, resPtr string) {
		obj, ok := node.(map[string]any)
		if !ok {
			return
		}
		// A node with its own $id opens a new resource rooted at this pointer.
		if id, ok := obj["$id"].(string); ok && id != "" {
			resPtr = ptr
			resPtrs[resPtr] = true
		}
		if a, ok := obj["$dynamicAnchor"].(string); ok && a != "" {
			anchors = append(anchors, docAnchor{resPtr, ptr, a})
		}
		// Descend through every subschema position v6 recognises (union across
		// drafts; a position absent from this node is simply skipped).
		for _, key := range subschemaObjKeys { // value is an object of schemas
			if m, ok := obj[key].(map[string]any); ok {
				for k, v := range m {
					walk(v, ptr+"/"+key+"/"+escapePointer(k), resPtr)
				}
			}
		}
		for _, key := range subschemaArrKeys { // value is an array of schemas
			if arr, ok := obj[key].([]any); ok {
				for i, v := range arr {
					walk(v, ptr+"/"+key+"/"+strconv.Itoa(i), resPtr)
				}
			}
		}
		for _, key := range subschemaOneKeys { // value is a single schema
			if v, ok := obj[key]; ok {
				walk(v, ptr+"/"+key, resPtr)
			}
		}
		// items may be a single schema OR an array of schemas (draft <2020).
		if arr, ok := obj["items"].([]any); ok {
			for i, v := range arr {
				walk(v, ptr+"/items/"+strconv.Itoa(i), resPtr)
			}
		}
	}
	walk(doc, "", "")
	return anchors, resPtrs
}

// bridgeDocAnchors force-compiles each anchor location collectDocAnchors found,
// on the SAME compiler, to obtain the exact compiled *Schema v6 resolves to, and
// records it in da. This adds $dynamicAnchors the exported graph could not reach
// (un-$ref'd $defs). No-op when the document declares no $dynamicAnchor.
func bridgeDocAnchors(doc any, c *v6.Compiler, da *dynAnchors) {
	anchors, resPtrs := collectDocAnchors(doc)
	if len(anchors) == 0 {
		return
	}

	// compileURL#ptr addresses a node; an empty pointer is the document root.
	compileAt := func(ptr string) *v6.Schema {
		url := compileURL
		if ptr != "" {
			url += "#" + ptr
		}
		s, err := c.Compile(url)
		if err != nil {
			// An anchor location that fails isolation-compile (e.g. a node relying
			// on an inherited dialect that reads differently when compiled as its
			// own root) is dropped. Resolution then falls back to the ref's lexical
			// target — spec-correct WHEN no matching anchor is in scope, and safe
			// for a reachable anchor because pass A (collectReachable) has already
			// recorded it. The residual risk is a genuinely-in-scope anchor that
			// ONLY the document walk can reach AND that fails isolation-compile; no
			// suite case hits it (0 fail both drafts), and TestDynamicRef-
			// UnreferencedDefsAnchor pins the reachable un-$ref'd-$defs path.
			return nil
		}
		return s
	}

	root := map[string]*v6.Schema{} // resource-pointer → compiled resource root
	for rp := range resPtrs {
		if s := compileAt(rp); s != nil {
			root[rp] = s
			da.resourceRoots[s] = s
		}
	}
	for _, a := range anchors {
		res, anchor := root[a.resPtr], compileAt(a.anchorPtr)
		if res == nil || anchor == nil {
			continue
		}
		da.put(res, a.name, anchor)
		// The anchor node's resource is its declaring resource; record it so a
		// scope entered at the anchor resolves correctly. Do not overwrite a
		// mapping the exported-graph pass already set for a reachable node.
		if _, ok := da.resourceRoots[anchor]; !ok {
			da.resourceRoots[anchor] = res
		}
	}
}

// subschema position keys v6 recognises, split by value shape (union across
// drafts; walking a key absent from a node is a no-op). "items" is handled
// specially (single-or-array) and is not listed here.
var (
	subschemaObjKeys = []string{"definitions", "$defs", "properties", "patternProperties", "dependencies", "dependentSchemas"}
	subschemaArrKeys = []string{"allOf", "anyOf", "oneOf", "prefixItems"}
	subschemaOneKeys = []string{"not", "additionalProperties", "additionalItems", "propertyNames", "contains", "if", "then", "else", "unevaluatedProperties", "unevaluatedItems", "contentSchema"}
)

// escapePointer escapes a JSON Pointer reference token (RFC 6901): ~ -> ~0,
// / -> ~1. Applied to object keys used as pointer segments.
func escapePointer(tok string) string {
	out := make([]byte, 0, len(tok))
	for i := 0; i < len(tok); i++ {
		switch tok[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, tok[i])
		}
	}
	return string(out)
}

// dynScope is the runtime dynamic-scope stack: the resource roots entered on the
// path from the root schema to the current node, outermost first. Like refStack
// it extends by value (with returns an extended copy), so a child recursion is
// handed the extended scope and siblings never see each other's entries.
//
// A slice is used rather than the chunked stack refStack uses because the scope
// is only pushed on a resource crossing (following a ref into a schema with a
// different resource root), which is rare relative to the per-node ref-cycle
// key, and the outermost-first ordering v6's resolution needs is naturally a
// slice. The backing array is shared on append growth, but with never mutates an
// existing index, so a parent's view is stable across a child's extension.
type dynScope struct {
	roots []*v6.Schema // resource roots, outermost (index 0) to innermost
}

// with returns the scope extended by resource root r. If r is already the
// innermost entry it is not duplicated (staying within a resource does not
// re-push it).
func (d dynScope) with(r *v6.Schema) dynScope {
	if r == nil {
		return d
	}
	if n := len(d.roots); n > 0 && d.roots[n-1] == r {
		return d
	}
	next := make([]*v6.Schema, len(d.roots)+1)
	copy(next, d.roots)
	next[len(d.roots)] = r
	return dynScope{roots: next}
}

// resolveDynamicAnchor mirrors v6's: scan the dynamic scope OUTERMOST-first and
// return the outermost resource that declares name; fall back to the lexical
// target when no scope resource declares it. This is what makes an anchored
// $dynamicRef resolve to the extending schema at the top of the scope rather
// than its lexical target.
func (da *dynAnchors) resolveDynamicAnchor(name string, scope dynScope, fallback *v6.Schema) *v6.Schema {
	sch := fallback
	for _, root := range scope.roots { // outermost first; last match (innermost) does NOT win — outermost does
		if m := da.byResource[root]; m != nil {
			if dsch, ok := m[name]; ok {
				return dsch // first (outermost) match wins, matching v6's scan direction + assignment
			}
		}
	}
	return sch
}

// resolveRecursiveAnchor mirrors v6's: scan the dynamic scope and return the
// OUTERMOST resource whose root carries $recursiveAnchor:true, else the lexical
// fallback. v6 assigns on every match while scanning innermost-first, so the
// outermost match is the final value — we scan outermost-first and take the
// first, which is equivalent.
func (da *dynAnchors) resolveRecursiveAnchor(scope dynScope, fallback *v6.Schema) *v6.Schema {
	for _, root := range scope.roots {
		if root.RecursiveAnchor {
			return root
		}
	}
	return fallback
}

// dref bundles the dynamic-ref resolution state threaded through validation: the
// immutable per-compile anchor index (da, nil when the schema declares no
// dynamic anchors — the common case, in which the whole mechanism is inert) and
// the runtime dynamic scope (extended by value as resources are entered, like
// refStack). Passed by value: da is a shared read-only pointer and scope extends
// copy-on-write, so a child's extension never disturbs a sibling's view.
type dref struct {
	da    *dynAnchors
	scope dynScope
}

// entering returns dr with c's resource pushed onto the scope when c belongs to
// a resource the scope has not yet entered at its innermost position. A node's
// resource is looked up in the reconstructed resourceRoots index; staying within
// the current resource is a no-op (dynScope.with dedups the innermost entry).
// This is called on entry to every validate frame, so the scope tracks exactly
// the resources on the path from the root to the current node — v6's dynamic
// scope, rebuilt on our side.
func (dr dref) entering(c *v6.Schema) dref {
	if dr.da == nil {
		return dr
	}
	if root, ok := dr.da.resourceRoots[c]; ok {
		dr.scope = dr.scope.with(root)
	}
	return dr
}
