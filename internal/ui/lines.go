package ui

import "strings"

// oneLineText flattens text to a single line for compact display.
func oneLineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clipLines truncates rendered content to at most h lines so the surface
// never overflows its rect.
func clipLines(s string, h int) string {
	if h <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= h {
		return s
	}
	return strings.Join(lines[:h], "\n")
}

// windowLines returns at most h entries from lines, scrolled so the entry at
// cursor stays visible. Without this a selection past the fold would be
// acted on while off-screen. Returns lines unchanged when it already fits.
func windowLines(lines []string, cursor, h int) []string {
	if h <= 0 || len(lines) <= h {
		return lines
	}
	off := cursor - (h-1)/2
	if off < 0 {
		off = 0
	}
	if off > len(lines)-h {
		off = len(lines) - h
	}
	return lines[off : off+h]
}
