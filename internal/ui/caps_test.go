package ui

import "testing"

func envOf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestColorLevel(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want Color
	}{
		{"nothing says 16", nil, Color16},
		{"TERM=xterm is 16", []string{"TERM", "xterm"}, Color16},
		{"TERM=linux is 16", []string{"TERM", "linux"}, Color16},
		{"256color", []string{"TERM", "xterm-256color"}, Color256},
		{"screen-256color", []string{"TERM", "screen-256color"}, Color256},
		{"Apple Terminal is 256 even with a plain TERM", []string{"TERM", "xterm", "TERM_PROGRAM", "Apple_Terminal"}, Color256},
		{"COLORTERM=truecolor", []string{"TERM", "xterm-256color", "COLORTERM", "truecolor"}, ColorTrue},
		{"COLORTERM=24bit", []string{"COLORTERM", "24bit"}, ColorTrue},
		{"COLORTERM=yes is no promise of 24 bit", []string{"TERM", "xterm", "COLORTERM", "yes"}, Color16},
		{"TERM=xterm-direct", []string{"TERM", "xterm-direct"}, ColorTrue},
		{"kitty", []string{"TERM", "xterm-kitty"}, ColorTrue},
		{"Windows Terminal", []string{"WT_SESSION", "x"}, ColorTrue},
		{"iTerm", []string{"TERM_PROGRAM", "iTerm.app", "TERM", "xterm-256color"}, ColorTrue},
		{"new VTE", []string{"VTE_VERSION", "6800"}, ColorTrue},
		{"old VTE", []string{"VTE_VERSION", "3000", "TERM", "xterm-256color"}, Color256},
	}
	for _, c := range cases {
		if got := colorLevel(envOf(c.env...)); got != c.want {
			t.Errorf("%s: colorLevel = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestUnicodeNeedsAUTF8Locale(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		goos string
		want bool
	}{
		{"UTF-8 LANG", []string{"LANG", "en_US.UTF-8"}, "linux", true},
		{"utf8 spelling", []string{"LANG", "en_GB.utf8"}, "linux", true},
		{"LANG=C", []string{"LANG", "C"}, "linux", false},
		{"POSIX", []string{"LC_ALL", "POSIX"}, "linux", false},
		{"latin-1", []string{"LANG", "de_DE.ISO-8859-1"}, "linux", false},
		{"LC_ALL beats LANG", []string{"LC_ALL", "C", "LANG", "en_US.UTF-8"}, "linux", false},
		{"LC_ALL beats LANG the other way", []string{"LC_ALL", "C.UTF-8", "LANG", "C"}, "linux", true},
		{"LC_CTYPE beats LANG", []string{"LC_CTYPE", "en_US.UTF-8", "LANG", "C"}, "linux", true},
		{"no locale at all is assumed modern", nil, "darwin", true},
		{"the kernel console has no such glyphs", []string{"TERM", "linux", "LANG", "en_US.UTF-8"}, "linux", false},
		{"Windows Terminal", []string{"WT_SESSION", "x"}, "windows", true},
		{"legacy Windows console, whatever LANG says", []string{"LANG", "en_US.UTF-8"}, "windows", false},
	}
	for _, c := range cases {
		if got := unicodeOK(envOf(c.env...), c.goos); got != c.want {
			t.Errorf("%s: unicodeOK = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLinksOnlyWhereKnown(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want bool
	}{
		{"unknown terminal", []string{"TERM", "xterm-256color"}, false},
		{"Apple Terminal does not", []string{"TERM_PROGRAM", "Apple_Terminal"}, false},
		{"iTerm", []string{"TERM_PROGRAM", "iTerm.app"}, true},
		{"VS Code", []string{"TERM_PROGRAM", "vscode"}, true},
		{"Windows Terminal", []string{"WT_SESSION", "x"}, true},
		{"kitty", []string{"KITTY_WINDOW_ID", "1"}, true},
		{"VTE 5000", []string{"VTE_VERSION", "5000"}, true},
		{"VTE 4999", []string{"VTE_VERSION", "4999"}, false},
		{"inside tmux, even in iTerm", []string{"TERM_PROGRAM", "iTerm.app", "TMUX", "/tmp/tmux"}, false},
		{"FORCE_HYPERLINK=1", []string{"FORCE_HYPERLINK", "1"}, true},
		{"FORCE_HYPERLINK=1 inside tmux", []string{"FORCE_HYPERLINK", "1", "TMUX", "x"}, true},
		{"FORCE_HYPERLINK=0 beats the terminal", []string{"FORCE_HYPERLINK", "0", "TERM_PROGRAM", "iTerm.app"}, false},
	}
	for _, c := range cases {
		if got := linksOK(envOf(c.env...)); got != c.want {
			t.Errorf("%s: linksOK = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLightBackground(t *testing.T) {
	for v, want := range map[string]bool{"": false, "15;0": false, "0;15": true, "0;7": true, "15;default;0": false, "0;default;15": true, "garbage": false, "0;8": false} {
		if got := lightBackground(envOf("COLORFGBG", v)); got != want {
			t.Errorf("COLORFGBG=%q: light = %v, want %v", v, got, want)
		}
	}
}

func TestDetectComposes(t *testing.T) {
	c := detect(envOf("TERM", "xterm-256color", "COLORTERM", "truecolor", "LANG", "C", "TERM_PROGRAM", "WezTerm"), "linux")
	if c.Color != ColorTrue || c.Unicode || !c.Links {
		t.Errorf("detect = %+v", c)
	}
	if z := (Caps{}); z.Color != Color16 || z.Unicode || z.Links {
		t.Errorf("the zero Caps must be the most conservative terminal: %+v", z)
	}
}
