package bootstrap

import (
	"fmt"
	"log/slog"
	"net"
	"os"
)

func printBanner(logger *slog.Logger) {
	// 使用与原版 FigletText 相同的字形，保留 CE 的产品名称。
	logo := "  _____                         _               _      ____   _____ \n" +
		" | ____|   __ _   ___   _   _  | |__     ___   | |_   / ___| | ____|\n" +
		" |  _|    / _` | / __| | | | | | '_ \\   / _ \\  | __| | |     |  _|  \n" +
		" | |___  | (_| | \\__ \\ | |_| | | |_) | | (_) | | |_  | |___  | |___ \n" +
		" |_____|  \\__,_| |___/  \\__, | |_.__/   \\___/   \\__|  \\____| |_____|\n" +
		"                        |___/                                       "
	fmt.Fprintln(os.Stdout, colorize(logo, "96"))
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
		fmt.Fprintln(os.Stdout, colorize("[WebUI] 首次启动默认管理密码: "+password, "93"))
		fmt.Fprintln(os.Stdout, colorize("[WebUI] 默认密码仅显示一次，请保存后及时修改 config.json", "93"))
	}
	logger.Info("[WebUI] 服务已启动，按 Ctrl+C 停止")
}
