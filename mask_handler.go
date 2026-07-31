package slogs

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maskHandler implements slog.Handler with configurable mask output format.
//
// The mask string controls which fields appear in the output:
//
//	T - time (ISO8601)
//	L - level (DEBUG/INFO/WARN/ERROR)
//	C - caller (file.go:line)
//	M - message text
//
// Example - mask="TLCM" produces:
//
//	2026-08-01T01:14:09+08:00 INFO logger.go:84 server started
type maskHandler struct {
	mask   string
	level  slog.Leveler
	attrs  []slog.Attr
	groups []string
	w      io.Writer
	mu     sync.Mutex
}

// newMaskHandler creates a maskHandler. The mask string is uppercased;
// an empty mask maps to "LCM" (level, caller, message — no time).
func newMaskHandler(mask string, level slog.Level, w io.Writer) slog.Handler {
	m := strings.ToUpper(mask)
	if m == "" {
		m = "LCM"
	}
	return &maskHandler{mask: m, level: level, w: w}
}

func (h *maskHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *maskHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var buf bytes.Buffer

	if strings.Contains(h.mask, "T") {
		buf.WriteString(r.Time.Format(time.RFC3339))
		buf.WriteByte(' ')
	}
	if strings.Contains(h.mask, "L") {
		buf.WriteString(r.Level.String())
		buf.WriteByte(' ')
	}
	if strings.Contains(h.mask, "C") && r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		if f.File != "" {
			buf.WriteString(filepath.Base(f.File))
			buf.WriteByte(':')
			buf.WriteString(strconv.Itoa(f.Line))
			buf.WriteByte(' ')
		}
	}
	if strings.Contains(h.mask, "M") {
		buf.WriteString(r.Message)
	}

	// Append attributes as key=value pairs.
	hasAttrs := len(h.attrs) > 0 || r.NumAttrs() > 0
	if hasAttrs {
		buf.WriteString("  ")
		first := true
		appendAttr := func(key, value string) {
			if !first {
				buf.WriteByte(' ')
			}
			buf.WriteString(key)
			buf.WriteByte('=')
			buf.WriteString(value)
			first = false
		}
		r.Attrs(func(a slog.Attr) bool {
			appendAttr(h.resolveKey(a.Key), a.Value.String())
			return true
		})
		for _, a := range h.attrs {
			appendAttr(h.resolveKey(a.Key), a.Value.String())
		}
	}

	buf.WriteByte('\n')
	_, err := h.w.Write(buf.Bytes())
	return err
}

func (h *maskHandler) resolveKey(key string) string {
	if len(h.groups) == 0 {
		return key
	}
	return strings.Join(h.groups, ".") + "." + key
}

func (h *maskHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	newAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	newAttrs = append(newAttrs, h.attrs...)
	newAttrs = append(newAttrs, attrs...)
	return &maskHandler{
		mask:   h.mask,
		level:  h.level,
		attrs:  newAttrs,
		groups: h.groups,
		w:      h.w,
	}
}

func (h *maskHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name
	return &maskHandler{
		mask:   h.mask,
		level:  h.level,
		attrs:  h.attrs,
		groups: newGroups,
		w:      h.w,
	}
}
