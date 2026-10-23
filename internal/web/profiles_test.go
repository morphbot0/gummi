package web

import (
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/webapi"
)

// TestProfileRoutes: a person adds, edits, defaults and deletes profiles
// from the page; each answer is the file as it then reads, the new-card
// form offers the names at once, and a refusal leaves the file alone.
func TestProfileRoutes(t *testing.T) {
	b := newBoardHarness(t)
	path := b.root + "/.gummi/profiles.yaml"

	var got webapi.Profiles
	b.must(http.StatusOK, http.MethodGet, "/api/profiles", nil, &got)
	if len(got.Profiles) != 0 || got.Path != path || len(got.Slots) == 0 || !slices.Contains(got.Backends, "claude") || got.DefaultBackend != "fake" {
		t.Fatalf("a board with no profiles file reads %+v", got)
	}

	cheap := webapi.ProfileRequest{Roles: []webapi.ProfileRole{
		{Role: "implementer", Model: "fake-model"},
		{Role: "scout", Model: "fake-small"},
	}}
	b.must(http.StatusOK, http.MethodPut, "/api/profiles/cheap", cheap, &got)
	if got.Default != "cheap" || len(got.Profiles) != 1 || len(got.Profiles[0].Roles) != 2 || got.Problem != "" {
		t.Fatalf("after the first save: %+v", got)
	}
	b.must(http.StatusOK, http.MethodPut, "/api/profiles/strong", webapi.ProfileRequest{Roles: []webapi.ProfileRole{{Role: "implementer", Model: "fake-big"}}}, &got)
	b.must(http.StatusOK, http.MethodPost, "/api/profiles/strong/default", struct{}{}, &got)
	if got.Default != "strong" || got.Profiles[0].Name != "strong" {
		t.Fatalf("after the default moved: %+v", got)
	}
	var form webapi.Form
	b.must(http.StatusOK, http.MethodGet, "/api/form", nil, &form)
	if !slices.Equal(form.Profiles, []string{"strong", "cheap"}) {
		t.Errorf("the new-card form offers %v", form.Profiles)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var refused map[string]any
	for _, c := range []struct {
		method, url string
		body        any
		code        int
	}{
		{http.MethodDelete, "/api/profiles/strong", nil, http.StatusBadRequest},
		{http.MethodDelete, "/api/profiles/nope", nil, http.StatusNotFound},
		{http.MethodPost, "/api/profiles/nope/default", struct{}{}, http.StatusNotFound},
		{http.MethodPut, "/api/profiles/bad", webapi.ProfileRequest{Roles: []webapi.ProfileRole{{Role: "implementer", Backend: "claude", Model: "gpt-5"}}}, http.StatusBadRequest},
		{http.MethodPut, "/api/profiles/bad", webapi.ProfileRequest{Roles: []webapi.ProfileRole{{Role: "implementer", Model: "a"}, {Role: "implementer", Model: "b"}}}, http.StatusBadRequest},
		{http.MethodPut, "/api/profiles/has space", cheap, http.StatusBadRequest},
	} {
		if code := b.call(c.method, c.url, c.body, &refused); code != c.code {
			t.Errorf("%s %s = %d %v, want %d", c.method, c.url, code, refused, c.code)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("a refused edit changed the file:\n%s", after)
	}

	b.must(http.StatusOK, http.MethodDelete, "/api/profiles/cheap", nil, &got)
	if len(got.Profiles) != 1 || got.Profiles[0].Name != "strong" {
		t.Fatalf("after the delete: %+v", got)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "cheap") {
		t.Errorf("the file still names cheap:\n%s", raw)
	}
}
