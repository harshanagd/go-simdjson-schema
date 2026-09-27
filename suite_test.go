package jsonschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dlclark/regexp2"
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

// formatSkip lists optional/format files excluded from scoring rather than
// counted as fails — the same set v6's own harness skips.
//   - ecmascript-regex.json: its sole case asserts that \a is INVALID ECMA-262.
//     dlclark/regexp2 (the engine WithRegexpEngine wires, and v6's own opt-in)
//     accepts \a, so it cannot pass this file — v6 skips it for the same reason,
//     despite shipping the regexp2 opt-in. The engine still closes real ECMA-262
//     *pattern* constructs (\c, lookahead, …) that Go RE2 rejects, which is what
//     it is for; this one assertion-of-invalidity is beyond it.
//   - idn-email / idn-hostname: need IDNA/Unicode machinery beyond the built-in
//     validators.
var formatSkip = map[string]struct{}{
	"ecmascript-regex.json": {},
	"idn-email.json":        {},
	"idn-hostname.json":     {},
}

// suiteRemotesDir holds the suite's remote schema fixtures. Every $ref to a
// remote in the suite uses the http://localhost:1234/ prefix, which suiteRemotes
// maps onto this directory.
const suiteRemotesDir = "testdata/JSON-Schema-Test-Suite/remotes"

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

// decodeSuiteJSON decodes a suite schema or instance with UseNumber so that the
// integer/number distinction (1 vs 1.0) survives — the evaluator relies on it.
func decodeSuiteJSON(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

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

	// The optional/format/ tree is scored with format assertion enabled (the
	// cases are written to be run that way), mirroring v6's own harness. A few
	// files exercise formats out of scope for a Go RE2 / stdlib backend — the
	// same set v6 skips — so they are excluded rather than counted as fails.
	formatFiles, err := filepath.Glob(filepath.Join(dir, "optional", "format", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(formatFiles)

	total := score{}
	perFile := map[string]score{}

	// scoreFile walks one suite file and folds its result into the scoreboard.
	// assertFormat adds WithFormatAssertion for the optional/format tree.
	scoreFile := func(file string, assertFormat bool) {
		name := filepath.Base(file)
		if assertFormat {
			if _, skip := formatSkip[name]; skip {
				return
			}
			name = "format/" + name
		}
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
			schemaDoc, err := decodeSuiteJSON(g.Schema)
			if err != nil {
				t.Fatalf("%s / %q: schema decode: %v", name, g.Description, err)
			}
			opts := []Option{WithLoader(suiteRemotes(suiteRemotesDir))}
			if assertFormat {
				opts = append(opts, WithFormatAssertion(), WithRegexpEngine(dlclarkCompile))
			}
			sch, err := Compile(schemaDoc, opts...)
			if err != nil {
				// A compile failure counts as unsupported for now.
				fs.unsupported += len(g.Tests)
				continue
			}
			for _, tc := range g.Tests {
				instance, err := decodeSuiteJSON(tc.Data)
				if err != nil {
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

	for _, file := range files {
		scoreFile(file, false)
	}
	for _, file := range formatFiles {
		scoreFile(file, true)
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
		*draftFilter, total.pass, total.fail, total.unsupported, len(perFile))
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

// suiteRemotes resolves the suite's remote $ref fixtures. Every remote in the
// suite is addressed as http://localhost:1234/<path>; this loader maps that
// prefix onto the on-disk remotes directory it wraps. It implements URLLoader,
// so the harness exercises the real WithLoader public API rather than a shortcut.
type suiteRemotes string

func (rl suiteRemotes) Load(url string) (any, error) {
	rem, ok := strings.CutPrefix(url, "http://localhost:1234/")
	if !ok {
		return nil, fmt.Errorf("suiteRemotes: unexpected remote url %q", url)
	}
	raw, err := os.ReadFile(filepath.Join(string(rl), filepath.FromSlash(rem)))
	if err != nil {
		return nil, err
	}
	return decodeSuiteJSON(raw)
}

// dlclarkRegexp adapts a dlclark/regexp2 regexp to our Regexp interface. Like
// v6, regexp2 stays a test-only dependency (the library ships the Go RE2 default
// and exposes WithRegexpEngine so a caller supplies ECMA-262 themselves).
type dlclarkRegexp regexp2.Regexp

func (re *dlclarkRegexp) MatchString(s string) bool {
	matched, err := (*regexp2.Regexp)(re).MatchString(s)
	return err == nil && matched
}

func (re *dlclarkRegexp) String() string { return (*regexp2.Regexp)(re).String() }

// dlclarkCompile is a RegexpEngine over dlclark/regexp2 with the ECMAScript
// flag, giving the ECMA-262 semantics the optional/format ecmascript-regex cases
// (and pattern/patternProperties) require. Mirrors v6's example engine.
func dlclarkCompile(pattern string) (Regexp, error) {
	re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*dlclarkRegexp)(re), nil
}
