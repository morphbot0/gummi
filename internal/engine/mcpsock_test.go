package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/mcp"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
)

// fakeNoTools is an agent that cannot call client tools, so the engine
// bridges it over MCP.
type fakeNoTools struct{ *agent.Fake }

// sockConn is a raw framing client over one accepted session socket.
type sockConn struct {
	t    *testing.T
	conn net.Conn
	sc   *bufio.Scanner
	seq  int
}

func dialSock(t *testing.T, path string) *sockConn {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	return &sockConn{t: t, conn: conn, sc: sc}
}

func (c *sockConn) send(v any) {
	c.t.Helper()
	if err := mcp.Encode(c.conn, v); err != nil {
		c.t.Fatal(err)
	}
}

// read scans for the next frame with the given json id (skipping any
// interleaved others — for single-flight tests there are none).
func (c *sockConn) read(id string) map[string]any {
	c.t.Helper()
	if !c.sc.Scan() {
		c.t.Fatalf("socket closed awaiting id %s", id)
	}
	var got map[string]any
	if err := jsonUnmarshal(c.t, c.sc.Bytes(), &got); err != nil {
		c.t.Fatal(err)
	}
	b, _ := jsonMarshal(got["id"])
	if string(b) != id {
		c.t.Fatalf("socket response id = %s, want %s", b, id)
	}
	return got
}

func (c *sockConn) hello(feature string) map[string]any {
	c.t.Helper()
	id := c.nextID()
	c.send(mcp.Request{
		JSONRPC: mcp.JSONRPC, ID: jsonRaw(id), Method: "hello",
		Params: jsonRaw(`{"feature":"` + feature + `"}`),
	})
	return c.read(id)
}

func (c *sockConn) nextID() string {
	c.seq++
	return i2s(c.seq)
}

