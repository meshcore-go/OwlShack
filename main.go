package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/meshcore-go/meshcore-bot/internal/app"
	"github.com/meshcore-go/meshcore-bot/internal/buildinfo"
	"github.com/meshcore-go/meshcore-bot/internal/config"
	"github.com/meshcore-go/meshcore-bot/internal/logging"
	flag "github.com/spf13/pflag"
)

func main() {
	configPath := flag.StringP("config", "c", "", "path to config file (toml, yaml, or json)")
	showVersion := flag.BoolP("version", "V", false, "print version and exit")
	verbosity := flag.CountP("verbose", "v", "increase log verbosity (-v=debug, -vv=trace, -vvv=trace+)")
	flag.Parse()

	if *showVersion {
		fmt.Println("meshcore-bot", buildinfo.Version)
		return
	}

	cfg, resolvedPath, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(cfg.Companions) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no companions configured")
		os.Exit(1)
	}

	logLevel := ""
	if cfg.LogLevel != nil {
		logLevel = *cfg.LogLevel
	}
	logging.Configure(*verbosity, logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, cfg, resolvedPath); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
