package jsonschema

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	v6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// benchPair is one compiled schema plus a decoded instance, ready to validate.
// Both validators share the same decoded instance so the comparison is fair.
type benchPair struct {
	ours     *Schema
	theirs   *v6.Schema
	instance any
}

// collectBenchPairs walks one keyword file and returns every (schema, instance)
// pair that OUR validator fully supports — i.e. Validate returns a verdict, not
// ErrNotImplemented. Cases we gate are skipped so the comparison is like-for-like
// on the green subset. The schema is compiled once here, outside the timed loop.
func collectBenchPairs(tb testing.TB, file string) []benchPair {
	tb.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		tb.Fatalf("%s: %v", file, err)
	}
	var groups []suiteGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		tb.Fatalf("%s: %v", file, err)
	}

	var pairs []benchPair
	for _, g := range groups {
		schemaDoc, err := decodeSuiteJSON(g.Schema)
		if err != nil {
			continue
		}
		ours, err := Compile(schemaDoc)
		if err != nil {
			continue
		}
		for _, tc := range g.Tests {
			inst, err := decodeSuiteJSON(tc.Data)
			if err != nil {
				continue
			}
			// Only keep cases our evaluator actually supports.
			if errors.Is(ours.Validate(inst), ErrNotImplemented) {
				continue
			}
			pairs = append(pairs, benchPair{ours: ours, theirs: ours.c, instance: inst})
		}
	}
	return pairs
}

// BenchmarkValidate compares our Validate against santhosh-tekuri/jsonschema v6's
// Validate, one sub-benchmark per keyword file, over the cases our evaluator
// supports. Both walk the same decoded any instance (the go-simdjson tape backend
// is a later seam), so this measures evaluator-vs-evaluator on a shared backend —
// not yet the tape advantage.
//
// Run: go test -bench BenchmarkValidate -benchmem .
func BenchmarkValidate(b *testing.B) {
	dir := filepath.Join(suiteRoot, *draftFilter)
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		b.Skipf("suite not present at %s (run: git submodule update --init --recursive)", dir)
	}
	sort.Strings(files)

	for _, file := range files {
		name := filepath.Base(file)
		keyword := name[:len(name)-len(".json")]
		pairs := collectBenchPairs(b, file)
		if len(pairs) == 0 {
			continue // nothing we support in this file
		}

		b.Run(keyword+"/ours", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				p := pairs[i%len(pairs)]
				_ = p.ours.Validate(p.instance)
			}
		})
		b.Run(keyword+"/v6", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				p := pairs[i%len(pairs)]
				_ = p.theirs.Validate(p.instance)
			}
		})
	}
}
