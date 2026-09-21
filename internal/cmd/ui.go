package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
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

Examples:
  velox ui
  velox dashboard
  velox ui --port 18080 --no-open`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUI(cmd.Context(), port, !noOpen)
		},
	}

	cmd.Flags().IntVarP(&port, "port", "P", 18080, "web dashboard port")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not automatically open the browser")

	return cmd
}

func runUI(ctx context.Context, port int, openBrowser bool) error {
	store, err := storage.Open(appCfg.DBPath(), logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	srv := web.NewServer(appCfg, store, port, logger)

	// Listen for termination signals
	sigCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return srv.Start(sigCtx, openBrowser)
}
