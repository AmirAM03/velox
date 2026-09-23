package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	loggerPkg "github.com/AmirAM03/velox/internal/logger"
	"github.com/AmirAM03/velox/internal/storage"
	"github.com/AmirAM03/velox/internal/web"
)

func newUICmd() *cobra.Command {
	var (
		port   int
		noOpen bool
	)

	cmd := &cobra.Command{
		Use:     "ui",
		Aliases: []string{"dashboard", "web"},
		Short:   "Launch the interactive Web Dashboard",
		Long: `Start the local web dashboard offering complete visual control over all Velox features:
  - Configuration Explorer & live inspection
  - Subscription fetching and manual copy-paste with automatic deduplication
  - 3-stage latency benchmarks against custom target URLs
  - One-click proxy connection, system proxy control, and database maintenance
  - In-depth application logs with search, level filters, retention policies, and export`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUI(cmd.Context(), port, !noOpen)
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 18080, "web dashboard port")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not automatically open the browser")

	return cmd
}

func runUI(ctx context.Context, port int, openBrowser bool) error {
	store, err := storage.Open(appCfg.DBPath(), logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	// Initialize DB logging handler to persist all slog events to SQLite
	lvl := parseLogLevel(appCfg.LogLevel)
	dbHandler := loggerPkg.NewDBHandler(loggerPkg.Config{
		Store:      store,
		Level:      lvl,
		JSONFormat: appCfg.LogJSON,
	})
	defer dbHandler.Close()

	appLogger := slog.New(dbHandler)
	slog.SetDefault(appLogger)
	logger = appLogger

	// Start background log retention pruner
	loggerPkg.StartPruner(ctx, store, appLogger, 1*time.Hour)

	srv := web.NewServer(appCfg, store, port, appLogger, dbHandler)

	// Listen for termination signals
	sigCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return srv.Start(sigCtx, openBrowser)
}
