package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
)

// ---- fixtures ----------------------------------------------------------------

var testCols = []Column{
	{Title: "TIME", Width: 8},
	{Title: "STATUS", Width: 9, Status: true},
	{Title: "HTTP", Width: 4, Right: true},
	{Title: "TIER", Width: 7, MinTerm: 60},
	{Title: "CREDITS", Width: 8, Right: true, MinTerm: 100},
	{Title: "MS", Width: 6, Right: true, MinTerm: 72},
	{Title: "URL"},
}

func row(id, status, tier, url string) Row {
	return Row{
		Key: id, Status: status, Tier: tier, Search: url + " " + id + " " + status,
		Cells: []string{"10:00:" + id, status, "200", tier, "1", "120", url},
	}
}

// fakeSource is a Source scripted by the test.
type fakeSource struct {
	mu     sync.Mutex
	poll   func(known map[string]struct{}) (Update, error)
	detail func(key string) (string, error)
	polls  int
}

func (f *fakeSource) Poll(_ context.Context, known map[string]struct{}) (Update, error) {
	f.mu.Lock()
	f.polls++
	f.mu.Unlock()
	if f.poll == nil {
		return Update{}, nil
	}
	return f.poll(known)
}

func (f *fakeSource) Detail(_ context.Context, key string) (string, error) {
	if f.detail == nil {
		return "detail of " + key, nil
	}
	return f.detail(key)
}

func logsCfg(src Source, rows ...Row) Config {
	return Config{
		Title: "spicrawl logs", Noun: "requests", Empty: "no requests yet",
		Columns: testCols, Statuses: []string{"success", "failed", "blocked"},
		BadKey: "e", BadLabel: "errors", BadStatus: func(s string) bool { return s != "success" },
		Source: src, Initial: Update{Rows: rows}, Poll: 10 * time.Millisecond,
		Now: func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) },
	}
}

func sample() []Row {
	return []Row{
		row("09", "success", "fetch", "https://a.example/09"),
		row("08", "failed", "js", "https://b.example/08"),
		row("07", "blocked", "browser", "https://c.example/07"),
		row("06", "success", "js", "https://d.example/06"),
		row("05", "failed", "fetch", "https://e.example/05"),
	}
}

func newLogs(rows ...Row) *Model {
	m := New(context.Background(), logsCfg(&fakeSource{}, rows...))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// press sends keys and returns the last command.
func press(m *Model, keys ...string) (cmd tea.Cmd) {
	for _, k := range keys {
		_, cmd = m.Update(key(k))
	}
	return cmd
}

func keys(m *Model) string {
	var ids []string
	for _, r := range m.shown {
		ids = append(ids, r.Key)
	}
	return strings.Join(ids, ",")
}

func text(m *Model) string { return ansi.Strip(m.View().Content) }

// runCmd runs cmd and returns its message, or nil if it blocks (a timer).
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case m := <-ch:
		return m
	case <-time.After(30 * time.Millisecond):
		return nil
	}
}

// quits reports whether cmd (or a command batched in it) is tea.Quit.
func quits(cmd tea.Cmd) bool {
	switch msg := runCmd(cmd).(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if quits(c) {
				return true
			}
		}
	}
	return false
}

// ---- filtering -----------------------------------------------------------------

