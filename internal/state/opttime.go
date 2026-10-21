package state

import "time"

// formatOptTime stores a zero time as the empty string, the convention
// every optional timestamp column follows.
func formatOptTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeFmt)
}

// parseOptTime is formatOptTime's reverse.
func parseOptTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(timeFmt, v)
}
