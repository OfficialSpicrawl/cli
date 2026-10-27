package ui

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Color is how many colours the terminal can show. The zero value is the
// basic 16, which every terminal that passes Live has.
type Color int

const (
	Color16   Color = iota // the terminal's own palette: follows the user's theme
	Color256               // xterm 256-colour cube
	ColorTrue              // 24-bit
)

// Caps is what the terminal can draw. Detect fills it from the environment;
// the zero value is the most conservative terminal: 16 colours, ASCII glyphs,
// no hyperlinks.
type Caps struct {
	Color   Color
	Unicode bool // the locale is UTF-8 and the console has the glyphs
	Links   bool // OSC 8 hyperlinks are understood
	Light   bool // the background is light (COLORFGBG says so); dark or unknown otherwise
	Width   int  // fixed column count, for tests; 0 asks the terminal
}

// Detect reads the terminal's capabilities from the environment. It does not
// decide whether the output is live (that is Live); it only says what a live
// terminal can do.
func Detect() Caps { return detect(os.Getenv, runtime.GOOS) }

func detect(getenv func(string) string, goos string) Caps {
	return Caps{
		Color:   colorLevel(getenv),
		Unicode: unicodeOK(getenv, goos),
		Links:   linksOK(getenv),
		Light:   lightBackground(getenv),
	}
}

func colorLevel(getenv func(string) string) Color {
	term := strings.ToLower(getenv("TERM"))
	ct := strings.ToLower(getenv("COLORTERM"))
	prog := getenv("TERM_PROGRAM")
	switch {
	case ct == "truecolor" || ct == "24bit",
		strings.Contains(term, "truecolor"), strings.Contains(term, "24bit"), strings.Contains(term, "direct"),
		term == "xterm-kitty", term == "alacritty", strings.HasPrefix(term, "foot"),
		getenv("WT_SESSION") != "", getenv("KITTY_WINDOW_ID") != "",
		prog == "iTerm.app", prog == "vscode", prog == "WezTerm", prog == "ghostty", prog == "Hyper",
		vteVersion(getenv) >= 3600:
		return ColorTrue
	case strings.Contains(term, "256"), prog == "Apple_Terminal":
		return Color256
	}
	return Color16
}

// vteVersion is $VTE_VERSION as a number (GNOME Terminal and friends), or 0.
func vteVersion(getenv func(string) string) int {
	n, _ := strconv.Atoi(getenv("VTE_VERSION"))
	return n
}

// unicodeOK is true when the glyphs (✓ ✗ ⠋ ━) will not turn into boxes or
// mojibake: the first of LC_ALL, LC_CTYPE and LANG that is set must name UTF-8
// (C and POSIX do not). With no locale at all a modern terminal is assumed,
// except the kernel console, which has no such glyphs. Windows Terminal draws
// them; the legacy console, with its raster fonts, does not.
func unicodeOK(getenv func(string) string, goos string) bool {
	if goos == "windows" {
		return getenv("WT_SESSION") != ""
	}
	if getenv("TERM") == "linux" {
		return false
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := strings.ToLower(getenv(k)); v != "" {
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return true
}

// linksOK is true for terminals known to understand OSC 8 hyperlinks. An
// unknown terminal gets plain text, because one that does not understand the
// sequence may print its payload. FORCE_HYPERLINK=1/0 overrides the guess.
// tmux and screen are not trusted: what they pass through varies by version.
func linksOK(getenv func(string) string) bool {
	if v := getenv("FORCE_HYPERLINK"); v != "" {
		return v != "0" && strings.ToLower(v) != "false"
	}
	if getenv("TMUX") != "" || getenv("STY") != "" {
		return false
	}
	term := strings.ToLower(getenv("TERM"))
	switch getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "vscode", "ghostty", "Hyper":
		return true
	}
	switch {
	case getenv("WT_SESSION") != "", getenv("KITTY_WINDOW_ID") != "", getenv("KONSOLE_VERSION") != "",
		getenv("DOMTERM") != "", vteVersion(getenv) >= 5000,
		term == "xterm-kitty", term == "alacritty", strings.HasPrefix(term, "foot"):
		return true
	}
	return false
}

// lightBackground reads $COLORFGBG ("15;0", or "15;default;0"), which rxvt,
// Konsole and some others set to the foreground;background palette indexes.
// Index 7 and 15 are the light greys and whites.
func lightBackground(getenv func(string) string) bool {
	parts := strings.Split(getenv("COLORFGBG"), ";")
	bg, err := strconv.Atoi(parts[len(parts)-1])
	return err == nil && (bg == 7 || bg == 15)
}