func TestFiltersStatusTierErrorsAndSearch(t *testing.T) {
	m := newLogs(sample()...)
	if got := keys(m); got != "09,08,07,06,05" {
		t.Fatalf("all rows: %s", got)
	}

	press(m, "s") // success
	if got := keys(m); got != "09,06" {
		t.Errorf("status=success: %s", got)
	}
	press(m, "s") // failed
	if got := keys(m); got != "08,05" {
		t.Errorf("status=failed: %s", got)
	}
	press(m, "s", "s") // blocked, then back to all
	if got := keys(m); got != "09,08,07,06,05" || m.f.status != "" {
		t.Errorf("status cycle did not wrap to all: %q %s", m.f.status, got)
	}

	press(m, "t") // fetch
	if got := keys(m); got != "09,05" {
		t.Errorf("tier=fetch: %s", got)
	}
	press(m, "t") // js
	if got := keys(m); got != "08,06" {
		t.Errorf("tier=js: %s", got)
	}
	press(m, "c")
	press(m, "e")
	if got := keys(m); got != "08,07,05" {
		t.Errorf("errors only: %s", got)
	}
	press(m, "t", "t", "t") // fetch, js, browser: errors + browser
	if got := keys(m); got != "07" {
		t.Errorf("errors + tier=browser: %s", got)
	}
	press(m, "c")
	if got := keys(m); got != "09,08,07,06,05" || m.f.active() {
		t.Errorf("c did not clear: %s", got)
	}

	press(m, "/", "d", ".", "e")
	if got := keys(m); got != "06" {
		t.Errorf("search d.e (live while typing): %s", got)
	}
	press(m, "enter")
	if m.mode != modeList || m.f.text != "d.e" || keys(m) != "06" {
		t.Errorf("enter keeps the search: mode %v text %q rows %s", m.mode, m.f.text, keys(m))
	}
	press(m, "/", "backspace", "backspace", "backspace", "esc") // esc restores what was there
	if m.f.text != "d.e" || keys(m) != "06" {
		t.Errorf("esc did not restore the search: %q %s", m.f.text, keys(m))
	}
	press(m, "/", "backspace", "backspace", "backspace", "enter")
	if m.f.text != "" || len(m.shown) != 5 {
		t.Errorf("emptied search still filters: %q %s", m.f.text, keys(m))
	}
	press(m, "/", "Z", "Z", "enter")
	if len(m.shown) != 0 || !strings.Contains(text(m), "no requests match the filters (c clears them)") {
		t.Errorf("empty filtered list says:\n%s", text(m))
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	m := newLogs(sample()...)
	press(m, "/", "B", ".", "E", "X", "enter")
	if keys(m) != "08" {
		t.Errorf("got %s", keys(m))
	}
}

// ---- polling and the cursor ------------------------------------------------------

func TestNewRowsGoOnTopAndTheCursorStaysOnItsRow(t *testing.T) {
	m := newLogs(sample()...)
	press(m, "down", "down") // on 07
	m.Update(pollMsg{u: Update{Rows: []Row{row("11", "success", "js", "https://n/11"), row("10", "failed", "js", "https://n/10")}}})
	if got := keys(m); got != "11,10,09,08,07,06,05" {
		t.Fatalf("order after the poll: %s", got)
	}
	if m.shown[m.cur.idx].Key != "07" {
		t.Errorf("the cursor moved to %s", m.shown[m.cur.idx].Key)
	}
	if !strings.Contains(text(m), "+2 new") {
		t.Errorf("status bar does not count the new rows:\n%s", text(m))
	}

	// On the first row the view follows the newest.
	press(m, "g")
	m.Update(pollMsg{u: Update{Rows: []Row{row("12", "success", "js", "https://n/12")}}})
	if m.shown[m.cur.idx].Key != "12" {
		t.Errorf("the cursor did not follow the newest row: %s", m.shown[m.cur.idx].Key)
	}
}

func TestPollDoesNotDuplicateKnownRows(t *testing.T) {
	m := newLogs(sample()...)
	m.Update(pollMsg{u: Update{Rows: []Row{row("10", "success", "js", "u"), row("09", "success", "fetch", "u")}}})
	if got := keys(m); got != "10,09,08,07,06,05" {
		t.Errorf("got %s", got)
	}
}

func TestReplaceSwapsTheTable(t *testing.T) {
	m := newLogs(sample()...)
	m.Update(pollMsg{u: Update{Replace: true, Rows: []Row{row("09", "success", "js", "u"), row("20", "failed", "js", "u")}}})
	if got := keys(m); got != "09,20" || m.added != 1 {
		t.Errorf("got %s, %d new", got, m.added)
	}
}

func TestRowCapDropsTheOldest(t *testing.T) {
	cfg := logsCfg(&fakeSource{}, sample()...)
	cfg.MaxRows = 6
	m := New(context.Background(), cfg)
	m.Update(pollMsg{u: Update{Rows: []Row{row("11", "success", "js", "u"), row("10", "success", "js", "u"), row("12", "success", "js", "u")}}})
	if len(m.rows) != 6 || m.rows[5].Key != "07" {
		t.Errorf("rows %d, last %s", len(m.rows), m.rows[len(m.rows)-1].Key)
	}
	if _, ok := m.seen["05"]; ok {
		t.Error("a dropped row is still remembered as seen, so it could never come back")
	}
}

func TestPollKnownSnapshotIsTheNewestRows(t *testing.T) {
	var got map[string]struct{}
	src := &fakeSource{poll: func(known map[string]struct{}) (Update, error) { got = known; return Update{}, nil }}
	m := New(context.Background(), logsCfg(src, sample()...))
	runCmd(m.fetch())
	if _, ok := got["09"]; !ok || len(got) != 5 {
		t.Errorf("known = %v", got)
	}
}

func TestPausedViewSkipsPolling(t *testing.T) {
	src := &fakeSource{}
	m := New(context.Background(), logsCfg(src, sample()...))
	press(m, "p")
	if !strings.Contains(text(m), "paused") {
		t.Errorf("no paused marker:\n%s", text(m))
	}
	_, cmd := m.Update(tickMsg{gen: m.gen})
	runCmd(cmd)
	if src.polls != 0 {
		t.Error("a paused view polled")
	}
	press(m, "p")
	_, cmd = m.Update(tickMsg{gen: m.gen})
	runCmd(cmd)
	if src.polls != 1 {
		t.Errorf("resumed view polled %d times", src.polls)
	}
	// A stale tick (an old generation) does nothing.
	_, cmd = m.Update(tickMsg{gen: m.gen - 1})
	if cmd != nil {
		t.Error("a stale tick scheduled work")
	}
}

// ---- errors ------------------------------------------------------------------------

type slow struct{ after time.Duration }

func (slow) Error() string               { return "429 slow down" }
func (s slow) RetryAfter() time.Duration { return s.after }

func TestPollErrorShowsOnTheStatusBarAndBacksOff(t *testing.T) {
	m := newLogs(sample()...)
	m.Update(pollMsg{err: errors.New("cannot reach the Spicrawl API: dial tcp: refused")})
	bar := text(m)
	if !strings.Contains(bar, "! cannot reach the Spicrawl API: dial tcp: refused · retrying in ") {
		t.Errorf("no error on the status bar:\n%s", bar)
	}
	if !strings.Contains(bar, "! offline") {
		t.Errorf("title bar does not say offline:\n%s", bar)
	}
	if got := len(m.shown); got != 5 {
		t.Errorf("an error cleared the table (%d rows)", got)
	}

	poll := 2 * time.Second
	want := []time.Duration{2, 4, 8, 16, 30, 30}
	err := errors.New("x")
	for i, w := range want {
		if got := Backoff(poll, i, err); i > 0 && got != w*time.Second {
			t.Errorf("Backoff(failures=%d) = %s, want %ss", i, got, w*time.Second)
		}
	}
	if got := Backoff(poll, 0, nil); got != poll {
		t.Errorf("healthy delay = %s", got)
	}
	if got := Backoff(poll, 3, slow{7 * time.Second}); got != 7*time.Second {
		t.Errorf("Retry-After ignored: %s", got)
	}
	if got := Backoff(poll, 3, slow{time.Hour}); got != time.Minute {
		t.Errorf("Retry-After not capped: %s", got)
	}

	// The next good poll clears the error and the offline marker.
	m.Update(pollMsg{u: Update{Rows: []Row{row("10", "success", "js", "u")}}})
	if m.err != nil || m.failures != 0 || strings.Contains(text(m), "offline") {
		t.Errorf("did not recover: err=%v failures=%d", m.err, m.failures)
	}
}

func TestErrorTextNeverShowsEngineNamesOrControlCodes(t *testing.T) {
	m := newLogs(sample()...)
	m.Update(pollMsg{err: errors.New("moli died\x1b]0;pwn\x07 in plain-chrome")})
	out := m.View().Content
	if strings.Contains(strings.ToLower(out), "moli") || strings.Contains(strings.ToLower(out), "chrome") {
		t.Errorf("engine name on screen:\n%s", out)
	}
	if strings.Contains(out, "\x1b]") || strings.Contains(out, "\x07") {
		t.Errorf("control code from an error reached the screen: %q", out)
	}
}

func TestFirstPollFailureIsRetried(t *testing.T) {
	cfg := logsCfg(&fakeSource{})
	cfg.InitialErr = errors.New("503")
	m := New(context.Background(), cfg)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.err == nil || m.failures != 1 || !strings.Contains(text(m), "waiting for the API") {
		t.Errorf("start with an error: err=%v\n%s", m.err, text(m))
	}
}

// ---- detail ---------------------------------------------------------------------------

func TestEnterOpensDetailAndEscReturns(t *testing.T) {
	src := &fakeSource{detail: func(k string) (string, error) {
		return "id:  " + k + "\nstatus: ok\x1b]0;pwned\x07\x1b[31m red \x1b[0m\x1b[2J", nil
	}}
	m := New(context.Background(), logsCfg(src, sample()...))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	press(m, "down")
	cmd := press(m, "enter")
	if m.mode != modeDetail || !strings.Contains(text(m), "loading") {
		t.Fatalf("no loading state:\n%s", text(m))
	}
	msg := runCmd(cmd)
	m.Update(msg)
	out := m.View().Content
	if !strings.Contains(ansi.Strip(out), "id:  08") {
		t.Errorf("detail of the selected row missing:\n%s", ansi.Strip(out))
	}
	if strings.Contains(out, "\x1b]0") || strings.Contains(out, "\x07") || strings.Contains(out, "\x1b[2J") {
		t.Errorf("control codes from the page reached the screen: %q", out)
	}
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("colour (SGR) was stripped from the page: %q", out)
	}

	// q and Enter go back; neither quits from the detail.
	if quits(press(m, "q")) || m.mode != modeList {
		t.Errorf("q in detail: mode %v", m.mode)
	}
	press(m, "enter")
	press(m, "esc")
	if m.mode != modeList {
		t.Error("esc did not go back")
	}
}

