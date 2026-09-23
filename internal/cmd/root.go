// Package cmd implements the command-line interface for Velox.
// Velox runs as a dedicated high-performance Web Dashboard by default.
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
	var (
		port   int
		noOpen bool
	)

	rootCmd := &cobra.Command{
		Use:   "velox",
		Short: "Velox — high-performance V2Ray proxy engine & web dashboard",
		Long: `Velox provides an interactive, full-featured web dashboard for managing,
parsing, testing, scoring, connecting, and deep-logging V2Ray proxy configurations.

Running velox launches the local Web Dashboard directly.`,
		PersistentPreRunE: initConfig,
		SilenceUsage:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUI(cmd.Context(), port, !noOpen)
		},
	}

	// Dashboard flags
	rootCmd.Flags().IntVarP(&port, "port", "p", 18080, "web dashboard port")
	rootCmd.Flags().BoolVar(&noOpen, "no-open", false, "do not automatically open the browser")

	// Persistent flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.velox/config.yaml or /etc/velox/config.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().Bool("log-json", false, "output logs in JSON format")
	rootCmd.PersistentFlags().String("data-dir", "", "data directory (default: ~/.velox or /var/lib/velox)")

	// Keep 'ui' / 'dashboard' alias subcommand
	rootCmd.AddCommand(newUICmd())

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
		viper.AddConfigPath(".")
		if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
			viper.AddConfigPath(filepath.Join(homeDir, ".velox"))
			viper.AddConfigPath(filepath.Join(homeDir, ".config", "velox"))
		}
		viper.AddConfigPath("/etc/velox")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix("VELOX")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("read config: %w", err)
		}
	}

	if err := viper.Unmarshal(appCfg); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}

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

	if appCfg.DataDir == "" {
		appCfg.DataDir = config.DefaultDataDir()
	}

	if err := os.MkdirAll(appCfg.DataDir, 0755); err != nil {
		return fmt.Errorf("create data dir %q: %w", appCfg.DataDir, err)
	}

	logger = initLogger(appCfg.LogLevel, appCfg.LogJSON)
	slog.SetDefault(logger)

	return nil
}

// initLogger creates a structured console slog.Logger.
func initLogger(level string, jsonFormat bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLogLevel(level)}

	var handler slog.Handler
	if jsonFormat {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	return slog.New(handler)
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
