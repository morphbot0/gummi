package web

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A large JSON answer goes gzipped to a browser that takes it, and the same
// bytes decode; a small one, a request that refuses gzip and a websocket
// upgrade are written as they are.
func TestALargeJSONAnswerIsGzipped(t *testing.T) {
	big := map[string]string{"text": strings.Repeat("a line of the thread ", 200)}
	small := map[string]string{"ok": "yes"}
	serve := func(v any, header map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		for k, val := range header {
			r.Header.Set(k, val)
		}
		w := httptest.NewRecorder()
		compressJSON(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, v) })(w, r)
		return w
	}

	w := serve(big, map[string]string{"Accept-Encoding": "gzip, deflate"})
	if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers = %v, want gzip with Vary", w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil || got["text"] != big["text"] {
		t.Fatalf("decoded %q (%v), want the answer back", raw, err)
	}

	for name, c := range map[string]struct {
		v      any
		header map[string]string
	}{
		"small":   {small, map[string]string{"Accept-Encoding": "gzip"}},
		"refused": {big, map[string]string{"Accept-Encoding": "gzip;q=0"}},
		"none":    {big, nil},
		"upgrade": {big, map[string]string{"Accept-Encoding": "gzip", "Upgrade": "websocket"}},
	} {
		w := serve(c.v, c.header)
		if ce := w.Header().Get("Content-Encoding"); ce != "" {
			t.Errorf("%s: Content-Encoding = %q, want none", name, ce)
		}
		if !json.Valid(w.Body.Bytes()) {
			t.Errorf("%s: body %q is not JSON", name, w.Body.String())
		}
	}
}