func TestDetailErrorIsShownInPlace(t *testing.T) {
	src := &fakeSource{detail: func(string) (string, error) { return "", errors.New("404 not found") }}
	m := New(context.Background(), logsCfg(src, sample()...))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(runCmd(press(m, "enter")))
	if !strings.Contains(text(m), "! 404 not found") {
		t.Errorf("detail error missing:\n%s", text(m))
	}
}

func TestStaleDetailIsIgnored(t *testing.T) {
	m := New(context.Background(), logsCfg(&fakeSource{}, sample()...))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	press(m, "enter")
	press(m, "esc")
	m.Update(detailMsg{key: "09", text: "late"})
	if m.detailText != "" {
		t.Error("a detail that arrived after the user left was kept")
	}
}

// ---- quitting and finishing -------------------------------------------------------------

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		m := newLogs(sample()...)
		if !quits(press(m, k)) || m.Reason() != Quit {
			t.Errorf("%s: reason %v", k, m.Reason())
		}
	}
	m := newLogs(sample()...)
	if !quits(press(m, "ctrl+c")) || m.Reason() != Interrupt {
		t.Errorf("ctrl+c: reason %v", m.Reason())
	}
	// Ctrl-C quits from the search box and the detail too.
	for _, setup := range [][]string{{"/"}, {"enter"}} {
		m := newLogs(sample()...)
		press(m, setup...)
		if !quits(press(m, "ctrl+c")) || m.Reason() != Interrupt {
			t.Errorf("ctrl+c after %v: reason %v", setup, m.Reason())
		}
	}
	// q is a letter in the search box.
	m = newLogs(sample()...)
	if quits(press(m, "/", "q")) || m.f.text != "q" {
		t.Error("q in the search box quit or was lost")
	}
}

