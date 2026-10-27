package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

func TestInteractiveMatrix(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	for _, tc := range []struct {
		name         string
		in, out, off bool // stdin tty, stdout tty, --json or --quiet
		env          func(string) string
		want         bool
	}{
		{"terminal", true, true, false, env("TERM", "xterm-256color"), true},
		{"no TERM at all (Windows)", true, true, false, env(), true},
		{"NO_COLOR keeps the view", true, true, false, env("TERM", "xterm", "NO_COLOR", "1"), true},
		{"CI=false", true, true, false, env("CI", "false"), true},
		{"CI=0", true, true, false, env("CI", "0"), true},
		{"--json or --quiet", true, true, true, env(), false},
		{"stdin piped", false, true, false, env(), false},
		{"stdout piped", true, false, false, env(), false},
		{"TERM=dumb", true, true, false, env("TERM", "dumb"), false},
		{"CI=true", true, true, false, env("CI", "true"), false},
		{"CI=1", true, true, false, env("CI", "1"), false},
	} {
		if got := interactive(tc.in, tc.out, tc.env, tc.off); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestColourFollowsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if !Colour() {
		t.Error("colour off with NO_COLOR empty")
	}
	t.Setenv("NO_COLOR", "1")
	if Colour() {
		t.Error("colour on with NO_COLOR=1")
	}
}

// ---- teatest: the model in a real (virtual) program ----------------------------

func waitFor(t *testing.T, tm *teatest.TestModel, s string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(s)) },
		teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
}

