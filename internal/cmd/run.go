package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	loggerPkg "github.com/AmirAM03/velox/internal/logger"
	"github.com/AmirAM03/velox/internal/storage"
	"github.com/AmirAM03/velox/internal/web"
)

func runUI(ctx context.Context, port int) error {
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

	return srv.Start(sigCtx)
}
