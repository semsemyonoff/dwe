package trace

import (
	"context"
	"log/slog"
	"strings"
)

// slogHandler formats slog records and routes them through the same printer
// precedence as the trace emit functions (ctx override → global printer →
// fallback writer). It is installed via slog.SetDefault by the CLI root ONLY
// when the level is LevelDebug, so existing Warn/Error behaviour is unchanged
// when no diagnostic flags are set.
//
// Because it is installed only at Debug, Enabled always returns true: every
// record that reaches the handler is meant to be emitted.
type slogHandler struct {
	preformatted string   // " key=val" pairs accumulated via WithAttrs
	groups       []string // active group path applied to record attrs
}

// NewSlogHandler returns an slog.Handler that routes records to the trace sink.
// Install it via slog.SetDefault only when the active level is LevelDebug.
func NewSlogHandler() slog.Handler {
	return &slogHandler{}
}

// Enabled reports whether records at the given level are handled. The handler is
// only ever installed at Debug, so it accepts everything.
func (h *slogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

// Handle formats the record as "LEVEL message key=val …" and emits it through
// the trace printer precedence using the record's context.
func (h *slogHandler) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Level.String())
	b.WriteByte(' ')
	b.WriteString(r.Message)
	b.WriteString(h.preformatted)
	prefix := strings.Join(h.groups, ".")
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, prefix, a)
		return true
	})
	emit(ctx, b.String())
	return nil
}

// WithAttrs returns a handler that prepends attrs (qualified by the current
// group path) to every subsequent record.
func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	var b strings.Builder
	b.WriteString(h.preformatted)
	prefix := strings.Join(h.groups, ".")
	for _, a := range attrs {
		writeAttr(&b, prefix, a)
	}
	return &slogHandler{preformatted: b.String(), groups: h.groups}
}

// WithGroup returns a handler that nests subsequent attrs under name.
func (h *slogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := make([]string, len(h.groups)+1)
	copy(groups, h.groups)
	groups[len(h.groups)] = name
	return &slogHandler{preformatted: h.preformatted, groups: groups}
}

// writeAttr appends " key=value" to b, qualifying key with prefix and expanding
// group-valued attrs recursively. Empty attrs and empty groups are skipped.
func writeAttr(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	key := a.Key
	if prefix != "" && key != "" {
		key = prefix + "." + key
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, ga := range a.Value.Group() {
			writeAttr(b, key, ga)
		}
		return
	}
	if a.Equal(slog.Attr{}) {
		return
	}
	b.WriteByte(' ')
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(a.Value.String())
}
