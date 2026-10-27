package cmd

// Helpers for the live-view tests: a fake terminal for the full-screen path.

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// fakeTerminal forces the full-screen path on and gives the view a screen
// (everything it writes) and a keyboard. keys writes to the keyboard once the
// run has started; closing happens when the run ends.
type fakeTerminal struct {
	Screen *lockedBuf
	kb     *io.PipeWriter
}

func newFakeTerminal(t *testing.T) *fakeTerminal {
	t.Helper()
	pr, pw := io.Pipe()
	ft := &fakeTerminal{Screen: &lockedBuf{}, kb: pw}
	prevI, prevO := watchInteractive, tuiOptions
	watchInteractive = func() bool { return true }
	tuiOptions = []tea.ProgramOption{tea.WithInput(pr), tea.WithOutput(ft.Screen), tea.WithWindowSize(80, 24)}
	t.Cleanup(func() {
		watchInteractive, tuiOptions = prevI, prevO
		pw.Close()
		pr.Close()
	})
	return ft
}

// Press types s on the keyboard.
func (f *fakeTerminal) Press(s string) { _, _ = io.WriteString(f.kb, s) }

// Wait blocks until the screen has shown want, or the test fails.
func (f *fakeTerminal) Wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(f.Screen.String(), want) {
		if time.Now().After(deadline) {
			// Not Fatalf: this runs on the script's goroutine. Ctrl-C ends the run.
			t.Errorf("the screen never showed %q:\n%s", want, ansi.ReplaceAllString(f.Screen.String(), ""))
			f.Press("\x03")
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Left reports whether the view entered the alternate screen and left it again
// with the cursor shown, which is what "restored" means.
func (f *fakeTerminal) Left() (entered, restored bool) {
	s := f.Screen.String()
	in, out := strings.LastIndex(s, "\x1b[?1049h"), strings.LastIndex(s, "\x1b[?1049l")
	return in >= 0, in >= 0 && out > in && strings.LastIndex(s, "\x1b[?25h") > in
}

// runWatch runs the CLI in human mode on the fake terminal while script drives it.
func runWatch(t *testing.T, ft *fakeTerminal, baseURL string, script func(), args ...string) (stdoutS, stderrS string, code int) {
	t.Helper()
	if script != nil {
		go script()
	}
	return runHuman(t, baseURL, false, args...)
}
