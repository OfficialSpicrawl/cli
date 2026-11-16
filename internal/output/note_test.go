package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestNote(t *testing.T) {
	var out, errb bytes.Buffer
	p := &Printer{Out: &out, Err: &errb}
	p.Note("use %s", "--render")
	if errb.String() != "note: use --render\n" || out.Len() != 0 {
		t.Errorf("human: stderr %q stdout %q", errb.String(), out.String())
	}

	errb.Reset()
	p.JSON = true
	p.Note("anything")
	if errb.Len() != 0 || out.Len() != 0 {
		t.Errorf("json mode must be silent: %q %q", errb.String(), out.String())
	}

	// same sanitising as Warn: an internal engine name becomes its tier
	p.JSON = false
	p.Note("the Moli engine failed")
	var wb bytes.Buffer
	(&Printer{Out: &out, Err: &wb}).Warn("the Moli engine failed")
	if strings.Contains(errb.String(), "Moli") || strings.TrimPrefix(errb.String(), "note: ") != strings.TrimPrefix(wb.String(), "warning: ") {
		t.Errorf("note %q vs warning %q", errb.String(), wb.String())
	}
}
