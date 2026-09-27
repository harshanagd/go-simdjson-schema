# go-simdjson-schema

High-performance JSON Schema validator for Go — a drop-in replacement for
[santhosh-tekuri/jsonschema](https://github.com/santhosh-tekuri/jsonschema) v6,
evaluating directly over [go-simdjson](https://github.com/harshanagd/go-simdjson)'s
SIMD-parsed, zero-copy tape.

It reuses v6's compiler — so the API is the same and migration is an import
swap — but validates against the parser's tape instead of walking a
`map[string]any`, deleting the materialisation cost that dominates the
reference implementation.

> Status: early. The API compatibility and architecture are settled; draft
> coverage is earned against the official test suite.

## Compliance

Measured against the official [JSON-Schema-Test-Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite)
for **draft2020-12** — the 46 keyword files plus the scored `optional/format/`
tree (a few formats needing full ECMA-262 regex or IDNA are skipped, the same
set v6 skips). *Unsupported* cases return `ErrNotImplemented` and are never
scored as a wrong verdict — zero *fail* is the hard bar.

| Metric | Cases |
|---|---:|
| ✅ pass | 1631 |
| ❌ fail | 0 |
| ⏳ unsupported | 39 |

Implemented (green): the type-agnostic assertions (`type`, `const`, `enum`), the
object / array / string / number keyword families, the in-place applicators, the
object / array applicator subschemas (`properties`, `items`, `contains`, …), the
references (`$ref`, `$recursiveRef`, single-context `$dynamicRef`, and remote
`$ref` via a caller-supplied resource/loader), the unevaluated applicators
(`unevaluatedProperties`, `unevaluatedItems`), and asserting `format` (opt-in via
`WithFormatAssertion`, reusing v6's RFC validators).

| Section | Keywords | Status |
|---|---|---|
| Type-agnostic | `type`, `const`, `enum` | ✅ implemented |
| Object | `minProperties`, `maxProperties`, `required`, `dependentRequired` | ✅ implemented |
| Array | `minItems`, `maxItems`, `uniqueItems` | ✅ implemented |
| String | `minLength`, `maxLength`, `pattern` | ✅ implemented |
| Number | `minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `multipleOf` | ✅ implemented |
| Applicators | `allOf`, `anyOf`, `oneOf`, `not`, `if`/`then`/`else` | ✅ implemented |
| Object/array applicators | `properties`, `patternProperties`, `additionalProperties`, `propertyNames`, `dependentSchemas`, `items`, `prefixItems`, `contains`, `minContains`, `maxContains` | ✅ implemented |
| References | `$ref`, `$recursiveRef`, single-context `$dynamicRef` | ✅ implemented |
| References (remote) | remote `$ref` via `Compile` + `WithResource`/`WithLoader` | ✅ implemented |
| References (dynamic) | multi-context `$dynamicRef`/`$recursiveRef` (runtime-scope resolution) | ⏳ `ErrNotImplemented` |
| Unevaluated | `unevaluatedProperties`, `unevaluatedItems` | ✅ implemented |
| `format` | asserting `format` (opt-in `WithFormatAssertion`) | ✅ implemented |
| `content*` | content vocabulary | ⏳ gated (`ErrNotImplemented`) |

## Why

`santhosh-tekuri/jsonschema` v6 first parses JSON into `map[string]any`, then
walks that tree. Building the tree is the elephant: on the large corpus v6's
`Validate` alone costs ~78K–201K allocations *on top of* whatever
`json.Unmarshal` spent materialising the `any`. A tape evaluator deletes the
tree outright — every value already has a stable position on the SIMD-parsed
tape, so validation walks the tape in place.

## Design in one line

Reuse v6's compiler (its `$ref`/`$id`/anchor/vocabulary/draft-detection/
meta-schema plumbing is thousands of lines of hard, correct spec work) and
write a **new evaluator** against the go-simdjson tape. This is option **B**,
built so the instruction-stream compiler (option C, Blaze-style) is reachable
incrementally.

## Correctness first

Get the [JSON-Schema-Test-Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite)
green before optimising anything. A fast, wrong validator is worthless — the
Blaze paper found several popular validators failing 200+ suite cases.

## Benchmarks

`BenchmarkValidate` compares this validator's `Validate` against
santhosh-tekuri/jsonschema v6's, one sub-benchmark per keyword file, over the
subset of suite cases this evaluator supports (cases we gate are skipped so the
comparison is like-for-like). Both validators walk the same decoded `any`
instance, so this measures **evaluator vs evaluator on a shared backend** — the
go-simdjson tape backend is a later seam, so this does not yet include the
parse-side (materialisation) win described above.

```bash
# ours vs v6, per keyword, with allocations
GOWORK=off go test -run '^$' -bench BenchmarkValidate -benchmem .

# a single keyword pair
GOWORK=off go test -run '^$' -bench 'BenchmarkValidate/type/' -benchmem .
```

Each keyword yields two lines, `<keyword>/ours` and `<keyword>/v6`, comparable
directly or via `benchstat`.

### Results

Measured on the passing subset (Go 1.26, `-benchmem`, `ns/op` and `allocs/op`;
lower is better). Across every keyword ours is faster and allocates less —
typically **~1.5–3× faster** on subschema-recursing keywords and **~4–15×
faster with zero allocations** on the leaf assertions v6 still allocates an
error/annotation scaffold for.

| Keyword | ours ns | v6 ns | ours allocs | v6 allocs |
|---|---:|---:|---:|---:|
| boolean_schema | 9.7 | 105.4 | 0 | 5 |
| content | 10.3 | 99.3 | 0 | 3 |
| format | 12.4 | 87.7 | 0 | 3 |
| pattern | 29.7 | 141.1 | 0 | 3 |
| maxItems | 36.6 | 132.3 | 0 | 5 |
| maxLength | 37.3 | 136.0 | 0 | 5 |
| minItems | 38.4 | 132.9 | 0 | 5 |
| minLength | 50.2 | 135.5 | 0 | 6 |
| minProperties | 51.5 | 133.4 | 0 | 4 |
| maxProperties | 56.6 | 152.7 | 0 | 5 |
| type | 58.0 | 195.0 | 2 | 7 |
| not | 67.9 | 235.9 | 1 | 8 |
| required | 81.5 | 229.3 | 1 | 6 |
| dependentRequired | 113.7 | 219.2 | 1 | 5 |
| prefixItems | 123.3 | 228.5 | 2 | 7 |
| uniqueItems | 135.5 | 268.6 | 3 | 6 |
| ref | 139.1 | 223.7 | 2 | 7 |
| enum | 150.5 | 281.7 | 5 | 9 |
| const | 151.7 | 289.1 | 5 | 10 |
| propertyNames | 177.6 | 270.5 | 3 | 8 |
| exclusiveMinimum | 195.3 | 351.7 | 8 | 14 |
| exclusiveMaximum | 196.4 | 325.4 | 8 | 14 |
| anyOf | 195.4 | 394.2 | 5 | 12 |
| minimum | 199.6 | 308.4 | 7 | 12 |
| default | 213.9 | 303.2 | 5 | 8 |
| if-then-else | 248.2 | 461.5 | 8 | 14 |
| dependentSchemas | 257.5 | 381.8 | 5 | 10 |
| maximum | 259.1 | 342.5 | 8 | 13 |
| properties | 268.5 | 340.8 | 5 | 9 |
| additionalProperties | 291.3 | 347.3 | 4 | 7 |
| oneOf | 332.6 | 546.0 | 6 | 14 |
| allOf | 340.5 | 704.4 | 10 | 25 |
| items | 348.5 | 499.8 | 8 | 13 |
| patternProperties | 368.7 | 530.2 | 5 | 12 |
| multipleOf | 374.7 | 550.1 | 13 | 19 |
| minContains | 581.6 | 848.0 | 21 | 27 |
| maxContains | 564.6 | 885.8 | 23 | 28 |
| contains | 619.0 | 1111.0 | 23 | 38 |

Two tiers are visible. **Leaf assertions** (`boolean_schema`, `format`,
`content`, `pattern`, the min/max bounds) settle a verdict from v6's compiled
fields without touching a subschema, so ours does zero allocations where v6
still stands up its per-call error/annotation scaffold — the 4–15× gap. The
**subschema-recursing keywords** (`allOf`/`contains`/`items`/…) still allocate,
but far less than v6, because our evaluator carries no validation-context,
scope-chain, or structured-error-tree machinery on the hot path. As sections 7
(`$ref` → scope) and 9 (`unevaluated*` → annotation tracker) land, the
recursing tier will give some of this margin back; the leaf tier and the
(not-yet-measured) tape parse-side win are the durable advantages.

## Running the tests

The conformance suite is vendored as a git submodule. After cloning, initialise
it:

```bash
git submodule update --init --recursive
```

The compliance harness walks the official
[JSON-Schema-Test-Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite)
and prints a per-keyword scoreboard, sorted worst-first so the next thing to
work on is at the top. It classifies every case as **pass** (verdict matches),
**fail** (wrong verdict — always surfaced), or **unsupported**
(`ErrNotImplemented` — not counted against the build while the evaluator is a
stub). A file drops off the worklist once it reaches `fail=0 unsupported=0`.

```bash
# Run the harness (scoreboard prints via t.Log, so -v is required to see it).
GOWORK=off go test . -v

# Pick a different draft directory (default: draft2020-12).
GOWORK=off go test . -v -draft draft7

# Turn the scoreboard into a gate: fail on any wrong or unsupported case.
GOWORK=off go test . -v -strict
```

`GOWORK=off` bypasses any enclosing Go workspace so the module is tested
directly. If an uncached dependency fetch fails because the module proxy is
unreachable, add `GOPROXY=direct GOSUMDB=off` to pull from the VCS host.

## Licence

Apache-2.0. The compiler is derived from santhosh-tekuri/jsonschema v6
(Apache-2.0); modifications are noted per the licence. Any optimisation ideas
taken from the Blaze paper are implemented from the paper, never from its
AGPL-3.0 source.
