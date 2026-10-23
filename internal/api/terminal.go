package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/cmrigney/sbx-projects/internal/agent"
	"github.com/cmrigney/sbx-projects/internal/core"
)

// TerminalProtocol is the WebSocket subprotocol of the terminal. Browsers
// can't set headers on a WebSocket, so they send the token as a second
// subprotocol, "bearer.TOKEN", which is never echoed back.
const TerminalProtocol = "sbxp-terminal"

const bearerProtocol = "bearer."

// Close codes the terminal ends with besides the standard ones. Their
// reasons are written for people.
const (
	closeUnavailable websocket.StatusCode = 4000 + iota
	closeSignedOut
)

// terminalMsg is a text frame from the client. Keystrokes arrive as binary
// frames, and the terminal's output leaves as binary frames.
type terminalMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// pingEvery keeps idle terminals from being dropped by NATs and proxies.
var pingEvery = 30 * time.Second

func terminalToken(r *http.Request) string {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return t
	}
	for _, field := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(field, ",") {
			if t, ok := strings.CutPrefix(strings.TrimSpace(p), bearerProtocol); ok {
				return t
			}
		}
	}
	return ""
}

// terminal attaches to the agent's session over a WebSocket, as Enter does
// in the TUI: a stopped sandbox is resumed and a missing agent started
// first. Closing the socket detaches; the agent keeps running.
func (s *Server) terminal(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.Devices.Authenticate(terminalToken(r))
	if !ok {
		writeErr(w, fail(http.StatusUnauthorized, "This device isn't paired with %s. Pair it again with sbx-projects api --pair.", s.Name))
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{TerminalProtocol},
		// The token is sent by the page, never by the browser on its own as
		// a cookie would be, so another site can't open a terminal; the
		// Origin check would only break the dev server's proxy.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	name := r.PathValue("name")
	say := func(text string) { _ = c.Write(ctx, websocket.MessageBinary, []byte("\x1b[2m"+text+"\x1b[0m\r\n")) }
	if err := s.prepareAttach(name, say); err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			c.Close(closeUnavailable, closeReason(ae.msg))
		} else {
			c.Close(closeUnavailable, closeReason("Could not attach: "+err.Error()))
		}
		return
	}

	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	cmd := s.Monitor.svc.Backend.Interactive(name, agent.AttachArgv()...)
	// xterm.js draws 24-bit colour, which AttachArgv asks tmux for when it
	// sees this.
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	ptmx, err := pty.StartWithSize(cmd, winsize(cols, rows))
	if err != nil {
		c.Close(closeUnavailable, closeReason("Could not attach: "+err.Error()))
		return
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer stopAttach(cmd, ptmx, exited)

	// The terminal's output.
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					cancel()
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Keystrokes and resizes.
	go func() {
		defer cancel()
		for {
			typ, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := ptmx.Write(b); err != nil {
					return
				}
				continue
			}
			var msg terminalMsg
			if json.Unmarshal(b, &msg) == nil && msg.Type == "resize" {
				_ = pty.Setsize(ptmx, winsize(msg.Cols, msg.Rows))
			}
		}
	}()

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	recheck := time.NewTicker(recheckEvery)
	defer recheck.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-recheck.C:
			if !s.Devices.Has(dev.ID) {
				c.Close(closeSignedOut, "This device was signed out")
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		case <-outDone:
			// tmux exits when the client detaches with Ctrl+B d or the
			// session ends; let its last output reach the socket first.
			err := <-exited
			exited <- err
			reason := "Detached"
			if err != nil {
				reason = "The session ended: " + err.Error()
			}
			c.Close(websocket.StatusNormalClosure, closeReason(reason))
			return
		}
	}
}

// prepareAttach gets the sandbox ready to attach to, starting it or its
// agent and waiting for that when needed. say shows progress in the
// terminal.
func (s *Server) prepareAttach(name string, say func(string)) error {
	m := s.Monitor
	m.mu.Lock()
	it, ok := m.lookup(name)
	if !ok {
		m.mu.Unlock()
		return fail(http.StatusNotFound, "There's no sandbox called %s", name)
	}
	if it.busy != "" {
		m.mu.Unlock()
		return fail(http.StatusConflict, "%s is busy: %s", name, it.busy)
	}
	var steps []core.Step
	var done string
	switch {
	case !it.running():
		steps, done = m.svc.Resume(it.rec), "Resumed "+name
	case !it.agentUp:
		steps, done = []core.Step{m.svc.StartAgent(it.rec, "", true)}, "Started the agent in "+name
	}
	// Attaching reads the reply, as in the TUI.
	m.svc.MarkSeen(it.rec, it.probe)
	m.publish()
	if len(steps) == 0 {
		m.mu.Unlock()
		return nil
	}
	for i, st := range steps {
		run, busy := st.Run, st.Busy
		steps[i].Run = func(ctx context.Context) (string, error) {
			say(busy + "…")
			return run(ctx)
		}
	}
	finished := m.run(name, steps, done)
	m.mu.Unlock()
	return <-finished
}

// stopAttach ends the attach client, which detaches it from tmux. Closing
// the pty hangs it up; one that ignores that is killed.
func stopAttach(cmd *exec.Cmd, ptmx io.Closer, exited chan error) {
	_ = ptmx.Close()
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
	}
}

func winsize(cols, rows int) *pty.Winsize {
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	return &pty.Winsize{Cols: uint16(min(cols, 1000)), Rows: uint16(min(rows, 1000))}
}

// closeReason fits text into a close frame, whose reason is at most 123
// bytes.
func closeReason(text string) string {
	if len(text) <= 123 {
		return text
	}
	cut := 120
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return fmt.Sprintf("%s…", text[:cut])
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
