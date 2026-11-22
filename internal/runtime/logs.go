package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"easybot/internal/model"
	"easybot/internal/security"
)

// Logs owns the process log buffer and creates module-scoped loggers. Every
// module logger writes the same structured event to console and runtime history.
type Logs struct {
	buffer *LogBuffer
	output *slog.Logger
}

func NewLogs(limit int, output *slog.Logger) *Logs {
	if output == nil {
		output = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Logs{buffer: NewLogBuffer(limit), output: output}
}

func (l *Logs) New(module string) *slog.Logger {
	return slog.New(&moduleHandler{logs: l, module: module, output: l.output.With("module", module)})
}

func (l *Logs) Recent(limit int) []LogEntry { return l.buffer.Recent(limit) }
func (l *Logs) SetSink(sink func(LogEntry)) { l.buffer.SetSink(sink) }

type moduleHandler struct {
	logs   *Logs
	module string
	output *slog.Logger
	attrs  []slog.Attr
	groups []string
}

func (h *moduleHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *moduleHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.output.Enabled(ctx, record.Level) {
		if err := h.output.Handler().Handle(ctx, record); err != nil {
			return err
		}
	}
	var message strings.Builder
	message.WriteString(record.Message)
	requestID := ""
	for _, attr := range h.attrs {
		appendLogAttr(&message, &requestID, "", attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendLogAttr(&message, &requestID, strings.Join(h.groups, "."), attr)
		return true
	})
	level := strings.ToLower(record.Level.String())
	h.logs.buffer.AddWithRequestID(level, h.module, message.String(), requestID)
	return nil
}

func (h *moduleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.output = h.output.With(attrsToArgs(attrs)...)
	// Freeze the current group at WithAttrs time, so later WithGroup calls
	// affect only attributes added afterwards.
	for i := len(h.groups) - 1; i >= 0; i-- {
		attrs = []slog.Attr{{Key: h.groups[i], Value: slog.GroupValue(attrs...)}}
	}
	next.attrs = append(slices.Clone(h.attrs), attrs...)
	return &next
}

func (h *moduleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.output = h.output.WithGroup(name)
	next.groups = append(slices.Clone(h.groups), name)
	return &next
}

// Keep searchable context in the existing message column, without changing
// the API or database schema. Redact whole credential values before quoting,
// including values containing whitespace or punctuation.
func appendLogAttr(message *strings.Builder, requestID *string, group string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	key := attr.Key
	if group != "" && key != "" {
		key = group + "." + key
	} else if key == "" {
		key = group
	}
	if attr.Value.Kind() == slog.KindGroup {
		for _, child := range attr.Value.Group() {
			appendLogAttr(message, requestID, key, child)
		}
		return
	}
	if attr.Key == "request_id" {
		*requestID = attr.Value.String()
	}
	value := Redact(fmt.Sprint(attr.Value.Any()))
	if security.SensitiveLogKey.MatchString(key) {
		value = "[REDACTED]"
	}
	message.WriteByte(' ')
	message.WriteString(strconv.Quote(key))
	message.WriteByte('=')
	message.WriteString(strconv.Quote(value))
}

func attrsToArgs(attrs []slog.Attr) []any {
	args := make([]any, 0, len(attrs)*2)
	for _, attr := range attrs {
		args = append(args, attr.Key, attr.Value.Any())
	}
	return args
}

var _ slog.Handler = (*moduleHandler)(nil)

type LogEntry = model.LogEntry

type LogBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	limit   int
	sink    func(LogEntry)
}

func NewLogBuffer(limit int) *LogBuffer {
	if limit < 1 {
		limit = 500
	}
	return &LogBuffer{limit: limit}
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(value[:])
}

func (b *LogBuffer) Add(level, source, message string) {
	b.AddWithRequestID(level, source, message, "")
}

func (b *LogBuffer) AddWithRequestID(level, source, message, requestID string) {
	entry := LogEntry{ID: newID(), Time: time.Now().UTC(), Level: level, Source: source, Message: Redact(message)}
	if validRequestID(requestID) {
		entry.RequestID = requestID
	}
	b.mu.Lock()
	sink := b.sink
	if len(b.entries) == b.limit {
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = entry
	} else {
		b.entries = append(b.entries, entry)
	}
	b.mu.Unlock()
	if sink != nil {
		sink(entry)
	}
}

func validRequestID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char)) {
			return false
		}
	}
	return true
}

func (b *LogBuffer) SetSink(sink func(LogEntry)) {
	b.mu.Lock()
	b.sink = sink
	b.mu.Unlock()
}

func (b *LogBuffer) Recent(limit int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit < 1 || limit > len(b.entries) {
		limit = len(b.entries)
	}
	result := make([]LogEntry, 0, limit)
	for i := len(b.entries) - 1; i >= 0 && len(result) < limit; i-- {
		result = append(result, b.entries[i])
	}
	return result
}

func Redact(value string) string { return security.Redact(value) }

type LogWriter interface {
	WriteLog(context.Context, LogEntry) error
}

type AsyncLogWriter struct {
	queue    chan LogEntry
	writer   LogWriter
	dropped  atomic.Uint64
	failed   atomic.Uint64
	enabled  atomic.Bool
	minLevel atomic.Int32
	done     chan struct{}
}

func NewAsyncLogWriter(writer LogWriter, capacity int) *AsyncLogWriter {
	if capacity < 1 {
		capacity = 1024
	}
	a := &AsyncLogWriter{queue: make(chan LogEntry, capacity), writer: writer, done: make(chan struct{})}
	a.enabled.Store(true)
	a.minLevel.Store(int32(slog.LevelDebug))
	go a.run()
	return a
}

func (a *AsyncLogWriter) TryWrite(entry LogEntry) {
	levels := map[string]int32{"debug": int32(slog.LevelDebug), "info": int32(slog.LevelInfo), "warn": int32(slog.LevelWarn), "error": int32(slog.LevelError)}
	level, ok := levels[strings.ToLower(entry.Level)]
	if strings.TrimSpace(entry.Level) == "" {
		level, ok = int32(slog.LevelDebug), true
	}
	if !ok || level < a.minLevel.Load() || !a.enabled.Load() {
		return
	}
	select {
	case a.queue <- entry:
	default:
		a.dropped.Add(1)
	}
}

func (a *AsyncLogWriter) Dropped() uint64         { return a.dropped.Load() }
func (a *AsyncLogWriter) Failed() uint64          { return a.failed.Load() }
func (a *AsyncLogWriter) SetEnabled(enabled bool) { a.enabled.Store(enabled) }
func (a *AsyncLogWriter) SetMinLevel(level string) {
	levels := map[string]int32{"debug": int32(slog.LevelDebug), "info": int32(slog.LevelInfo), "warn": int32(slog.LevelWarn), "error": int32(slog.LevelError)}
	if value, ok := levels[strings.ToLower(level)]; ok {
		a.minLevel.Store(value)
	}
}

func (a *AsyncLogWriter) Close(ctx context.Context) {
	close(a.queue)
	select {
	case <-a.done:
	case <-ctx.Done():
	}
}

func (a *AsyncLogWriter) run() {
	defer close(a.done)
	for entry := range a.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := a.writer.WriteLog(ctx, entry); err != nil {
			a.failed.Add(1)
		}
		cancel()
	}
}

func NormalizeLogFilterText(value string) string { return strings.TrimSpace(value) }
