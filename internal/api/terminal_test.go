package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/cmrigney/sbx-projects/internal/sbx"
)

// attach opens the terminal the way a browser does, with the token as a
// subprotocol.
func (f *fixture) attach(t *testing.T, name string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return f.dialTerminal(t, name, "")
}

func (f *fixture) dialTerminal(t *testing.T, name, query string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	url := "wss" + strings.TrimPrefix(f.srv.URL, "https") + "/v1/sandboxes/" + name + "/terminal?cols=100&rows=30" + query
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

func TestTerminalOpensAShellWithoutTheAgent(t *testing.T) {
	f := newFixture(t, []sbx.MockSeed{{Name: "claude-a", Agent: "claude", Workspace: "/src/repo", Running: true}})
	f.waitFor(t, func(m map[string]SandboxView) bool { return m["claude-a"].Running })
	c, _, err := f.dialTerminal(t, "claude-a", "&shell=1")
	if err != nil {
		t.Fatal(err)
	}
	out := readUntil(t, c, "sbx exec -it claude-a bash -l")
	if strings.Contains(out, "Starting the agent") {
		t.Fatalf("a shell started the agent: %q", out)
	}
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
	if !errors.As(err, &ce) || ce.Code != websocket.StatusNormalClosure || ce.Reason != "The shell exited" {
		t.Fatalf("closed with %v", err)
	}
	if v := f.waitFor(t, func(map[string]SandboxView) bool { return true })["claude-a"]; v.AgentUp {
		t.Fatal("the agent was started")
	}
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

type fakeSpeech struct {
	got  chan []byte
	text string
	err  error
}

func (f *fakeSpeech) Transcribe(ctx context.Context, audio []byte) (string, error) {
	f.got <- audio
	return f.text, f.err
}

func dictationFixture(t *testing.T, speech Transcriber) *websocket.Conn {
	t.Helper()
	f := newFixture(t, []sbx.MockSeed{{Name: "claude-a", Agent: "claude", Workspace: "/src/repo", Running: true, AgentUp: true}})
	f.srvHandler.Speech = speech
	f.waitFor(t, func(m map[string]SandboxView) bool { return m["claude-a"].AgentUp })
	c, _, err := f.attach(t, "claude-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	readUntil(t, c, "Press enter")
	return c
}

func sendJSON(t *testing.T, c *websocket.Conn, body string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
		t.Fatal(err)
	}
}

// nextText skips terminal output and returns the next text frame.
func nextText(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		typ, b, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ == websocket.MessageText {
			return string(b)
		}
	}
}

func TestDictationReturnsTextWithoutTypingIt(t *testing.T) {
	speech := &fakeSpeech{got: make(chan []byte, 1), text: "fix <the> bug"}
	c := dictationFixture(t, speech)
	sendJSON(t, c, `{"type":"dictate-start"}`)
	sendJSON(t, c, `{"type":"dictate-audio","data":"AQID"}`)
	sendJSON(t, c, `{"type":"dictate-audio","data":"BAU="}`)
	sendJSON(t, c, `{"type":"dictate-end"}`)
	if got := <-speech.got; string(got) != "\x01\x02\x03\x04\x05" {
		t.Fatalf("audio = %v", got)
	}
	if got := nextText(t, c); got != `{"text":"fix <the> bug","type":"transcript"}` {
		t.Fatalf("reply = %s", got)
	}
}

func TestDictationWithoutWhisperSaysHowToSetItUp(t *testing.T) {
	c := dictationFixture(t, nil)
	sendJSON(t, c, `{"type":"dictate-start"}`)
	sendJSON(t, c, `{"type":"dictate-end"}`)
	if got := nextText(t, c); !strings.Contains(got, "sbx-projects api whisper") {
		t.Fatalf("reply = %s", got)
	}
}

func TestCancelledDictationIsNotTranscribed(t *testing.T) {
	speech := &fakeSpeech{got: make(chan []byte, 1), text: "never"}
	c := dictationFixture(t, speech)
	sendJSON(t, c, `{"type":"dictate-start"}`)
	sendJSON(t, c, `{"type":"dictate-audio","data":"AQID"}`)
	sendJSON(t, c, `{"type":"dictate-cancel"}`)
	sendJSON(t, c, `{"type":"dictate-end"}`)
	sendJSON(t, c, `{"type":"dictate-start"}`)
	sendJSON(t, c, `{"type":"dictate-audio","data":"BAU="}`)
	sendJSON(t, c, `{"type":"dictate-end"}`)
	if got := <-speech.got; string(got) != "\x04\x05" {
		t.Fatalf("audio = %v", got)
	}
}

func TestPlainReplyIsOneSafeLine(t *testing.T) {
	text, copied, total := plainReply("Done.\n\n- one\r\n- \x1b[31mtwo‮\x7f")
	if text != "Done. - one - [31mtwo" || copied != total || total != len(text) {
		t.Fatalf("got %q, %d of %d", text, copied, total)
	}
	for _, long := range []string{strings.Repeat("é", 900), strings.Repeat(`"`, 900), strings.Repeat("x", 1500)} {
		text, copied, total := plainReply(long)
		encoded := len(text) + strings.Count(text, `"`)
		if !utf8.ValidString(text) || len(text) > maxCopied || encoded > maxCopiedJSON || copied >= total && total > maxCopied {
			t.Fatalf("cut badly: %d bytes, %d encoded, %d of %d", len(text), encoded, copied, total)
		}
	}
}

func TestLastPRLinkIsOnTheProjectsRepo(t *testing.T) {
	const remote = "github.com/me/app"
	for in, want := range map[string]string{
		"Opened https://github.com/me/app/pull/7.":                                  "https://github.com/me/app/pull/7",
		"[#8](https://github.com/ME/App/pull/8/files)":                              "https://github.com/ME/App/pull/8",
		"https://github.com/me/app/pull/1 then https://github.com/me/app/pull/2":    "https://github.com/me/app/pull/2",
		"https://github.com/me/app/pull/3 and https://github.com/other/lib/pull/99": "https://github.com/me/app/pull/3",
		"https://github.com/other/lib/pull/99":                                      "",
		"no link":                                                                   "",
	} {
		if got := lastPRLink(in, remote); got != want {
			t.Errorf("lastPRLink(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTerminalMenuRequests(t *testing.T) {
	f := newFixture(t, []sbx.MockSeed{{Name: "claude-a", Agent: "claude", Workspace: "/src/repo", Running: true, AgentUp: true,
		Events: []map[string]any{{"ts": time.Now().Unix(), "ev": "Stop", "msg": "Opened\nhttps://github.com/me/app/pull/7"}}}})
	f.waitFor(t, func(m map[string]SandboxView) bool { return m["claude-a"].AgentUp })
	m := f.srvHandler.Monitor
	m.mu.Lock()
	for i := range m.projects {
		m.projects[i].Remote = "github.com/me/app"
	}
	m.mu.Unlock()
	var reply ReplyView
	deadline := time.Now().Add(5 * time.Second)
	for f.do(t, "GET", "/v1/sandboxes/claude-a/reply", "", &reply) != 200 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	c, _, err := f.attach(t, "claude-a")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	readUntil(t, c, "Press enter")
	sendJSON(t, c, `{"type":"reply-copy"}`)
	if got := nextText(t, c); !strings.Contains(got, `"text":"Opened https://github.com/me/app/pull/7"`) || !strings.Contains(got, `"type":"reply"`) {
		t.Fatalf("copy = %s", got)
	}
	sendJSON(t, c, `{"type":"reply-review"}`)
	if got := nextText(t, c); !strings.Contains(got, `"url":"https://github.com/me/app/pull/7"`) || !strings.Contains(got, `"type":"review"`) {
		t.Fatalf("review = %s", got)
	}
}

func TestDictationWithoutATerminal(t *testing.T) {
	f := newFixture(t, nil)
	speech := &fakeSpeech{got: make(chan []byte, 1), text: "add a health check"}
	f.srvHandler.Speech = speech
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "wss" + strings.TrimPrefix(f.srv.URL, "https") + "/v1/dictate"
	if _, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: f.srv.Client(),
		Subprotocols: []string{TerminalProtocol, bearerProtocol + "wrong"}}); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a bad token got %v", err)
	}
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: f.srv.Client(),
		Subprotocols: []string{TerminalProtocol, bearerProtocol + f.token}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	sendJSON(t, c, `{"type":"dictate-start"}`)
	sendJSON(t, c, `{"type":"dictate-audio","data":"AQID"}`)
	sendJSON(t, c, `{"type":"dictate-end"}`)
	<-speech.got
	if got := nextText(t, c); !strings.Contains(got, "add a health check") {
		t.Fatalf("reply = %s", got)
	}
}
