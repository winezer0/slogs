// Package main contains isolated measurement tooling, not a production handler.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	slogs "github.com/winezer0/slogs"
	"gorm.io/gorm"
)

type logRow struct {
	ID         uint64 `gorm:"primaryKey"`
	Time       time.Time
	Level      string
	Message    string
	Attributes string
}

type boundAttr struct {
	path []string
	attr slog.Attr
}
type dbHandler struct {
	db     *gorm.DB
	attrs  []boundAttr
	groups []string
}

var _ slogs.BatchHandler = (*dbHandler)(nil)

func (h *dbHandler) Enabled(context.Context, slog.Level) bool { return true }
func putAttr(dst map[string]any, path []string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	for _, key := range path {
		child, ok := dst[key].(map[string]any)
		if !ok {
			child = map[string]any{}
			dst[key] = child
		}
		dst = child
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		if len(group) == 0 {
			return
		}
		if a.Key == "" {
			for _, item := range group {
				putAttr(dst, nil, item)
			}
			return
		}
		nested := map[string]any{}
		for _, item := range group {
			putAttr(nested, nil, item)
		}
		dst[a.Key] = nested
	} else {
		dst[a.Key] = a.Value.Any()
	}
}
func (h *dbHandler) Handle(ctx context.Context, r slog.Record) error {
	row, err := h.row(r)
	if err != nil {
		return err
	}
	return h.db.WithContext(ctx).Create(&row).Error
}

// HandleBatch commits all rows in one transaction, keeping GORM out of slogs.
// This adapter does not use per-record context values; adapters that do can read
// records[i].Context. The method context controls the entire transaction.
func (h *dbHandler) HandleBatch(ctx context.Context, records []slogs.BatchRecord) error {
	if len(records) == 0 {
		return nil
	}
	rows := make([]logRow, 0, len(records))
	for _, entry := range records {
		row, err := h.row(entry.Record)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	return h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Create(&rows).Error
	})
}

func (h *dbHandler) row(r slog.Record) (logRow, error) {
	attrs := map[string]any{}
	for _, a := range h.attrs {
		putAttr(attrs, a.path, a.attr)
	}
	r.Attrs(func(a slog.Attr) bool { putAttr(attrs, h.groups, a); return true })
	encoded, err := json.Marshal(attrs)
	if err != nil {
		return logRow{}, err
	}
	return logRow{Time: r.Time, Level: r.Level.String(), Message: r.Message, Attributes: string(encoded)}, nil
}
func (h *dbHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append([]boundAttr(nil), h.attrs...)
	for _, a := range attrs {
		a.Value = a.Value.Resolve()
		n.attrs = append(n.attrs, boundAttr{append([]string(nil), h.groups...), a})
	}
	return &n
}
func (h *dbHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	n := *h
	n.groups = append(append([]string(nil), h.groups...), name)
	return &n
}
