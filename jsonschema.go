// Package jsonschema is a JSON Schema validator backed by the go-simdjson tape.
//
// It is a drop-in replacement for github.com/santhosh-tekuri/jsonschema v6: the
// compile-then-validate API is the same, but validation walks the SIMD-parsed
// tape instead of a map[string]any. See .kiro/specs/jsonschema-validator for the
// design.
//
// Compilation reuses v6's compiler (its $ref/$id/anchor/vocabulary/draft-
// detection plumbing is thousands of lines of correct spec work — design.md
// option B). What is new here is the evaluator: it reads the exported fields of
// v6's compiled *jsonschema.Schema and validates against them. This first slice
// covers the type-agnostic assertions and the object/array/string/number
// keyword families (sections 1-5); any schema whose compiled form carries an
// applicator, $ref, or unevaluated* keyword returns ErrNotImplemented so an
// instance is never partially validated then silently passed.
package jsonschema

import (
	"errors"
	"fmt"

	v6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// ErrNotImplemented is returned for schema features the evaluator does not yet
// support (applicators, $ref, unevaluated*, dynamic refs). The compliance
// harness counts it as "unsupported".
var ErrNotImplemented = errors.New("jsonschema: schema feature not implemented")

// Schema is a compiled JSON Schema. It wraps v6's compiled schema (the source of
// truth for the schema's shape) and the evaluator reads its exported fields. It
// will grow to lower v6's tree into the tape evaluator's instruction stream
// (design.md option B->C).
type Schema struct {
	c *v6.Schema
}

// compileURL is the synthetic base URI given to an in-memory schema document.
// v6 requires every resource to have a URL for $id/$ref base resolution; a
// caller compiling a bare document has none, so we supply a stable placeholder.
const compileURL = "mem://schema"

// URLLoader loads a JSON Schema document from an absolute URL, for resolving a
// $ref to a resource not supplied in-memory. It mirrors v6's URLLoader: the
// caller controls all fetching (there is no built-in network loader), so remote
// resolution is opt-in and side-effect-free by default.
type URLLoader interface {
	// Load returns the decoded JSON document (map[string]any, bool, or a
	// json.Number-using decode) for the given absolute url.
	Load(url string) (any, error)
}

// Option configures a Compile call. Options mirror v6's compiler-configuration
// surface for reference resolution.
type Option func(*options)

type resource struct {
	url string
	doc any
}

type options struct {
	resources []resource // eager pre-seeded docs, in call order
	loader    URLLoader  // lazy resolver for cache misses
}

// WithResource pre-registers a schema document under an absolute url, so a $ref
// to that url resolves against the supplied doc without any load. Mirrors v6's
// Compiler.AddResource. Use it when the referenced documents are known and
// finite (e.g. a fixed set of remotes). Multiple WithResource options may be
// passed; registering the same url twice is an error at compile time.
func WithResource(url string, doc any) Option {
	return func(o *options) {
		o.resources = append(o.resources, resource{url, doc})
	}
}

// WithLoader sets a URLLoader consulted when a $ref names a url that was not
// pre-registered with WithResource and is not already cached. Mirrors v6's
// Compiler.UseLoader. Resolution order per url is: any WithResource doc (v6's
// document cache), then v6's embedded metaschemas, then this loader. Loaded docs
// are cached and their own refs resolved transitively.
func WithLoader(loader URLLoader) Option {
	return func(o *options) { o.loader = loader }
}

// Compile turns a decoded JSON Schema document (map[string]any, bool, or the
// output of encoding/json with UseNumber) into a *Schema, using v6's compiler.
// Reference resolution to other documents is configured with WithResource
// (eager) and WithLoader (lazy); with neither, only the in-memory doc and v6's
// embedded metaschemas are resolvable.
func Compile(doc any, opts ...Option) (*Schema, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	c := v6.NewCompiler()
	if o.loader != nil {
		c.UseLoader(v6Loader{o.loader})
	}
	for _, r := range o.resources {
		if r.url == compileURL {
			return nil, fmt.Errorf("jsonschema: WithResource url %q is reserved for the compiled document", compileURL)
		}
		if err := c.AddResource(r.url, r.doc); err != nil {
			return nil, fmt.Errorf("jsonschema: add resource %q: %w", r.url, err)
		}
	}
	if err := c.AddResource(compileURL, doc); err != nil {
		return nil, fmt.Errorf("jsonschema: add resource: %w", err)
	}
	sch, err := c.Compile(compileURL)
	if err != nil {
		return nil, fmt.Errorf("jsonschema: compile: %w", err)
	}
	return &Schema{c: sch}, nil
}

// v6Loader adapts our public URLLoader to v6's identical interface, keeping v6
// out of our public signature (the drop-in surface is ours, not a re-export).
type v6Loader struct{ l URLLoader }

func (a v6Loader) Load(url string) (any, error) { return a.l.Load(url) }

// usesUnimplemented reports the first schema feature outside sections 1-5 that
// the compiled schema relies on, or "" if the schema is fully within the
// implemented surface. It is read from v6's compiled form: a populated field is
// a keyword the author wrote. Reference, applicator, and unevaluated* fields all
// gate to ErrNotImplemented; the fields sections 1-5 handle are ignored here.
func usesUnimplemented(s *v6.Schema) string {
	switch {
	// Section 1: type-agnostic assertions.
	// Format is populated by v6 ONLY when it will assert (draft-07 and earlier
	// by default; 2019/2020 when the metaschema requires the vocab). A populated
	// Format therefore means "v6 asserts this" — gate it rather than silently
	// skip it, or a pre-2019 {"format":...} schema would pass invalid input.
	case s.Format != nil:
		return "format"
	// Content vocabulary: annotation-only unless AssertContent() was set on the
	// compiler. Our Compile never sets it, so these are nil today — but gate them
	// so the "any populated assertion field is gated" invariant holds regardless
	// of how the schema was compiled.
	case s.ContentSchema != nil, s.ContentEncoding != nil, s.ContentMediaType != nil:
		return "content"
	// Section 7: references. $ref/$recursiveRef are resolved by v6 at compile
	// time into direct *Schema pointers, so the evaluator follows the pointer
	// (validate.go) — not gated. TWO exceptions need v6's unexported dynamic
	// scope machinery and stay gated:
	//   - $recursiveRef whose static target carries $recursiveAnchor: true — v6
	//     re-resolves it through the runtime scope (resolveRecursiveAnchor), so
	//     following the static pointer would resolve nested nodes to the base
	//     (lax) schema instead of the extending (strict) one — a false PASS.
	//   - $dynamicRef carrying a dynamic Anchor — resolution differs by runtime
	//     scope via the unexported dynamicAnchors map. This deliberately also
	//     gates the rare shape whose anchor matches no $dynamicAnchor in scope
	//     (which spec-behaves like a plain $ref): gating it is conservative — it
	//     reports unsupported rather than risk a wrong verdict, never a false PASS.
	// The anchor-free case of each is a plain lexical reference and is followed
	// statically (correct, matches v6).
	case s.RecursiveRef != nil && s.RecursiveRef.RecursiveAnchor:
		return "$recursiveRef"
	case s.DynamicRef != nil && s.DynamicRef.Anchor != "":
		return "$dynamicRef"
	// Extensions carry custom vocabularies we do not yet run.
	case len(s.Extensions) > 0:
		return "extension vocabulary"
	}
	return ""
}
