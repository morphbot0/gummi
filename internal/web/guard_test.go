package web

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// DNS rebinding: a page on another name, re-pointed at this server, is
// refused on every route whatever else it carries — the page, the public
// routes, a write with a matching Origin, the event stream.
func TestOnlyTheServersOwnNamesAreAnswered(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.OpenAccess = true; o.Hosts = []string{"Board.Example."} })
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(h.http.URL, "http://"))
	c := h.client()

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/", ""},
		{http.MethodGet, "/assets/app.js", ""},
		{http.MethodGet, "/api/session", ""},
		{http.MethodGet, "/api/board", ""},
		{http.MethodGet, "/api/events", ""},
		{http.MethodPost, "/api/pair/request", "{}"},
		{http.MethodPost, "/api/cards", `{"kind":"freeform","title":"pwned"}`},
	} {
		res, _ := h.do(c, tc.method, tc.path, tc.body,
			"Host", "attacker.example:"+port, "Origin", "http://attacker.example:"+port)
		if res.StatusCode != http.StatusMisdirectedRequest {
			t.Errorf("%s %s for attacker.example = %d, want 421", tc.method, tc.path, res.StatusCode)
		}
	}
	for _, host := range []string{
		"127.0.0.1:" + port, "localhost:" + port, "LOCALHOST", "[::1]:" + port,
		"board.example:" + port, "board.example", // a configured name, any port
	} {
		if res, _ := h.do(c, http.MethodGet, "/api/session", "", "Host", host); res.StatusCode != http.StatusOK {
			t.Errorf("GET /api/session for %s = %d, want 200", host, res.StatusCode)
		}
	}
	// a name learned later (the tailnet's, once the node is up)
	if res, _ := h.do(c, http.MethodGet, "/api/session", "", "Host", "gummi.tail1234.ts.net"); res.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("the tailnet name before it is known = %d, want 421", res.StatusCode)
	}
	h.srv.AllowHosts("gummi.tail1234.ts.net")
	if res, _ := h.do(c, http.MethodGet, "/api/session", "", "Host", "gummi.tail1234.ts.net"); res.StatusCode != http.StatusOK {
		t.Errorf("the tailnet name once allowed = %d, want 200", res.StatusCode)
	}
}

// The address the connection arrived on is one of the server's names — a
// LAN listener opened by its address — and another address is not.
func TestHostMatchesTheListenersOwnAddress(t *testing.T) {
	h := newHarness(t)
	hostAt := func(host, local string) bool {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		addr, err := net.ResolveTCPAddr("tcp", local)
		if err != nil {
			t.Fatal(err)
		}
		r = r.WithContext(contextWithLocalAddr(r, addr))
		return h.srv.hostAllowed(r)
	}
	if !hostAt("192.168.1.5:7878", "192.168.1.5:7878") {
		t.Error("the listener's own address was refused")
	}
	if hostAt("192.168.1.6:7878", "192.168.1.5:7878") {
		t.Error("another address was answered")
	}
	if hostAt("", "127.0.0.1:7878") {
		t.Error("an empty Host was answered")
	}
}

// The Origin check compares the scheme as well as the host: an https page
// is not the http page on the same host and port.
func TestSameOriginComparesTheScheme(t *testing.T) {
	h := newHarness(t)
	c := h.client()
	h.pair(c, "Simon")
	https := strings.Replace(h.http.URL, "http://", "https://", 1)
	if res, _ := h.do(c, http.MethodPost, "/api/cards/FD-001/send", `{"text":"hi"}`, "Origin", https); res.StatusCode != http.StatusForbidden {
		t.Errorf("an https Origin on an http listener = %d, want 403", res.StatusCode)
	}
	// a TLS-terminating proxy on this machine (`tailscale serve`) says so
	if res, _ := h.do(c, http.MethodPost, "/api/cards/FD-001/send", `{"text":"hi"}`, "Origin", https, "X-Forwarded-Proto", "https"); res.StatusCode != http.StatusNotFound {
		t.Errorf("an https Origin through a local proxy = %d, want through to the handler (404)", res.StatusCode)
	}
}

// HSTS rides on TLS responses for a name, never for an address or
// localhost (which would pin every server on this machine's loopback).
func TestHSTSOnTLSForANameOnly(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Hosts = []string{"board.example"} })
	tlsSrv := httptest.NewTLSServer(h.srv.Handler())
	t.Cleanup(tlsSrv.Close)
	get := func(base, host string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, base+"/api/session", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		client := tlsSrv.Client()
		if !strings.HasPrefix(base, "https") {
			client = http.DefaultClient
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res
	}
	if got := get(tlsSrv.URL, "board.example").Header.Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=") {
		t.Errorf("HSTS over TLS for a name = %q", got)
	}
	for _, host := range []string{strings.TrimPrefix(tlsSrv.URL, "https://"), "localhost"} {
		if got := get(tlsSrv.URL, host).Header.Get("Strict-Transport-Security"); got != "" {
			t.Errorf("HSTS for %s = %q, want none", host, got)
		}
	}
	if got := get(h.http.URL, "board.example").Header.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS over plain HTTP = %q, want none", got)
	}
}

// A client that sends its headers and then trickles the body is cut off,
// while a handler that runs long after reading its body is not: its
// context stays alive.
func TestASlowBodyIsCutOffButASlowHandlerIsNot(t *testing.T) {
	old := bodyReadTimeout
	bodyReadTimeout = 200 * time.Millisecond
	t.Cleanup(func() { bodyReadTimeout = old })

	srv := httptest.NewServer(readDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "slow", http.StatusRequestTimeout)
			return
		}
		select {
		case <-time.After(3 * bodyReadTimeout):
			_, _ = io.WriteString(w, "ok")
		case <-r.Context().Done():
			http.Error(w, "cancelled", http.StatusServiceUnavailable)
		}
	})))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")

	// the slow client: headers, a byte of body, then nothing
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: %s\r\nContent-Length: 1000\r\n\r\n{", addr)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Fatalf("the slow body held the connection for %s", waited)
	}

	// the slow handler: a complete body, then work past the deadline
	res, err = http.Post(srv.URL, "application/json", strings.NewReader(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || string(b) != "ok" {
		t.Errorf("a slow handler after a full body = %d %q, want ok", res.StatusCode, b)
	}
}

func contextWithLocalAddr(r *http.Request, addr net.Addr) context.Context {
	return context.WithValue(r.Context(), http.LocalAddrContextKey, addr)
}

// An endpoint on this machine or its network is refused at subscribe
// time, before anything is stored.
func TestAPushEndpointMustBePublic(t *testing.T) {
	pusher, err := OpenPush(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.Push = pusher })
	c := h.client()
	h.pair(c, "Simon")
	sub := testSubscription(t, "x")
	for _, ep := range []string{"https://127.0.0.1:6379/x", "https://169.254.169.254/latest", "https://localhost/x"} {
		body := fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}}`, ep, sub.Keys.P256dh, sub.Keys.Auth)
		if res, out := h.do(c, http.MethodPost, "/api/push/subscribe", body); res.StatusCode != http.StatusBadRequest {
			t.Errorf("subscribe %s = %d %v, want 400", ep, res.StatusCode, out)
		}
	}
	if subs := pusher.Store.List(); len(subs) != 0 {
		t.Errorf("stored %+v", subs)
	}
}
