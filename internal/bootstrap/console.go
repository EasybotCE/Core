package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"easybot/internal/runtime"
)

type consoleHandler struct {
	minimum slog.Level
	mu      *sync.Mutex
	attrs   []slog.Attr
	groups  []string
}

var useColors = os.Getenv("NO_COLOR") == "" && enableColors()

func consoleLogger(levels ...slog.Level) *slog.Logger {
	minimum := slog.LevelInfo
	if len(levels) != 0 {
		minimum = levels[0]
	}
	return slog.New(&consoleHandler{mu: &sync.Mutex{}, minimum: minimum})
}

func (h *consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minimum
}

func (h *consoleHandler) Handle(_ context.Context, record slog.Record) error {
	module := "Core"
	var attributes []string
	appendAttr := func(attr slog.Attr) bool {
		if attr.Key == "module" {
			module = consoleModule(attr.Value.Resolve().String())
			return true
		}
		if attr.Key == "error" {
			errorText := runtime.Redact(attr.Value.Resolve().String())
			errorText = strings.ReplaceAll(strings.ReplaceAll(errorText, "\r", " "), "\n", " ")
			if errorText != "" {
				attributes = append(attributes, "error="+strconv.Quote(errorText))
			}
		}
		return true
	}
	for _, attr := range h.attrs {
		appendAttr(attr)
	}
	record.Attrs(appendAttr)
	message := strings.ReplaceAll(strings.ReplaceAll(record.Message, "\r", " "), "\n", " ")
	if len(attributes) > 0 {
		message += " " + strings.Join(attributes, " ")
	}
	if strings.HasPrefix(message, "["+module+"] ") {
		message = strings.TrimPrefix(message, "["+module+"] ")
	}
	var line strings.Builder
	level, colorCode := "INFO", "97"
	switch {
	case record.Level >= slog.LevelError+4:
		level, colorCode = "FATL", "97;41"
	case record.Level >= slog.LevelError:
		level, colorCode = "EROR", "97;41"
	case record.Level >= slog.LevelWarn:
		level, colorCode = "WARN", "93"
	case record.Level < slog.LevelInfo:
		level, colorCode = "DBUG", "37"
	}
	fmt.Fprint(&line, colorize("[", "90"), colorize(record.Time.Format("15:04:05"), "37"), colorize("][", "90"), colorize(level, colorCode), colorize("][", "90"), colorize(module, "96"))
	for _, group := range h.groups {
		fmt.Fprint(&line, colorize("][", "90"), colorize(group, "96"))
	}
	fmt.Fprint(&line, colorize("]:", "90"), colorize(message, "97"))
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprintln(os.Stdout, line.String())
	return err
}

func consoleModule(module string) string {
	switch module {
	case "web":
		return "WebUI"
	case "daemon":
		return "Core"
	case "logs":
		return "Log"
	case "config":
		return "Config"
	case "api":
		return "API"
	case "adapter":
		return "Adapter"
	case "bridge":
		return "Bridge"
	default:
		if module == "" {
			return "Core"
		}
		return module
	}
}

func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &next
}

func (h *consoleHandler) WithGroup(name string) slog.Handler {
	next := *h
	if name != "" {
		next.groups = append(append([]string(nil), h.groups...), name)
	}
	return &next
}

func colorize(text, color string) string {
	if !useColors {
		return text
	}
	return "\x1b[" + color + "m" + text + "\x1b[0m"
}
