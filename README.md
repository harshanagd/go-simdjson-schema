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
> coverage is earned against the official test suite and is not yet claimed
> here.

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
