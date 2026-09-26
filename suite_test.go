package jsonschema

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The compliance harness walks the official JSON-Schema-Test-Suite and reports
// how many cases the validator gets right, grouped by keyword file. While the
// tape evaluator is a stub, cases that return ErrNotImplemented are counted as
// "unsupported" and do NOT fail the run — the harness is a scoreboard you drive
// toward green, not a gate yet. Flip -strict once a draft is meant to be
// complete, and any wrong verdict or unsupported case fails the test.

var (
	strict      = flag.Bool("strict", false, "fail the test on any wrong or unsupported case")
	draftFilter = flag.String("draft", "draft2020-12", "which suite draft directory to run")
)

const suiteRoot = "testdata/JSON-Schema-Test-Suite/tests"

// suiteCase is one {schema, data, valid} triple.
type suiteCase struct {
	Description string          `json:"description"`
	Data        json.RawMessage `json:"data"`
	Valid       bool            `json:"valid"`
}

// suiteGroup is one entry in a suite file: a schema plus its cases.
type suiteGroup struct {
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Tests       []suiteCase     `json:"tests"`
}

type score struct{ pass, fail, unsupported int }

func TestSuiteCompliance(t *testing.T) {
	dir := filepath.Join(suiteRoot, *draftFilter)
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("suite not present at %s (run: git submodule update --init --recursive): %v", dir, err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no suite files under %s", dir)
	}
	sort.Strings(files)

	total := score{}
	perFile := map[string]score{}

	for _, file := range files {
		name := filepath.Base(file)
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var groups []suiteGroup
		if err := json.Unmarshal(raw, &groups); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		fs := score{}
		for _, g := range groups {
			var schemaDoc any
			if err := json.Unmarshal(g.Schema, &schemaDoc); err != nil {
				t.Fatalf("%s / %q: schema decode: %v", name, g.Description, err)
			}
			sch, err := Compile(schemaDoc)
			if err != nil {
				// A compile failure counts as unsupported for now.
				fs.unsupported += len(g.Tests)
				continue
			}
			for _, tc := range g.Tests {
				var instance any
				if err := json.Unmarshal(tc.Data, &instance); err != nil {
					t.Fatalf("%s / %q / %q: data decode: %v", name, g.Description, tc.Description, err)
				}
				verr := sch.Validate(instance)
				switch {
				case errors.Is(verr, ErrNotImplemented):
					fs.unsupported++
				case (verr == nil) == tc.Valid:
					fs.pass++
				default:
					fs.fail++
					if testing.Verbose() {
						t.Logf("FAIL %s :: %s :: %s (want valid=%v, got err=%v)",
							name, g.Description, tc.Description, tc.Valid, verr)
					}
				}
			}
		}
		perFile[name] = fs
		total.pass += fs.pass
		total.fail += fs.fail
		total.unsupported += fs.unsupported
	}

	// Scoreboard, sorted by most-failing then most-unsupported so the next thing
	// to work on is at the top.
	names := make([]string, 0, len(perFile))
	for n := range perFile {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := perFile[names[i]], perFile[names[j]]
		if a.fail != b.fail {
			return a.fail > b.fail
		}
		if a.unsupported != b.unsupported {
			return a.unsupported > b.unsupported
		}
		return names[i] < names[j]
	})

	t.Logf("=== %s compliance: %d pass, %d fail, %d unsupported (%d files) ===",
		*draftFilter, total.pass, total.fail, total.unsupported, len(files))
	for _, n := range names {
		s := perFile[n]
		if s.fail == 0 && s.unsupported == 0 {
			continue // fully green — omit from the worklist
		}
		t.Logf("  %-28s pass=%-4d fail=%-4d unsupported=%-4d", n, s.pass, s.fail, s.unsupported)
	}

	if *strict && (total.fail > 0 || total.unsupported > 0) {
		t.Fatalf("strict: %d failing, %d unsupported", total.fail, total.unsupported)
	}
	if total.fail > 0 && !*strict {
		// A wrong verdict is always worth surfacing, even in scoreboard mode,
		// but as a soft signal — the run stays green so CI tracks progress.
		t.Logf("note: %d cases produced a WRONG verdict (not ErrNotImplemented) — investigate", total.fail)
	}
}