func TestDoneQuitsButNeverFromUnderADetailPage(t *testing.T) {
	m := newLogs(sample()...)
	press(m, "enter")
	_, cmd := m.Update(pollMsg{u: Update{Done: true}})
	if quits(cmd) {
		t.Fatal("the view closed under a reader")
	}
	if !quits(press(m, "esc")) || m.Reason() != Finished {
		t.Errorf("leaving the page did not finish the view: reason %v", m.Reason())
	}

	m = newLogs(sample()...)
	_, cmd = m.Update(pollMsg{u: Update{Done: true}})
	if !quits(cmd) || m.Reason() != Finished {
		t.Errorf("done in the list: reason %v", m.Reason())
	}

	// Typing a search holds the exit as well.
	m = newLogs(sample()...)
	press(m, "/")
	_, cmd = m.Update(pollMsg{u: Update{Done: true}})
	if quits(cmd) {
		t.Fatal("the view closed while typing")
	}
	if !quits(press(m, "enter")) {
		t.Error("did not finish after the search was kept")
	}
}

func TestDeadlineEndsTheViewWithTimeout(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	cfg := logsCfg(&fakeSource{}, sample()...)
	cfg.Now = func() time.Time { return now }
	cfg.Deadline = now.Add(time.Minute)
	m := New(context.Background(), cfg)
	if quits(m.finish()) {
		t.Fatal("ended before the deadline")
	}
	now = now.Add(2 * time.Minute)
	if !quits(m.finish()) || m.Reason() != Timeout {
		t.Errorf("reason %v", m.Reason())
	}
}

// ---- layout ----------------------------------------------------------------------------------

