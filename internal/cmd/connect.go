package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/proxy"
	"github.com/AmirAM03/velox/internal/storage"
	"github.com/AmirAM03/velox/internal/system"
)

func newConnectCmd() *cobra.Command {
	var (
		configID     string
		port         int
		enableSystem bool
		healthCheck  bool
		poolSize     int
	)

	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Start local proxy connected through the best config",
		Long: `Start a local mixed SOCKS5 and HTTP proxy server using the best-scored
proxy config from your database.

Automatic health checks run periodically. If the active config fails,
Velox automatically fails over to the next best config in your warm pool.

Examples:
  velox connect
  velox connect --port 1080 --system
  velox connect --id <config-id>
  velox connect --system`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnect(cmd.Context(), configID, port, enableSystem, healthCheck, poolSize)
		},
	}

	cmd.Flags().StringVar(&configID, "id", "", "connect to specific config ID (default: best ranked)")
	cmd.Flags().IntVarP(&port, "port", "P", 0, "local proxy port (default: from config, e.g. 1080)")
	cmd.Flags().BoolVarP(&enableSystem, "system", "s", false, "configure OS system proxy")
	cmd.Flags().BoolVar(&healthCheck, "health", true, "enable periodic health check and failover")
	cmd.Flags().IntVar(&poolSize, "pool-size", 5, "warm pool size for failover")

	return cmd
}

func runConnect(ctx context.Context, configID string, port int, enableSystem, healthCheck bool, poolSize int) error {
	if port <= 0 {
		port = appCfg.Proxy.MixedPort
	}
	if port <= 0 {
		port = 1080
	}

	// 1. Open database
	store, err := storage.Open(appCfg.DBPath(), logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	// 2. Select initial config
	var initialConfig *model.ProxyConfig
	if configID != "" {
		configs, err := store.GetConfigsByIDs([]string{configID})
		if err != nil || len(configs) == 0 {
			return fmt.Errorf("config %q not found in database", configID)
		}
		initialConfig = configs[0]
	} else {
		configs, err := store.ListConfigs(1)
		if err != nil || len(configs) == 0 {
			return fmt.Errorf("no configs in database. Run 'velox parse' and 'velox test' first")
		}
		initialConfig = configs[0]
	}

	// 3. Initialize Xray engine
	eng := engine.NewXrayEngine(logger)
	defer eng.Close()

	// 4. Initialize Selector with warm pool
	sel := proxy.NewSelector(store, poolSize, appCfg.Proxy.FailThreshold, logger)
	if err := sel.RefreshWarmPool(); err != nil {
		logger.Warn("warm pool refresh error", "error", err)
	}
	if configID != "" {
		sel.SelectExplicitly(initialConfig)
	}

	active := sel.Active()
	if active == nil {
		return fmt.Errorf("unable to determine active config")
	}

	// 5. Start mixed SOCKS5+HTTP proxy server
	srv := proxy.NewServer(appCfg.Proxy.ListenAddr, port, eng, sel, logger)
	if err := srv.Start(); err != nil {
		return fmt.Errorf("start proxy server: %w", err)
	}
	defer srv.Stop()

	// 6. Start health checker & failover loop if enabled
	var hc *proxy.HealthChecker
	if healthCheck {
		hc = proxy.NewHealthChecker(
			sel,
			eng,
			appCfg.Targets.URLs,
			appCfg.Targets.ExpectStatus,
			appCfg.Proxy.HealthInterval,
			logger,
		)
		hc.Start(ctx)
		defer hc.Stop()
	}

	// 7. System proxy configuration
	sysProxy := system.NewProxyController(logger)
	if enableSystem {
		if err := sysProxy.Enable(appCfg.Proxy.ListenAddr, port, port); err != nil {
			logger.Warn("failed to set system proxy", "error", err)
		} else {
			fmt.Printf("🌐 System proxy enabled: %s:%d\n", appCfg.Proxy.ListenAddr, port)
		}
		defer func() {
			_ = sysProxy.Disable()
			fmt.Println("🌐 System proxy disabled")
		}()
	}

	// Print connection banner
	fmt.Printf("\n🚀 Velox Proxy Connected!\n")
	fmt.Printf("──────────────────────────────────────────────────\n")
	fmt.Printf("   Active Node:   [%s] %s\n", active.Protocol, active.DisplayName())
	fmt.Printf("   Server:        %s:%d\n", active.Address, active.Port)
	fmt.Printf("   Local SOCKS5:  socks5://%s:%d\n", appCfg.Proxy.ListenAddr, port)
	fmt.Printf("   Local HTTP:    http://%s:%d\n", appCfg.Proxy.ListenAddr, port)
	if healthCheck {
		fmt.Printf("   Health Check:  active (every %v, failover pool size: %d)\n",
			appCfg.Proxy.HealthInterval, poolSize)
	}
	fmt.Printf("──────────────────────────────────────────────────\n")
	fmt.Printf("Press Ctrl+C to disconnect.\n\n")

	// 8. Wait for OS interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
	case <-ctx.Done():
		fmt.Println("\nContext cancelled, shutting down...")
	}

	return nil
}
