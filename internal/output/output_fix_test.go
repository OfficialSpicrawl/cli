package output

import (
	"bytes"
	"strings"
	"testing"
)

// --json used to decode envelopes into float64: 1234567890123456789 became
// 1234567890123456800, and keys were re-sorted. Both must match --jsonl.
func TestOutputRawJSONKeepsNumbersAndKeyOrder(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out}
	if err := p.RawJSON([]byte(`{"z":1234567890123456789,"a":1.50,"s":"<&>"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"z\": 1234567890123456789,\n  \"a\": 1.50,\n  \"s\": \"<&>\"\n}\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}

	out.Reset() // not JSON: passed through untouched
	if err := p.RawJSON([]byte("plain")); err != nil || !strings.HasPrefix(out.String(), "plain") {
		t.Errorf("non-JSON: %q, %v", out.String(), err)
	}
}
