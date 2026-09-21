// Package cmd implements the Cobra CLI commands for Velox.
package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/AmirAM03/velox/internal/config"
)

var (
	cfgFile string
	appCfg  *config.Config
	logger  *slog.Logger
)

// NewRootCmd creates the root Cobra command.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "velox",
		Short: "Velox — high-performance V2Ray proxy config engine",
		Long: `Velox parses, tests, scores, and connects through proxy configurations
at high speed. It supports VMess, VLESS, Trojan, Shadowsocks, and more.

Parse configs from subscription URLs or manual input, test them against
your custom target URLs, and connect through the fastest working proxy.`,
		PersistentPreRunE: initConfig,
		SilenceUsage:      true,
	}

	// Persistent flags (available to all subcommands)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.velox/config.yaml or /etc/velox/config.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().Bool("log-json", false, "output logs in JSON format")
	rootCmd.PersistentFlags().String("data-dir", "", "data directory (default: ~/.velox or /var/lib/velox)")

	// Register subcommands
	rootCmd.AddCommand(newParseCmd())
	rootCmd.AddCommand(newTestCmd())
	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newConnectCmd())
	rootCmd.AddCommand(newDedupCmd())

	return rootCmd
}

// initConfig reads config file, env vars, and initializes logging.
func initConfig(cmd *cobra.Command, args []string) error {
	// Start with defaults
	appCfg = config.DefaultConfig()

	// Setup Viper
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// Search paths for config file:
		// 1. Current directory
		viper.AddConfigPath(".")
		// 2. User home: ~/.velox and ~/.config/velox
		if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
			viper.AddConfigPath(filepath.Join(homeDir, ".velox"))
			viper.AddConfigPath(filepath.Join(homeDir, ".config", "velox"))
		}
		// 3. System-wide config on Linux: /etc/velox
		viper.AddConfigPath("/etc/velox")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	// Env vars with VELOX_ prefix
	viper.SetEnvPrefix("VELOX")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	// Read config file (optional)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("read config: %w", err)
		}
	}

	// Unmarshal into config struct
	if err := viper.Unmarshal(appCfg); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}

	// Explicit CLI flags override config file and defaults
	if cmd.Flags().Changed("data-dir") {
		if val, err := cmd.Flags().GetString("data-dir"); err == nil && val != "" {
			appCfg.DataDir = val
		}
	}
	if cmd.Flags().Changed("log-level") {
		if val, err := cmd.Flags().GetString("log-level"); err == nil && val != "" {
			appCfg.LogLevel = val
		}
	}
	if cmd.Flags().Changed("log-json") {
		if val, err := cmd.Flags().GetBool("log-json"); err == nil {
			appCfg.LogJSON = val
		}
	}

	// Fallback to default data dir if empty
	if appCfg.DataDir == "" {
		appCfg.DataDir = config.DefaultDataDir()
	}

	// Ensure data directory exists
	if err := os.MkdirAll(appCfg.DataDir, 0755); err != nil {
		return fmt.Errorf("create data dir %q: %w", appCfg.DataDir, err)
	}

	// Initialize logger
	logger = initLogger(appCfg.LogLevel, appCfg.LogJSON)
	slog.SetDefault(logger)

	return nil
}

// initLogger creates a structured slog.Logger.
func initLogger(level string, jsonFormat bool) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if jsonFormat {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	return slog.New(handler)
}
