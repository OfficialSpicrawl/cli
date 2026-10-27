package cmd

// In JSON mode stderr is the problem document of a failure and nothing else
// (DESIGN.md, "Errors are the API's problem document"), so that
//
//	spicrawl ... --json 2>err.json; jq . err.json
//
// always parses. This runs every golden case that is not a human-mode one and
// holds its stderr to that: empty, or exactly one JSON object. A case that
// fails (non-zero exit) must have the object.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// jsonStderrKnownGaps are cases that still break the rule, with the reason.
// The test fails when one starts to pass, so the entry gets deleted.
var jsonStderrKnownGaps = map[string]string{}

// jsonStderrHumanByDesign are cases that run in JSON mode by the harness but
// leave it on purpose, so stderr is prose for a person.
var jsonStderrHumanByDesign = map[string]string{
	"browser/url_quiet_prints_bare_url": "browser url -q prints the bare URL even when piped (only --json overrides)",
}

// jsonStderrProblem reports why stderr is not "empty or exactly one JSON object".
func jsonStderrProblem(stderr string, exit int) error {
	if strings.TrimSpace(stderr) == "" {
		if exit != 0 {
			return fmt.Errorf("exit %d with an empty stderr: want the problem document", exit)
		}
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(stderr))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("stderr does not start with a JSON object: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("stderr has more than one JSON document (or trailing text)")
	}
	return nil
}

func TestJSONStderrIsOneProblemDocument(t *testing.T) {
	for _, c := range goldenCases {
		if _, ok := jsonStderrHumanByDesign[c.name]; ok || c.human {
			continue
		}
		t.Run(strings.ReplaceAll(c.name, "/", "."), func(t *testing.T) {
			r, _ := goldenExec(t, c, nil)
			err := jsonStderrProblem(r.stderr, r.code)
			if gap, ok := jsonStderrKnownGaps[c.name]; ok {
				if err == nil {
					t.Errorf("this case is clean now: delete it from jsonStderrKnownGaps (%s)", gap)
				}
				return
			}
			if err != nil {
				t.Errorf("%v\nstderr: %q", err, r.stderr)
			}
		})
	}
}

func TestJSONStderrCheck(t *testing.T) {
	for _, c := range []struct {
		stderr string
		exit   int
		ok     bool
	}{
		{"", 0, true},
		{"{\"error\":\"x\",\"exit_code\":2}\n", 2, true},
		{"{\n  \"code\": \"ERR::AUTH::INVALID_KEY\"\n}\n", 3, true},
		{"", 3, false}, // failed without the document
		{"warning: x\n{\"error\":\"x\"}\n", 2, false}, // prose first
		{"{\"error\":\"x\"}\nhint: y\n", 2, false},    // prose after
		{"{\"a\":1}\n{\"b\":2}\n", 2, false},          // two documents
		{"warning: this output is live\n", 0, false},  // prose on success
		{"[1]\n", 2, false},                           // not an object
	} {
		if err := jsonStderrProblem(c.stderr, c.exit); (err == nil) != c.ok {
			t.Errorf("%q (exit %d): got %v, want ok=%v", c.stderr, c.exit, err, c.ok)
		}
	}
}
