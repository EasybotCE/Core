// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/EasybotCE/easybotce/internal/app"
	"github.com/EasybotCE/easybotce/internal/buildinfo"
	"github.com/EasybotCE/easybotce/internal/config"
)

func main() {
	configPath := flag.String("config", "", "read bootstrap YAML from this file (no automatic discovery)")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "hub accepts no positional arguments")
		os.Exit(2)
	}
	if *version {
		fmt.Printf("EasybotCE %s (%s)\n", buildinfo.Version, buildinfo.Milestone)
		return
	}
	if err := run(*configPath); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("hub exited", "error", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.LoadFile(configPath, os.LookupEnv)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return app.Run(ctx, cfg, logger)
}
