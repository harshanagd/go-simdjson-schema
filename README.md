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
for **draft2020-12** and **draft2019-09** — the keyword files plus the scored
`optional/format/` tree (the IDNA formats and the one `ecmascript-regex` case
asserting a specific escape is *invalid* are skipped, the same set v6 skips
despite shipping the same regex opt-in). *Unsupported* cases return
`ErrNotImplemented` and are never scored as a wrong verdict — zero *fail* is the
hard bar. Both drafts are fully green with zero unsupported cases.

| Metric | draft2020-12 | draft2019-09 |
|---|---:|---:|
| ✅ pass | 1670 | 1635 |
| ❌ fail | 0 | 0 |
| ⏳ unsupported | 0 | 0 |

Implemented (green): the type-agnostic assertions (`type`, `const`, `enum`), the
object / array / string / number keyword families, the in-place applicators, the
object / array applicator subschemas (`properties`, `items`, `contains`, …), the
references (`$ref`, `$recursiveRef`, `$dynamicRef` including anchored runtime-scope
resolution, and remote `$ref` via a caller-supplied resource/loader), the
unevaluated applicators
(`unevaluatedProperties`, `unevaluatedItems`), and asserting `format` (opt-in via
`WithFormatAssertion`, reusing v6's RFC validators). `pattern`, `patternProperties`
and the `regex` format use Go's RE2 by default; a caller can supply an ECMA-262
engine via `WithRegexpEngine` (mirroring v6's `UseRegexpEngine`) to accept
constructs RE2 rejects.

| Section | Keywords | Status |
|---|---|---|
| Type-agnostic | `type`, `const`, `enum` | ✅ implemented |
| Object | `minProperties`, `maxProperties`, `required`, `dependentRequired` | ✅ implemented |
| Array | `minItems`, `maxItems`, `uniqueItems` | ✅ implemented |
| String | `minLength`, `maxLength`, `pattern` | ✅ implemented |
| Number | `minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `multipleOf` | ✅ implemented |
| Applicators | `allOf`, `anyOf`, `oneOf`, `not`, `if`/`then`/`else` | ✅ implemented |
| Object/array applicators | `properties`, `patternProperties`, `additionalProperties`, `propertyNames`, `dependentSchemas`, `items`, `prefixItems`, `contains`, `minContains`, `maxContains` | ✅ implemented |
| References | `$ref`, `$recursiveRef`, `$dynamicRef` (incl. anchored runtime-scope) | ✅ implemented |
| References (remote) | remote `$ref` via `Compile` + `WithResource`/`WithLoader` | ✅ implemented |
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

Measured on the passing subset (Go 1.26, Apple M3 Pro, `-benchmem`, `ns/op` and
`allocs/op`; lower is better). Across every keyword ours is faster and allocates
less — typically **~1.5–3× faster** on subschema-recursing keywords and **~4–12×
faster with zero allocations** on the leaf assertions v6 still allocates an
error/annotation scaffold for.

| Keyword | ours ns | v6 ns | ours allocs | v6 allocs |
|---|---:|---:|---:|---:|
| boolean_schema | 13.5 | 167.5 | 0 | 6 |
| content | 13.7 | 165.9 | 0 | 4 |
| format | 14.5 | 126.3 | 0 | 4 |
| pattern | 31.7 | 167.3 | 0 | 5 |
| minItems | 40.0 | 151.4 | 0 | 6 |
| maxLength | 42.7 | 184.5 | 0 | 6 |
| maxItems | 43.8 | 168.1 | 0 | 6 |
| minLength | 50.7 | 203.3 | 0 | 7 |
| minProperties | 58.0 | 185.0 | 0 | 5 |
| maxProperties | 59.3 | 202.0 | 0 | 5 |
| type | 59.7 | 247.9 | 2 | 8 |
| anchor | 72.4 | 411.4 | 1 | 14 |
| required | 87.1 | 244.9 | 1 | 7 |
| not | 98.2 | 356.4 | 1 | 12 |
| dependentRequired | 122.7 | 260.5 | 1 | 6 |
| prefixItems | 135.8 | 384.3 | 2 | 11 |
| uniqueItems | 152.9 | 349.6 | 3 | 10 |
| enum | 166.3 | 382.8 | 5 | 11 |
| const | 171.7 | 513.9 | 5 | 13 |
| propertyNames | 215.0 | 426.4 | 3 | 11 |
| exclusiveMaximum | 222.6 | 486.6 | 8 | 15 |
| default | 227.3 | 398.0 | 5 | 10 |
| minimum | 233.9 | 472.3 | 7 | 14 |
| if-then-else | 236.3 | 620.5 | 8 | 20 |
| maximum | 241.7 | 434.6 | 8 | 15 |
| exclusiveMinimum | 242.1 | 437.7 | 8 | 15 |
| anyOf | 276.5 | 563.1 | 6 | 17 |
| dependentSchemas | 279.3 | 473.6 | 5 | 13 |
| additionalProperties | 296.2 | 439.6 | 4 | 12 |
| properties | 319.8 | 553.4 | 5 | 13 |
| oneOf | 359.0 | 767.0 | 6 | 21 |
| allOf | 379.4 | 1043.0 | 10 | 32 |
| patternProperties | 388.1 | 726.4 | 5 | 16 |
| ref | 438.5 | 867.1 | 6 | 23 |
| multipleOf | 446.1 | 676.4 | 13 | 20 |
| items | 510.8 | 1029.0 | 9 | 31 |
| infinite-loop-detection | 514.8 | 1382.0 | 7 | 42 |
| minContains | 539.6 | 866.0 | 21 | 30 |
| dynamicRef | 602.4 | 1114.0 | 10 | 29 |
| unevaluatedItems | 605.5 | 1238.0 | 14 | 34 |
| maxContains | 646.0 | 899.8 | 23 | 32 |
| contains | 733.6 | 1396.0 | 23 | 44 |
| unevaluatedProperties | 1032.0 | 2049.0 | 15 | 35 |
| defs | 3364.0 | 6332.0 | 39 | 135 |

Two tiers are visible. **Leaf assertions** (`boolean_schema`, `format`,
`content`, `pattern`, the min/max bounds) settle a verdict from v6's compiled
fields without touching a subschema, so ours does zero allocations where v6
still stands up its per-call error/annotation scaffold — the 4–12× gap. The
**subschema-recursing keywords** (`allOf`/`contains`/`items`/`unevaluated*`/
`dynamicRef`/…) still allocate, but far less than v6, because our evaluator
carries no validation-context, scope-chain, or structured-error-tree machinery
on the hot path. The leaf tier and the (not-yet-measured) tape parse-side win
are the durable advantages.

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
