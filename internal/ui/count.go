package ui

import "strconv"

func itoa(n int) string { return strconv.Itoa(n) }

// plural is "" for exactly one, "s" otherwise.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
