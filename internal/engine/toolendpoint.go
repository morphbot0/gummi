package engine

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/mcp"
)

// startToolEndpoint serves a fixed tool set over a per-open unix socket for
// an MCPTools backend, speaking the session-socket protocol the `gummi
// __mcp --feature <id>` shim dials. It is the consult endpoint's shape with
// the tool list and dispatch passed in, so a crew's planner or the curator
// reaches exactly its own tools and nothing else.
func (e *Engine) startToolEndpoint(ctx context.Context, id domain.FeatureID, label string, tools func() []agent.ToolDef, dispatch func(context.Context, string, json.RawMessage) (string, error)) (string, func(), error) {
	path := filepath.Join(e.cfg.Workspace.StateDir(), "mcp", fmt.Sprintf("%s-%s-%s.sock", label, id, mcpNonce()))
	if len(path) > unixPathMax {
		sum := sha256.Sum256([]byte(path))
		path = filepath.Join(os.TempDir(), fmt.Sprintf("gummi-mcp-%s-%x.sock", label, sum[:6]))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", nil, err
	}
	_ = os.Remove(path)
	if err := sockPathErr(path); err != nil {
		return "", nil, fmt.Errorf("%s mcp listen: %w", label, err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return "", nil, fmt.Errorf("%s mcp listen %s: %w", label, path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return "", nil, err
	}
	epCtx, epCancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var connMu sync.Mutex
	conns := map[net.Conn]struct{}{}
	closed := false
	serve := func(conn net.Conn) {
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		var wmu sync.Mutex
		write := func(v any) {
			wmu.Lock()
			defer wmu.Unlock()
			_ = mcp.Encode(conn, v)
		}
		first := true
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			req, err := mcp.Decode(line)
			if err != nil {
				continue
			}
			if first {
				first = false
				var h helloParams
				_ = json.Unmarshal(req.Params, &h)
				if req.Method != "hello" || h.Feature != string(id) {
					if len(req.ID) > 0 {
						write(mcp.ErrorObject{JSONRPC: mcp.JSONRPC, ID: req.ID, Error: mcp.ErrorData{Code: mcp.FeatureMismatch, Message: "feature mismatch"}})
					}
					return
				}
				if len(req.ID) > 0 {
					write(mcp.Response{JSONRPC: mcp.JSONRPC, ID: req.ID, Result: json.RawMessage(fmt.Sprintf(`{"feature":%q}`, h.Feature))})
				}
				continue
			}
			switch req.Method {
			case "list_tools":
				res, err := mcp.MarshalTools(tools())
				if err != nil {
					write(mcp.ErrorObject{JSONRPC: mcp.JSONRPC, ID: req.ID, Error: mcp.ErrorData{Code: mcp.ToolError, Message: err.Error()}})
					continue
				}
				write(mcp.Response{JSONRPC: mcp.JSONRPC, ID: req.ID, Result: res})
			case "call_tool":
				wg.Add(1)
				go func(req *mcp.Request) {
					defer wg.Done()
					var p callToolParams
					if err := json.Unmarshal(req.Params, &p); err != nil {
						write(mcp.ErrorObject{JSONRPC: mcp.JSONRPC, ID: req.ID, Error: mcp.ErrorData{Code: mcp.ToolError, Message: err.Error()}})
						return
					}
					out, err := dispatch(epCtx, p.Name, p.Args)
					if err != nil {
						write(mcp.ErrorObject{JSONRPC: mcp.JSONRPC, ID: req.ID, Error: mcp.ErrorData{Code: mcp.ToolError, Message: err.Error()}})
						return
					}
					res, _ := json.Marshal(map[string]any{"result": out})
					write(mcp.Response{JSONRPC: mcp.JSONRPC, ID: req.ID, Result: res})
				}(req)
			default:
				if len(req.ID) > 0 {
					write(mcp.ErrorObject{JSONRPC: mcp.JSONRPC, ID: req.ID, Error: mcp.ErrorData{Code: mcp.MethodNotFound, Message: "method not found"}})
				}
			}
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			connMu.Lock()
			if closed {
				connMu.Unlock()
				_ = conn.Close()
				return
			}
			conns[conn] = struct{}{}
			connMu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { connMu.Lock(); delete(conns, conn); connMu.Unlock() }()
				serve(conn)
			}()
		}
	}()
	var once sync.Once
	teardown := func() {
		once.Do(func() {
			epCancel()
			_ = ln.Close()
			connMu.Lock()
			closed = true
			for c := range conns {
				_ = c.Close()
			}
			connMu.Unlock()
			wg.Wait()
			_ = os.Remove(path)
		})
	}
	return path, teardown, nil
}
