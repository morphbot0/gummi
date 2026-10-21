package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/webapi"
)

// What a request may say is checked at the door: a malformed query or
// body is a 400 that names the mistake, never a 500 and never a call
// into the board with a value it cannot read.
func TestRequestsAreCheckedBeforeTheBoardSeesThem(t *testing.T) {
	b := newBoardHarness(t)
	for _, c := range []struct {
		name, method, path string
		body               any
		want               string
	}{
		// a budget is whole credits: a fraction, or a number past an int,
		// is the person's typing and is answered in the field's words
		{"new card, fractional budget", http.MethodPost, "/api/cards", map[string]any{"kind": "feature", "title": "x", "envelope": 2.5}, "Budget must be a whole, non-negative number of credits"},
		{"new card, huge budget", http.MethodPost, "/api/cards", map[string]any{"kind": "feature", "title": "x", "envelope": 1e20}, "Budget must be a whole, non-negative number of credits"},
		{"budget action, fractional", http.MethodPost, "/api/cards/FD-001/actions/envelope", map[string]any{"number": 1.5}, "Budget must be a whole, non-negative number of credits"},
	} {
		var e webapi.Error
		if got := b.call(c.method, c.path, c.body, &e); got != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", c.name, got)
			continue
		}
		if !strings.Contains(e.Error, c.want) {
			t.Errorf("%s: error = %q, want it to say %q", c.name, e.Error, c.want)
		}
	}
}
