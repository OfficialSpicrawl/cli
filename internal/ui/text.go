package ui

import (
	"strings"
	"unicode/utf8"
)

// Width is how many terminal cells s takes: escape sequences (colour, OSC 8
// links) take none, East Asian wide characters and emoji take two, combining
// marks none. Tables, wrapping and the spinner line all measure with it.
func Width(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i += escLen(s[i:])
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		n += runeWidth(r)
		i += size
	}
	return n
}

// escLen is the length of the escape sequence at the start of s: a CSI
// (ESC [ ... final byte) or an OSC (ESC ] ... BEL or ESC \). Anything else is
// two bytes.
func escLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
	case ']':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
	default:
		return 2
	}
	return len(s)
}

// StripANSI removes colour, cursor and hyperlink escape sequences.
func StripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i += escLen(s[i:])
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// runeWidth is the cell width of one rune. ponytail: the tables cover CJK,
// Hangul, fullwidth forms, emoji and the common combining marks, not all of
// UAX #11; an exotic script may be off by a cell. Swap in x/text/width if a
// reader of such a script complains.
func runeWidth(r rune) int {
	switch {
	case r < 0x20, r >= 0x7f && r < 0xa0:
		return 0
	case r < 0x300:
		return 1
	}
	for _, t := range zeroWidth {
		if r >= t[0] && r <= t[1] {
			return 0
		}
	}
	for _, t := range wide {
		if r >= t[0] && r <= t[1] {
			return 2
		}
	}
	return 1
}

var zeroWidth = [][2]rune{
	{0x0300, 0x036f}, {0x1ab0, 0x1aff}, {0x1dc0, 0x1dff}, {0x200b, 0x200f}, {0x2028, 0x202e},
	{0x2060, 0x2064}, {0x20d0, 0x20ff}, {0xfe00, 0xfe0f}, {0xfe20, 0xfe2f}, {0xfeff, 0xfeff},
}

var wide = [][2]rune{
	{0x1100, 0x115f}, {0x231a, 0x231b}, {0x2e80, 0x303e}, {0x3041, 0x33ff}, {0x3400, 0x4dbf},
	{0x4e00, 0x9fff}, {0xa000, 0xa4cf}, {0xac00, 0xd7a3}, {0xf900, 0xfaff}, {0xfe30, 0xfe6f},
	{0xff00, 0xff60}, {0xffe0, 0xffe6}, {0x1f300, 0x1f64f}, {0x1f680, 0x1f6ff}, {0x1f900, 0x1faff},
	{0x20000, 0x3fffd},
}

// Truncate shortens plain text s to n cells, ending in ell when it cuts.
func Truncate(s string, n int, ell string) string {
	if Width(s) <= n {
		return s
	}
	n -= Width(ell)
	if n < 0 {
		n = 0
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runeWidth(r)
		if used+w > n {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + ell
}

// Wrap breaks plain text into lines of at most width cells at spaces. Existing
// newlines are kept, and so is each line's own indentation (every wrapped
// piece of it gets the same). A word wider than the line (a URL, an id) stays
// whole on a line of its own: the terminal soft-wraps it, which keeps it
// copyable. A width under 1 means unknown and returns the text as it is.
func Wrap(s string, width int) []string {
	if width < 1 {
		return strings.Split(s, "\n")
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		indent := para[:len(para)-len(strings.TrimLeft(para, " \t"))]
		var cur strings.Builder
		cur.WriteString(indent)
		w, started := Width(indent), false
		for _, word := range strings.Fields(para) {
			ww := Width(word)
			if started && w+1+ww > width {
				lines = append(lines, cur.String())
				cur.Reset()
				cur.WriteString(indent)
				w, started = Width(indent), false
			}
			if started {
				cur.WriteByte(' ')
				w++
			}
			cur.WriteString(word)
			w += ww
			started = true
		}
		if !started {
			cur.Reset() // a blank line stays blank
		}
		lines = append(lines, cur.String())
	}
	return lines
}