func TestViewIsExactlyTheTerminalSize(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {40, 10}, {64, 12}, {200, 50}} {
		m := New(context.Background(), logsCfg(&fakeSource{}, sample()...))
		m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		lines := strings.Split(text(m), "\n")
		if len(lines) != sz[1] {
			t.Errorf("%dx%d: %d lines", sz[0], sz[1], len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > sz[0] {
				t.Errorf("%dx%d: line %d is %d wide: %q", sz[0], sz[1], i, w, l)
			}
		}
	}
}

func TestTooSmallTerminalSaysSo(t *testing.T) {
	m := newLogs(sample()...)
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 6})
	if out := text(m); !strings.Contains(out, "terminal too small\n30x6, need 40x10\nq quits") {
		t.Errorf("got %q", out)
	}
	if !quits(press(m, "q")) {
		t.Error("q must still quit a too-small terminal")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24}) // growing it again brings the table back
	if !strings.Contains(text(m), "https://a.example/09") {
		t.Errorf("did not recover after resize:\n%s", text(m))
	}
}

func TestResizeKeepsTheCursorVisible(t *testing.T) {
	var rows []Row
	for i := 40; i > 0; i-- {
		rows = append(rows, row(fmt.Sprintf("%02d", i), "success", "js", fmt.Sprintf("https://x/%02d", i)))
	}
	m := newLogs(rows...)
	press(m, "G") // the last row
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	if !strings.Contains(text(m), "https://x/01") {
		t.Errorf("selected row scrolled out of sight after a resize:\n%s", text(m))
	}
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	if !strings.Contains(text(m), "> ") || !strings.Contains(text(m), "x/01") {
		t.Errorf("selected row lost after a second resize:\n%s", text(m))
	}
}

func TestNarrowTerminalDropsColumnsNotTheURL(t *testing.T) {
	m := newLogs(sample()...)
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 24})
	out := text(m)
	if strings.Contains(out, "TIER") || strings.Contains(out, "CREDITS") {
		t.Errorf("columns that do not fit are still shown:\n%s", out)
	}
	if !strings.Contains(out, "https://a.example/09") {
		t.Errorf("the URL does not fit at 50 columns:\n%s", out)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	if !strings.Contains(text(m), "CREDITS") {
		t.Errorf("wide terminal lacks the credits column:\n%s", text(m))
	}
}

func TestCellsFollowTheirColumnsWhenOneIsHidden(t *testing.T) {
	m := newLogs(sample()...) // 80 wide: CREDITS (MinTerm 100) is hidden
	line := strings.Split(text(m), "\n")[3]
	if !strings.Contains(line, "fetch") || !strings.Contains(line, "120") || !strings.Contains(line, "https://a.example/09") || !strings.HasPrefix(line, "> ") {
		t.Errorf("cells shifted into the wrong columns: %q", line)
	}
}

func TestControlCharactersInCellsAreNeutralised(t *testing.T) {
	r := row("01", "success", "js", "https://evil\x1b[2J\x1b]0;x\x07.example/\n")
	m := newLogs(r)
	out := m.View().Content
	if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b]0") || strings.Contains(out, "\x07") {
		t.Errorf("a control sequence in a row reached the screen: %q", out)
	}
}

// ---- snapshots (golden text of the screen) ---------------------------------------------------

func TestSnapshotLogs80x24(t *testing.T) {
	m := newLogs(sample()...)
	press(m, "down")
	golden.RequireEqual(t, text(m))
}

func TestSnapshotLogsFilteredSearch(t *testing.T) {
	m := newLogs(sample()...)
	press(m, "e", "/", "e", "x", "a", "m", "p", "l", "e", "/", "0", "8")
	golden.RequireEqual(t, text(m))
}

func TestSnapshotLogsError(t *testing.T) {
	m := newLogs(sample()...)
	m.Update(pollMsg{err: errors.New("503 ERR::INTERNAL::UNAVAILABLE")})
	golden.RequireEqual(t, text(m))
}

func TestSnapshotLogsWide(t *testing.T) {
	m := newLogs(sample()...)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	golden.RequireEqual(t, text(m))
}

func TestSnapshotLogsDetail(t *testing.T) {
	m := newLogs(sample()...)
	press(m, "enter")
	m.Update(detailMsg{key: "09", text: "✗ blocked  https://a.example/09\n\nid:           09\nstatus:       blocked\nengine:       js\nerror detail: the site served a bot challenge, a long line that has to wrap because it is wider than the screen is"})
	golden.RequireEqual(t, text(m))
}

func TestSnapshotNoRows(t *testing.T) {
	m := newLogs()
	golden.RequireEqual(t, text(m))
}
