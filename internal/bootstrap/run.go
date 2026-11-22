package bootstrap

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"easybot/internal/adapter/onebot11"
	"easybot/internal/api"
	"easybot/internal/application"
	"easybot/internal/application/events"
	"easybot/internal/bridge"
	"easybot/internal/config"
	"easybot/internal/runtime"
	"easybot/internal/store"
	"easybot/internal/webui"
)

// Run loads modules, serves both listeners, and returns the process exit code.
func Run() (code int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			consoleLogger().Error("程序异常退出", "error", fmt.Errorf("%v", recovered))
			code = 1
		}
	}()
	bootstrapLogger := consoleLogger()
	started := time.Now()
	settings, initialPassword, err := config.Load()
	if err != nil {
		bootstrapLogger.Error("启动配置读取失败", "error", err)
		return 1
	}
	logs := runtime.NewLogs(500, consoleLogger(settings.LogLevel))
	logger := logs.New("daemon")
	configLogger := logs.New("config")
	printBanner(logger)
	if initialPassword {
		configLogger.Info("首次启动已创建配置文件并生成默认管理密码", "path", settings.Path)
	} else {
		configLogger.Info("已加载守护进程配置", "path", settings.Path)
	}
	for name, value := range map[string]string{
		"EASYBOT_ADDR":          settings.Address,
		"EASYBOT_SQLITE_PATH":   settings.SQLitePath,
		"EASYBOT_ALLOWED_HOSTS": settings.AllowedHosts,
	} {
		if err := os.Setenv(name, value); err != nil {
			logger.Error("配置应用失败", "error", err)
			return 1
		}
	}
	databaseLogger := logger.WithGroup("DataBase")
	databaseLogger.Info("正在初始化数据库...")
	db, err := store.Open(settings.Database)
	if err != nil {
		databaseLogger.Error("数据库初始化失败", "error", err)
		return 1
	}
	defer db.Close()
	databaseLogger.Info("初始化完成，已连接 " + db.Dialect())

	address := settings.Address
	cert, key := settings.Certificate, settings.PrivateKey
	if err := config.ValidateListener(address, cert, key); err != nil {
		logger.Error("监听配置无效: "+address, "error", err)
		return 1
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		logger.Error("服务监听失败", "address", address, "error", err)
		return 1
	}
	defer listener.Close()
	bridgeListener, err := net.Listen("tcp", settings.BridgeAddress)
	if err != nil {
		logger.Error("Bridge 监听失败", "address", settings.BridgeAddress, "error", err)
		return 1
	}
	defer bridgeListener.Close()
	authLogger := logger.WithGroup("Authentication")
	admin, err := db.EnsureConfigAdmin(context.Background(), settings.Password)
	if err != nil {
		authLogger.Error("管理身份初始化失败", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	authLogger.Info("管理密码认证初始化完成")
	defer stop()
	logLogger := logger.WithGroup("Log")
	logLogger.Info("正在初始化运行日志...")
	logSettings, err := db.GetLogSettings(context.Background())
	if err != nil {
		logLogger.Error("日志设置初始化失败", "error", err)
		return 1
	}
	writer := runtime.NewAsyncLogWriter(db, 2048)
	writer.SetEnabled(logSettings.Persistent)
	writer.SetMinLevel(logSettings.Level)
	logs.SetSink(writer.TryWrite)
	if logSettings.Persistent {
		logLogger.Info(fmt.Sprintf("持久化已启用，保留 %d 天，最多 %d 条", logSettings.RetentionDays, logSettings.MaxRows))
	} else {
		logLogger.Info("持久化已关闭，使用内存日志")
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		writer.Close(flushCtx)
	}()
	maintenanceCtx, stopMaintenance := context.WithCancel(ctx)
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		maintainLogs(maintenanceCtx, db, logs.New("logs"))
	}()
	defer func() {
		stopMaintenance()
		<-maintenanceDone
	}()
	bridgeLogger := logs.New("bridge")
	handlers := events.New(ctx, db, application.NewBindings(db), logs.New("adapter"), bridgeLogger)
	adapters := onebot11.NewManager(ctx, db, handlers.OnSocialEvent)
	bridges := bridge.NewManager(db, bridgeLogger, handlers.BridgeCallbacks())
	defer bridges.Close()
	handlers.Connect(adapters, bridge.NewRouter(db, bridges, adapters, bridgeLogger))
	if err := adapters.StartEnabled(ctx); err != nil {
		logger.Error("适配器初始化失败", "error", err)
	} else if count := adapters.RuntimeCount(); count > 0 {
		logger.Info(fmt.Sprintf("已初始化 %d 个适配器实例", count))
	}
	apiHandler := api.NewForListenerWithRuntimes(db, logs.New("api"), logs, adapters, bridges, listener.Addr().String(), settings.Password, admin.ID, writer)
	server := &http.Server{
		Addr:              address,
		Handler:           webui.New(apiHandler),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	bridgeMux := http.NewServeMux()
	bridgeMux.Handle("/bridge", bridges)
	bridgeServer := &http.Server{Addr: settings.BridgeAddress, Handler: bridgeMux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	logs.New("web").Info("管理路由初始化完成")
	printStartup(logs.New("web"), listener.Addr().String(), cert != "", settings.Password, initialPassword)
	scheme := "ws"
	if settings.BridgeCertificate != "" {
		scheme = "wss"
	}
	logs.New("bridge").Info("监听地址: " + scheme + "://" + bridgeListener.Addr().String() + "/bridge")
	logger.WithGroup("Startup").Info(fmt.Sprintf("初始化完成，耗时 %d ms", time.Since(started).Milliseconds()))
	logger.Info("管理界面及后台服务初始化完成")
	serveErr := make(chan error, 2)
	go func() {
		if cert != "" {
			serveErr <- server.ServeTLS(listener, cert, key)
			return
		}
		serveErr <- server.Serve(listener)
	}()
	go func() {
		if settings.BridgeCertificate != "" {
			serveErr <- bridgeServer.ServeTLS(bridgeListener, settings.BridgeCertificate, settings.BridgePrivateKey)
			return
		}
		serveErr <- bridgeServer.Serve(bridgeListener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		bridges.Close()
		adapters.Shutdown(shutdownCtx)
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("管理服务关闭失败", "error", err)
		}
		if err := bridgeServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Bridge 服务关闭失败", "error", err)
		}
		logger.Info("守护进程服务已停止")
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("服务已停止", "error", err)
			return 1
		}
	}
	return 0
}
