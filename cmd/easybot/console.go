package main

import (
	"context"
	"easybot/internal/runtime"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

type consoleHandler struct {
	minimum slog.Level
	mu      *sync.Mutex
	attrs   []slog.Attr
	group   string
}

var useConsoleColors = os.Getenv("NO_COLOR") == "" && enableConsoleColors()

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
	level, color := "INFO", "97"
	switch {
	case record.Level >= slog.LevelError+4:
		level, color = "FATL", "97;41"
	case record.Level >= slog.LevelError:
		level, color = "EROR", "97;41"
	case record.Level >= slog.LevelWarn:
		level, color = "WARN", "93"
	case record.Level < slog.LevelInfo:
		level, color = "DBUG", "37"
	}
	fmt.Fprint(&line, consoleColor("[", "90"), consoleColor(record.Time.Format("15:04:05"), "37"), consoleColor("][", "90"), consoleColor(level, color), consoleColor("][", "90"), consoleColor(module, "96"), consoleColor("]:", "90"), consoleColor(message, "97"))
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
		next.group += name + "."
	}
	return &next
}

func consoleColor(text, color string) string {
	if !useConsoleColors {
		return text
	}
	return "\x1b[" + color + "m" + text + "\x1b[0m"
}

func printBanner(logger *slog.Logger) {
	// 使用与原版 FigletText 相同的字形，保留 CE 的产品名称。
	logo := "  _____                         _               _      ____   _____ \n" +
		" | ____|   __ _   ___   _   _  | |__     ___   | |_   / ___| | ____|\n" +
		" |  _|    / _` | / __| | | | | | '_ \\   / _ \\  | __| | |     |  _|  \n" +
		" | |___  | (_| | \\__ \\ | |_| | | |_) | | (_) | | |_  | |___  | |___ \n" +
		" |_____|  \\__,_| |___/  \\__, | |_.__/   \\___/   \\__|  \\____| |_____|\n" +
		"                        |___/                                       "
	fmt.Fprintln(os.Stdout, consoleColor(logo, "96"))
	logger.Info("EasybotCE v0.1.0-dev")
}

func printStartup(logger *slog.Logger, address string, tls bool, password string, initialPassword bool) {
	host, port, _ := net.SplitHostPort(address)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	if tls {
		scheme = "https"
	}
	logger.Info("[WebUI] 管理地址: " + scheme + "://" + net.JoinHostPort(host, port))
	logger.Info("[WebUI] 已启用管理密码登录")
	if initialPassword {
		// Credentials are intentionally printed once to the terminal, never to persisted logs.
		fmt.Fprintln(os.Stdout, consoleColor("[WebUI] 首次启动默认管理密码: "+password, "93"))
		fmt.Fprintln(os.Stdout, consoleColor("[WebUI] 默认密码仅显示一次，请保存后及时修改 config.json", "93"))
	}
	logger.Info("[WebUI] 服务已启动，按 Ctrl+C 停止")
}
