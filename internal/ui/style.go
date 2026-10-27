package ui

import "strings"

// Role is a meaning, not a colour. Callers say "this failed" or "this is the
// one thing to look at"; the terminal's capabilities decide how that looks.
//
// One accent (teal), three signals (green, amber, red) and a muted grey. The
// signals use the terminal's own palette wherever it is the safe choice, so a
// Solarized, Dracula or light theme tunes them for its own background. Only
// the accent and the amber leave the 16-colour palette, and only to colours
// that keep contrast on both a black and a white background.
type Role int

const (
	Accent  Role = iota // the one brand colour: spinner, progress, names to type
	Success             // green
	Warn                // amber
	Danger              // red
	Muted               // secondary text: keys, facts, elapsed time
	Strong              // headings and headlines
)

// sgr is the SGR parameter string for r on a terminal with these caps.
func (c Caps) sgr(r Role) string {
	switch r {
	case Success:
		return "32"
	case Danger:
		return "31"
	case Muted:
		return "2" // faint: relative to the user's own foreground, so it works on any theme
	case Strong:
		return "1"
	case Warn:
		switch c.Color {
		case ColorTrue:
			return "38;2;217;119;6" // amber-600: 3.2:1 on white, 6.5:1 on black
		case Color256:
			return "38;5;172"
		}
		return "33"
	}
	switch c.Color { // Accent
	case ColorTrue:
		if c.Light {
			return "38;2;15;118;110" // teal-700
		}
		return "38;2;13;148;136" // teal-600: 3.7:1 on white, 6:1 on black
	case Color256:
		return "38;5;30"
	}
	return "36"
}

// Glyphs is every symbol the output draws, so a locale or console that cannot
// show them gets ASCII with the same shape.
type Glyphs struct {
	OK, Bad, Warn, Neutral     string // ✓ ✗ ! –
	Bullet, Dot                string // › ·
	Ellipsis, Rule             string // … ─
	BarFull, BarHalf, BarEmpty string // ━ ╸ ─
	Frames                     []string
}

var (
	unicodeGlyphs = Glyphs{
		OK: "✓", Bad: "✗", Warn: "!", Neutral: "–",
		Bullet: "›", Dot: "·", Ellipsis: "…", Rule: "─",
		BarFull: "━", BarHalf: "╸", BarEmpty: "─",
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	}
	asciiGlyphs = Glyphs{
		OK: "+", Bad: "x", Warn: "!", Neutral: "-",
		Bullet: ">", Dot: "-", Ellipsis: "...", Rule: "-",
		BarFull: "#", BarHalf: "", BarEmpty: "-",
		Frames: []string{"|", "/", "-", "\\"},
	}
)

// Glyphs returns the symbol set this UI draws with.
func (u *UI) Glyphs() Glyphs {
	if u != nil && u.caps.Unicode {
		return unicodeGlyphs
	}
	return asciiGlyphs
}

// Paint wraps s in the style for r. It is s unchanged while the UI is off.
func (u *UI) Paint(r Role, s string) string {
	if !u.On() || s == "" {
		return s
	}
	return "\x1b[" + u.caps.sgr(r) + "m" + s + "\x1b[0m"
}

func (u *UI) Bold(s string) string   { return u.Paint(Strong, s) }
func (u *UI) Dim(s string) string    { return u.Paint(Muted, s) }
func (u *UI) Red(s string) string    { return u.Paint(Danger, s) }
func (u *UI) Green(s string) string  { return u.Paint(Success, s) }
func (u *UI) Amber(s string) string  { return u.Paint(Warn, s) }
func (u *UI) Accent(s string) string { return u.Paint(Accent, s) }
func (u *UI) Cyan(s string) string   { return u.Paint(Accent, s) } // the old name for Accent

// Link is s as a clickable hyperlink to url (OSC 8) on a terminal that has
// them. Elsewhere it is the text alone, or "text (url)" when the text is not
// the URL, so the address is never lost. An empty text means the URL itself.
func (u *UI) Link(url, text string) string {
	if text == "" {
		text = url
	}
	switch {
	case !u.On() || url == "":
		return text
	case !u.caps.Links:
		if text == url {
			return text
		}
		return text + " (" + url + ")"
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// Linkify turns each http(s) URL in s into a hyperlink, if the terminal has
// them; with no hyperlinks s is returned untouched.
func (u *UI) Linkify(s string) string {
	if !u.On() || !u.caps.Links || !strings.Contains(s, "http") {
		return s
	}
	var b strings.Builder
	for _, w := range strings.SplitAfter(s, " ") {
		word := strings.TrimRight(w, " ")
		if strings.HasPrefix(word, "https://") || strings.HasPrefix(word, "http://") {
			// trailing punctuation belongs to the sentence, not the address
			tail := ""
			for len(word) > 0 && strings.ContainsRune(".,;:)", rune(word[len(word)-1])) {
				tail = word[len(word)-1:] + tail
				word = word[:len(word)-1]
			}
			b.WriteString(u.Link(word, word) + tail + w[len(strings.TrimRight(w, " ")):])
			continue
		}
		b.WriteString(w)
	}
	return b.String()
}