func TestProgramShowsNewRowsAtTheTopAndQuitsOnQ(t *testing.T) {
	var n int
	src := &fakeSource{poll: func(map[string]struct{}) (Update, error) {
		n++
		return Update{Rows: []Row{row(fmt.Sprintf("%02d", 20+n), "failed", "js", fmt.Sprintf("https://live.example/%d", n))}}, nil
	}}
	m := New(context.Background(), logsCfg(src, sample()...))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "https://a.example/09")
	waitFor(t, tm, "https://live.example/2") // arrives by polling, with no key pressed
	tm.Send(key("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	final := tm.FinalModel(t).(*Model)
	if final.Reason() != Quit {
		t.Errorf("reason %v", final.Reason())
	}
	if final.rows[0].Key <= "21" {
		t.Errorf("newest row is not on top: %s", final.rows[0].Key)
	}
}

func TestProgramFinishesWhenTheSourceSaysDone(t *testing.T) {
	var calls int
	src := &fakeSource{poll: func(map[string]struct{}) (Update, error) {
		calls++
		return Update{Done: calls >= 3, Header: func(w int, _ time.Time) []string { return []string{"job 3/3"} }}, nil
	}}
	tm := teatest.NewTestModel(t, New(context.Background(), logsCfg(src, sample()...)), teatest.WithInitialTermSize(80, 24))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if r := tm.FinalModel(t).(*Model).Reason(); r != Finished {
		t.Errorf("reason %v", r)
	}
}

func TestProgramKeepsRetryingAfterErrors(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	src := &fakeSource{poll: func(map[string]struct{}) (Update, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls <= 2 {
			return Update{}, errors.New("503 unavailable")
		}
		return Update{Rows: []Row{row("30", "success", "js", "https://back.example/")}}, nil
	}}
	cfg := logsCfg(src, sample()...)
	cfg.Poll = time.Millisecond
	tm := teatest.NewTestModel(t, New(context.Background(), cfg), teatest.WithInitialTermSize(80, 24))
	waitFor(t, tm, "https://back.example/")
	tm.Send(key("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	mu.Lock()
	defer mu.Unlock()
	if calls < 3 {
		t.Errorf("polled %d times", calls)
	}
}

func TestProgramDetailThenQuit(t *testing.T) {
	m := New(context.Background(), logsCfg(&fakeSource{}, sample()...))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	waitFor(t, tm, "https://a.example/09")
	tm.Send(key("enter"))
	waitFor(t, tm, "detail of 09")
	tm.Send(key("esc"))
	tm.Send(key("ctrl+c"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if r := tm.FinalModel(t).(*Model).Reason(); r != Interrupt {
		t.Errorf("reason %v", r)
	}
}

// ---- Run: the terminal is restored however the view ends -----------------------------------

// leaveAlt is the sequence that returns to the normal screen; showCursor is the one that brings the cursor back.
const (
	enterAlt   = "\x1b[?1049h"
	leaveAlt   = "\x1b[?1049l"
	showCursor = "\x1b[?25h"
)

func restored(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, enterAlt) {
		t.Errorf("never entered the alternate screen: %q", out)
	}
	if !strings.Contains(out, leaveAlt) || strings.LastIndex(out, leaveAlt) < strings.LastIndex(out, enterAlt) {
		t.Errorf("did not leave the alternate screen: %q", out)
	}
	if !strings.Contains(out, showCursor) {
		t.Errorf("cursor left hidden: %q", out)
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestRunRestoresTheScreenWhenTheViewEnds(t *testing.T) {
	// Done ends it by itself; the others are driven in the tests below.
	src := &fakeSource{poll: func(map[string]struct{}) (Update, error) {
		time.Sleep(150 * time.Millisecond) // let the first frame out
		return Update{Done: true}, nil
	}}
	var out syncBuf
	r, err := Run(context.Background(), logsCfg(src, sample()...), tea.WithInput(nil), tea.WithOutput(&out), tea.WithWindowSize(80, 24))
	if err != nil || r != Finished {
		t.Fatalf("reason %v, err %v", r, err)
	}
	restored(t, out.String())
}

func TestRunRestoresTheScreenOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &fakeSource{poll: func(map[string]struct{}) (Update, error) {
		time.Sleep(150 * time.Millisecond) // let the first frame out
		cancel()                           // Ctrl-C as the process sees it: SIGINT cancels the command's context
		time.Sleep(50 * time.Millisecond)
		return Update{}, nil
	}}
	var out syncBuf
	_, err := Run(ctx, logsCfg(src, sample()...), tea.WithInput(nil), tea.WithOutput(&out), tea.WithWindowSize(80, 24))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
	restored(t, out.String())
}

// panics blows up on a message, in the program's own loop.
type panics struct{ Model }

func (p *panics) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(pollMsg); ok {
		panic("boom in Update")
	}
	_, cmd := p.Model.Update(msg)
	return p, cmd // keep this model: returning the embedded one would drop the panic
}

func (p *panics) Init() tea.Cmd {
	return func() tea.Msg { time.Sleep(150 * time.Millisecond); return pollMsg{} } // after the first frame
}

func TestRunProgramRestoresTheScreenAfterAPanic(t *testing.T) {
	m := &panics{Model: *New(context.Background(), logsCfg(&fakeSource{}, sample()...))}
	var out syncBuf
	_, err := runProgram(context.Background(), m, tea.WithInput(nil), tea.WithOutput(&out), tea.WithWindowSize(80, 24))
	if err == nil || !strings.Contains(err.Error(), "crashed") || !errors.Is(err, tea.ErrProgramPanic) {
		t.Fatalf("err %v", err)
	}
	restored(t, out.String())
}

func TestRunWithNoColorKeepsAttributesButNoColours(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	src := &fakeSource{poll: func(map[string]struct{}) (Update, error) {
		time.Sleep(150 * time.Millisecond)
		return Update{Done: true}, nil
	}}
	var out syncBuf
	if _, err := Run(context.Background(), logsCfg(src, sample()...), tea.WithInput(nil), tea.WithOutput(&out), tea.WithWindowSize(80, 24)); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "\x1b[1;7m") && !strings.Contains(s, "\x1b[7m") {
		t.Errorf("no reverse video: the selected row would be invisible without colour: %q", s)
	}
	for _, colour := range []string{"\x1b[31m", "\x1b[32m", "\x1b[36m", "38;5;", "38;2;", "\x1b[91m"} {
		if strings.Contains(s, colour) {
			t.Errorf("NO_COLOR but the output has %q", colour)
		}
	}
	if !strings.Contains(s, "> ") {
		t.Error("the selected row has no > marker")
	}
}
