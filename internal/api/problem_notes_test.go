package api

import (
	"encoding/json"
	"testing"
)

func TestProblemNotesAndHint(t *testing.T) {
	for _, c := range []struct {
		name, body, hint string
		notes            int
	}{
		{"hint and notes: hint wins", `{"notes":["n1","n2"],"diagnostics":{"hint":"h"}}`, "h", 2},
		{"notes only: first note", `{"notes":["n1","n2"]}`, "n1", 2},
		{"empty hint falls to note", `{"notes":["n1"],"diagnostics":{"hint":""}}`, "n1", 1},
		{"neither", `{"diagnostics":{}}`, "", 0},
		{"no diagnostics, no notes", `{}`, "", 0},
	} {
		var p Problem
		if err := json.Unmarshal([]byte(c.body), &p); err != nil {
			t.Fatal(err)
		}
		if len(p.Notes) != c.notes || p.Hint() != c.hint {
			t.Errorf("%s: notes %v hint %q", c.name, p.Notes, p.Hint())
		}
	}
}