// startMCPEndpoint returns only once the listener is accepting: dialing
// the returned path succeeds synchronously before any child spawns.
func TestMCPSockBindOrderingDial(t *testing.T) {
	e := newEngine(t, &fakeNoTools{agent.NewFake("")})
	f := domain.Feature{ID: "FF-001", Kind: domain.KindFreeform, Stage: domain.StageOpen, Profile: "default"}
	path, teardown, err := e.startMCPEndpoint(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()
	if got, want := path, mcpSockPath(e.cfg.Workspace, f.ID); got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	// the bind-ordering guarantee: dial succeeds without racing the accept
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	if err != nil {
		t.Fatalf("dial immediately after startMCPEndpoint: %v", err)
	}
	_ = conn.Close()
}

// a hello for the wrong feature is answered -32000 and the endpoint
// closes the connection rather than serving it.
func TestMCPSockHandshakeFeatureMismatch(t *testing.T) {
	e := newEngine(t, &fakeNoTools{agent.NewFake("")})
	f := domain.Feature{ID: "FF-001", Kind: domain.KindFreeform, Stage: domain.StageOpen, Profile: "default"}
	path, teardown, err := e.startMCPEndpoint(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()
	resp := dialSock(t, path).hello("FD-999")
	er := resp["error"].(map[string]any)
	if int(er["code"].(float64)) != mcp.FeatureMismatch {
		t.Fatalf("error code = %v, want %d", er["code"], mcp.FeatureMismatch)
	}
}

// a correct hello is acknowledged and list_tools returns the session's
// own tools (freeformTools), in order.
func TestMCPSockListTools(t *testing.T) {
	e := newEngine(t, &fakeNoTools{agent.NewFake("")})
	f := domain.Feature{ID: "FF-001", Kind: domain.KindFreeform, Stage: domain.StageOpen, Profile: "default"}
	path, teardown, err := e.startMCPEndpoint(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()
	c := dialSock(t, path)
	if r := c.hello("FF-001"); r["error"] != nil {
		t.Fatalf("hello error: %v", r["error"])
	}
	id := c.nextID()
	c.send(mcp.Request{JSONRPC: mcp.JSONRPC, ID: jsonRaw(id), Method: "list_tools"})
	resp := c.read(id)
	tools := resp["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools length = %d, want 2", len(tools))
	}
	for i, want := range []string{"ask_user", "resolve_annotation"} {
		if tools[i].(map[string]any)["name"] != want {
			t.Fatalf("tool[%d] = %v, want %s", i, tools[i].(map[string]any)["name"], want)
		}
	}
}

// call_tool with no live session for the feature answers an error.
func TestMCPSockCallToolNoSession(t *testing.T) {
	e := newEngine(t, &fakeNoTools{agent.NewFake("")})
	f := domain.Feature{ID: "FF-001", Kind: domain.KindFreeform, Stage: domain.StageOpen, Profile: "default"}
	path, teardown, err := e.startMCPEndpoint(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()
	c := dialSock(t, path)
	c.hello("FF-001")
	id := c.nextID()
	c.send(mcp.Request{
		JSONRPC: mcp.JSONRPC, ID: jsonRaw(id), Method: "call_tool",
		Params: jsonRaw(`{"name":"spec_view","args":{}}`),
	})
	if resp := c.read(id); resp["error"] == nil {
		t.Fatalf("call_tool without a session succeeded: %v", resp)
	}
}

// two in-flight call_tools on one connection interleave: spec_view (fast,
// resolves immediately) returns while ask_user is still blocked on the
// human. The session reads a real spec file.
func TestMCPSockCallToolInterleave(t *testing.T) {
	e := newEngine(t, &fakeNoTools{agent.NewFake("")})

	f := freeformCard(1, "interleave")
	createFeature(t, e.cfg.Store, f)
	sp, err := e.cfg.Store.AddSessionSpec(context.Background(), f.ID, "interleave", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(e.pool.Root(), sp.Path)
	specBody := "# FD-001\n\n## Problem\n\nsome problem\n"
	if err := os.MkdirAll(filepath.Dir(specPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specPath, []byte(specBody), 0o600); err != nil {
		t.Fatal(err)
	}
	s := attach(t, e, f)
	waitIdle(t, e, f.ID)

	path, teardown, err := e.startMCPEndpoint(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()
	c := dialSock(t, path)
	if r := c.hello("FF-001"); r["error"] != nil {
		t.Fatalf("hello error: %v", r["error"])
	}

	// slow ask_user blocks; fast spec_view must resolve first.
	slowID, fastID := c.nextID(), c.nextID()
	c.send(mcp.Request{
		JSONRPC: mcp.JSONRPC, ID: jsonRaw(slowID), Method: "call_tool",
		Params: jsonRaw(`{"name":"ask_user","args":{"changes_section":"Problem","question":"pick","options":[{"label":"a"}]}}`),
	})
	c.send(mcp.Request{
		JSONRPC: mcp.JSONRPC, ID: jsonRaw(fastID), Method: "call_tool",
		Params: jsonRaw(`{"name":"spec_view","args":{"section":"Problem"}}`),
	})

	// spec_view arrives while ask_user is still blocked.
	if !c.sc.Scan() {
		t.Fatal("socket closed before spec_view response")
	}
	var fast map[string]any
	if err := jsonUnmarshal(t, c.sc.Bytes(), &fast); err != nil {
		t.Fatal(err)
	}
	gotID, _ := jsonMarshal(fast["id"])
	if string(gotID) != fastID {
		t.Fatalf("first response id = %s, want %s (fast spec_view first)", gotID, fastID)
	}
	text := fast["result"].(map[string]any)["result"].(string)
	wantBody, _ := spec.ViewSection(specBody, "Problem")
	if text != wantBody {
		t.Fatalf("spec_view text = %q, want %q", text, wantBody)
	}

	// only now resolve the ask; its response is the chosen answer. Wait
	// for the ask_user dispatch to have registered the pending question
	// (it runs in its own socket goroutine) before answering.
	askDeadline := time.After(testWaitTimeout)
	for s.Snapshot().PendingAsk == nil {
		select {
		case <-askDeadline:
			t.Fatal("ask_user pending question never registered")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := e.Answer(context.Background(), f.ID, "picked-option"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if !c.sc.Scan() {
		t.Fatal("socket closed before ask_user response")
	}
	var slow map[string]any
	if err := jsonUnmarshal(t, c.sc.Bytes(), &slow); err != nil {
		t.Fatal(err)
	}
	gotID, _ = jsonMarshal(slow["id"])
	if string(gotID) != slowID {
		t.Fatalf("second response id = %s, want %s", gotID, slowID)
	}
	if text := slow["result"].(map[string]any)["result"].(string); text != "picked-option" {
		t.Fatalf("ask_user text = %q, want picked-option", text)
	}
}

// teardown removes the socket file and leaves the accept loop done.
func TestMCPSockTeardown(t *testing.T) {
	e := newEngine(t, &fakeNoTools{agent.NewFake("")})
	f := domain.Feature{ID: "FF-001", Kind: domain.KindFreeform, Stage: domain.StageOpen, Profile: "default"}
	path, teardown, err := e.startMCPEndpoint(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	teardown()
	teardown() // idempotent
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket file still present after teardown: %v", err)
	}
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop not joined within 2s")
	default:
	}
}

// A workspace deep on disk must not push the socket path past the unix
// socket length limit: mcpSockPath falls back to a short system-temp path
// that still binds, while short roots stay workspace-scoped.
func TestMCPSockPathStaysUnderUnixLimit(t *testing.T) {
	longRoot := filepath.Join(t.TempDir(), strings.Repeat("d", 130))
	ws := state.Workspace{Root: longRoot}
	path := mcpSockPath(ws, "FF-001")
	if len(path) > unixPathMax {
		t.Fatalf("socket path %d bytes exceeds unix limit: %s", len(path), path)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatalf("bind long-root socket %s: %v", path, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove long-root socket: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	// short roots keep the workspace-scoped location unchanged.
	wsShort := state.Workspace{Root: t.TempDir()}
	if p := mcpSockPath(wsShort, "FF-001"); !strings.HasPrefix(p, filepath.Join(wsShort.StateDir(), "mcp")) {
		t.Errorf("short root moved off the workspace: %s", p)
	}
}

func jsonUnmarshal(t *testing.T, b []byte, v any) error {
	t.Helper()
	return json.Unmarshal(b, v)
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }

func i2s(n int) string { return strconv.Itoa(n) }

// TestMCPSockCallToolReachesAFreeformSession: a freeform card's session
// lives in e.freeform, never e.live, and an MCP backend's ask_user must
// still reach it — it used to be refused as "no live session".
func TestMCPSockCallToolReachesAFreeformSession(t *testing.T) {
	ag := agent.NewFake("")
	ag.Caps = agent.Capabilities{MCPTools: true}
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })
	f := freeformCard(1, "poke at the pty leak")
	createFeature(t, store, f)
	ff, err := e.OpenFreeform(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	c := dialSock(t, mcpSockPath(ws, f.ID))
	if r := c.hello(string(f.ID)); r["error"] != nil {
		t.Fatalf("hello error: %v", r["error"])
	}
	id := c.nextID()
	c.send(mcp.Request{
		JSONRPC: mcp.JSONRPC, ID: jsonRaw(id), Method: "call_tool",
		Params: jsonRaw(`{"name":"ask_user","args":{"question":"which fd?","options":[{"label":"the pty"},{"label":"the log"}]}}`),
	})
	deadline := time.After(testWaitTimeout)
	for ff.Session().Snapshot().PendingAsk == nil {
		select {
		case <-deadline:
			t.Fatal("the freeform session never received the ask")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := e.Answer(context.Background(), f.ID, "the pty"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	resp := c.read(id)
	if resp["error"] != nil {
		t.Fatalf("call_tool error: %v", resp["error"])
	}
	if got := resp["result"].(map[string]any)["result"].(string); !strings.Contains(got, "the pty") {
		t.Errorf("ask_user result = %q, want the answer", got)
	}
}
