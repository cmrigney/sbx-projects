package applog

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// keepRecent is how many entries Recent returns at most.
const keepRecent = 500

// Entry is a logged record, kept so the app can show it.
type Entry struct {
	Time  time.Time
	Level slog.Level
	Msg   string
	Attrs []Attr
}

type Attr struct{ Key, Value string }

var recent struct {
	sync.Mutex
	entries []Entry
}

// Recent returns the latest entries logged since Open, oldest first.
func Recent() []Entry {
	recent.Lock()
	defer recent.Unlock()
	return append([]Entry(nil), recent.entries...)
}

func keep(e Entry) {
	recent.Lock()
	defer recent.Unlock()
	if len(recent.entries) >= keepRecent {
		recent.entries = append(recent.entries[:0], recent.entries[len(recent.entries)-keepRecent+1:]...)
	}
	recent.entries = append(recent.entries, e)
}

// keeper passes records on to the file and keeps a copy for Recent.
type keeper struct {
	next   slog.Handler
	attrs  []Attr
	prefix string
}

func (h *keeper) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *keeper) Handle(ctx context.Context, r slog.Record) error {
	e := Entry{Time: r.Time, Level: r.Level, Msg: r.Message, Attrs: append([]Attr(nil), h.attrs...)}
	r.Attrs(func(a slog.Attr) bool {
		e.Attrs = flatten(e.Attrs, h.prefix, a)
		return true
	})
	keep(e)
	return h.next.Handle(ctx, r)
}

func (h *keeper) WithAttrs(as []slog.Attr) slog.Handler {
	attrs := append([]Attr(nil), h.attrs...)
	for _, a := range as {
		attrs = flatten(attrs, h.prefix, a)
	}
	return &keeper{next: h.next.WithAttrs(as), attrs: attrs, prefix: h.prefix}
}

func (h *keeper) WithGroup(name string) slog.Handler {
	return &keeper{next: h.next.WithGroup(name), attrs: h.attrs, prefix: h.prefix + name + "."}
}

func flatten(out []Attr, prefix string, a slog.Attr) []Attr {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		for _, g := range v.Group() {
			out = flatten(out, prefix+a.Key+".", g)
		}
		return out
	}
	if a.Key == "" {
		return out
	}
	return append(out, Attr{prefix + a.Key, v.String()})
}
