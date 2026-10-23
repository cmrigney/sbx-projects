package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/cmrigney/sbx-projects/internal/sbx"
)

// attach opens the terminal the way a browser does, with the token as a
// subprotocol.
func (f *fixture) attach(t *testing.T, name string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	url := "wss" + strings.TrimPrefix(f.srv.URL, "https") + "/v1/sandboxes/" + name + "/terminal?cols=100&rows=30"
	return websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient:   f.srv.Client(),
		Subprotocols: []string{TerminalProtocol, bearerProtocol + f.token},
	})
}

// readUntil reads the terminal's output until it contains want.
func readUntil(t *testing.T, c *websocket.Conn, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out strings.Builder
	for !strings.Contains(out.String(), want) {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("reading for %q: %v; got %q", want, err, out.String())
		}
		out.Write(b)
	}
	return out.String()
}

func TestTerminalAttachesAndDetaches(t *testing.T) {
	f := newFixture(t, []sbx.MockSeed{{Name: "claude-a", Agent: "claude", Workspace: "/src/repo", Running: true, AgentUp: true}})
	f.waitFor(t, func(m map[string]SandboxView) bool { return m["claude-a"].AgentUp })
	c, _, err := f.attach(t, "claude-a")
	if err != nil {
		t.Fatal(err)
	}
	if c.Subprotocol() != TerminalProtocol {
		t.Fatalf("subprotocol = %q", c.Subprotocol())
	}
	readUntil(t, c, "sbx exec -it claude-a")
	// The demo's session ends at Enter, as tmux does at Ctrl+B d.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	for {
		if _, _, err = c.Read(ctx); err != nil {
			break
		}
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.StatusNormalClosure || ce.Reason != "Detached" {
		t.Fatalf("closed with %v", err)
	}
}

func TestTerminalResumesAStoppedSandboxFirst(t *testing.T) {
	f := newFixture(t, []sbx.MockSeed{{Name: "claude-a", Agent: "claude", Workspace: "/src/repo"}})
	f.waitFor(t, func(m map[string]SandboxView) bool { _, ok := m["claude-a"]; return ok })
	c, _, err := f.attach(t, "claude-a")
	if err != nil {
		t.Fatal(err)
	}
	out := readUntil(t, c, "sbx exec -it claude-a")
	if !strings.Contains(out, "Starting the sandbox…") || !strings.Contains(out, "Starting the agent…") {
		t.Fatalf("output = %q", out)
	}
	f.waitFor(t, func(m map[string]SandboxView) bool { return m["claude-a"].Running })
}

func TestTerminalRefusals(t *testing.T) {
	f := newFixture(t, nil)
	c, _, err := f.attach(t, "nope")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = c.Read(ctx)
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != closeUnavailable || !strings.Contains(ce.Reason, "no sandbox called nope") {
		t.Fatalf("closed with %v", err)
	}

	f.token = "wrong"
	if _, resp, err := f.attach(t, "nope"); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a bad token got %v, %+v", err, resp)
	}
}
