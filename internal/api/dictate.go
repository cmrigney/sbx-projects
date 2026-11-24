package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/cmrigney/sbx-projects/internal/whisper"
)

const (
	// maxDictation is 60 seconds of ADPCM at 8 KB/s. The Wio stops at 45.
	maxDictation = 60 * 8000
	// transcribeFor bounds one transcription, including waiting for another.
	transcribeFor = 60 * time.Second
	// dictateFor bounds a socket opened only to dictate: a recording and
	// its transcription.
	dictateFor = 3 * time.Minute
)

// Transcriber turns a recording into text.
type Transcriber interface {
	Transcribe(ctx context.Context, adpcm []byte) (string, error)
}

// dictation is a terminal's recording. Its audio arrives in text frames on
// the terminal's socket, as binary frames are keystrokes and the Wio has
// memory for only one TLS connection. The text goes back to the client, which
// types it, so that it can be edited before it's sent. Only the socket's
// read loop calls handle.
type dictation struct {
	speech Transcriber
	conn   *websocket.Conn
	ctx    context.Context
	audio  []byte
	cancel context.CancelFunc
}

// writeTextJSON sends a JSON text frame.
func writeTextJSON(ctx context.Context, c *websocket.Conn, v any) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // the Wio's reply buffer is small
	if enc.Encode(v) == nil {
		_ = c.Write(ctx, websocket.MessageText, bytes.TrimSpace(b.Bytes()))
	}
}

func (d *dictation) reply(v map[string]string) { writeTextJSON(d.ctx, d.conn, v) }

func (d *dictation) fail(message string) {
	d.reply(map[string]string{"type": "dictate-error", "message": message})
}

func (d *dictation) stop() {
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.audio = nil
}

func (d *dictation) handle(msg terminalMsg) {
	switch msg.Type {
	case "dictate-start":
		d.stop()
		d.audio = make([]byte, 0, 64<<10)
	case "dictate-cancel":
		d.stop()
	case "dictate-audio":
		if d.audio == nil {
			return
		}
		chunk, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil || len(d.audio)+len(chunk) > maxDictation {
			d.stop()
			d.fail("Recording too long")
			return
		}
		d.audio = append(d.audio, chunk...)
	case "dictate-end":
		if d.audio == nil {
			return
		}
		audio := d.audio
		d.audio = nil
		if d.speech == nil {
			d.fail(whisper.ErrNotSetUp.Error())
			return
		}
		ctx, cancel := context.WithTimeout(d.ctx, transcribeFor)
		d.cancel = cancel
		go func() {
			defer cancel()
			text, err := d.speech.Transcribe(ctx, audio)
			switch {
			case ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled):
				// Cancelled, or the terminal closed.
			case err == nil:
				d.reply(map[string]string{"type": "transcript", "text": text})
			case errors.Is(err, whisper.ErrNotSetUp), errors.Is(err, whisper.ErrNoSpeech):
				d.fail(err.Error())
			default:
				slog.Warn("transcribing", "err", err)
				d.fail("Transcription failed")
			}
		}()
	}
}

// dictate is dictation without a terminal, for the Wio's form fields. It
// speaks the terminal's dictation messages.
func (s *Server) dictate(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.Devices.Authenticate(terminalToken(r))
	if !ok {
		writeErr(w, fail(http.StatusUnauthorized, "This device isn't paired with %s. Pair it again with sbx-projects api --pair.", s.Name))
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{TerminalProtocol},
		InsecureSkipVerify: true, // authenticated by its token, as the terminal is
	})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(r.Context(), dictateFor)
	defer cancel()

	d := &dictation{speech: s.Speech, conn: c, ctx: ctx}
	go func() {
		defer cancel()
		defer d.stop()
		for {
			typ, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var msg terminalMsg
			if typ == websocket.MessageText && json.Unmarshal(b, &msg) == nil {
				d.handle(msg)
			}
		}
	}()
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
		}
	}
}
