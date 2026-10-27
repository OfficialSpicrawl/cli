package output

import (
	"bytes"
	"testing"
)

// In JSON mode stderr is the problem document or empty, so Warn and Info write
// nothing there; in human mode they are unchanged (Warn survives --quiet).
func TestWarnAndInfoByMode(t *testing.T) {
	for _, c := range []struct {
		name        string
		json, quiet bool
		wantWarn    string
		wantInfo    string
	}{
		{"json", true, false, "", ""},
		{"json quiet", true, true, "", ""},
		{"human", false, false, "warning: w 1\n", "i 2\n"},
		{"human quiet", false, true, "warning: w 1\n", ""},
	} {
		var warn, info bytes.Buffer
		(&Printer{JSON: c.json, Quiet: c.quiet, Err: &warn}).Warn("w %d", 1)
		(&Printer{JSON: c.json, Quiet: c.quiet, Err: &info}).Info("i %d", 2)
		if warn.String() != c.wantWarn || info.String() != c.wantInfo {
			t.Errorf("%s: warn %q info %q, want %q and %q", c.name, warn.String(), info.String(), c.wantWarn, c.wantInfo)
		}
	}
}
