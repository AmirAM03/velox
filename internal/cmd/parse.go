package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/AmirAM03/velox/internal/ingest"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/parser"
	"github.com/AmirAM03/velox/internal/storage"
)

func newParseCmd() *cobra.Command {
	var (
		subURLs   []string
		inputFile string
		source    string
	)

	cmd := &cobra.Command{
		Use:   "parse",
		Short: "Parse proxy configs from subscription URLs or input",
		Long: `Parse proxy configs from one or more subscription URLs, a file, or stdin.
Configs are deduplicated, normalized, and stored in the local database.

Examples:
  velox parse --sub "https://example.com/sub"
  velox parse --sub "https://sub1.com" --sub "https://sub2.com"
  velox parse --file configs.txt
  cat configs.txt | velox parse
  echo "vmess://..." | velox parse`,
		RunE: runParse,
	}

	cmd.Flags().StringArrayVar(&subURLs, "sub", nil, "subscription URL(s) to fetch")
	cmd.Flags().StringVarP(&inputFile, "file", "f", "", "file containing proxy config URIs")
	cmd.Flags().StringVar(&source, "source", "", "source label for tracking (default: auto-detect)")

	return cmd
}

func runParse(cmd *cobra.Command, args []string) error {
	subURLs, _ := cmd.Flags().GetStringArray("sub")
	inputFile, _ := cmd.Flags().GetString("file")
	sourceLabel, _ := cmd.Flags().GetString("source")

	var allRaw []string

	// Fetch from subscription URLs
	for _, url := range subURLs {
		logger.Info("fetching subscription", "url", url)
		raw, err := ingest.FetchSubscription(url, 0)
		if err != nil {
			logger.Error("failed to fetch subscription", "url", url, "error", err)
			continue
		}
		if sourceLabel == "" {
			sourceLabel = "sub:" + url
		}
		allRaw = append(allRaw, raw)
		logger.Info("fetched subscription", "url", url, "bytes", len(raw))
	}

	// Read from file
	if inputFile != "" {
		data, err := os.ReadFile(inputFile)
		if err != nil {
			return fmt.Errorf("read file %q: %w", inputFile, err)
		}
		if sourceLabel == "" {
			sourceLabel = "file:" + inputFile
		}
		allRaw = append(allRaw, string(data))
		logger.Info("read file", "file", inputFile, "bytes", len(data))
	}

	// Command-line arguments (manual paste e.g. velox parse "vless://..." "vmess://...")
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			allRaw = append(allRaw, arg)
			if sourceLabel == "" {
				sourceLabel = "paste"
			}
		}
	}

	// Read from stdin if no other input provided
	if len(subURLs) == 0 && inputFile == "" && len(args) == 0 {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			// stdin has piped data
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			if sourceLabel == "" {
				sourceLabel = "stdin"
			}
			allRaw = append(allRaw, string(data))
			logger.Info("read stdin", "bytes", len(data))
		} else {
			// Interactive terminal prompt for manual paste
			fmt.Println("📋 Paste your proxy configs below (press Ctrl+Z on Windows or Ctrl+D on Linux, then Enter to finish):")
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("read paste: %w", err)
			}
			if sourceLabel == "" {
				sourceLabel = "paste"
			}
			allRaw = append(allRaw, string(data))
		}
	}

	// Merge and clean raw content
	merged := strings.Join(allRaw, "\n")
	cleaned := ingest.CleanRawContent(merged)

	// Parse all configs with detailed duplicate tracking
	configs, failures, batchDups := parser.ParseManyDetailed(cleaned)

	logger.Info("parsing complete",
		"unique", len(configs),
		"batch_duplicates", batchDups,
		"failed", failures,
		"protocols", summarizeProtocols(configs),
	)

	if len(configs) == 0 {
		if failures > 0 {
			fmt.Printf("⚠️ No valid configs found (%d failed).\n", failures)
		} else {
			fmt.Println("No valid configs found.")
		}
		return nil
	}

	// Set source on all configs
	for _, cfg := range configs {
		cfg.Source = sourceLabel
	}

	// Store in database
	store, err := storage.Open(appCfg.DBPath(), logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	inserted, updated, err := store.UpsertConfigs(configs)
	if err != nil {
		return fmt.Errorf("store configs: %w", err)
	}

	// Print summary
	fmt.Printf("\n✅ Processed %d unique configs (%d new, %d existing/updated)\n", len(configs), inserted, updated)
	if batchDups > 0 {
		fmt.Printf("   Duplicates skipped in batch: %d\n", batchDups)
	}
	if failures > 0 {
		fmt.Printf("   Failed lines: %d\n", failures)
	}

	// Print protocol breakdown
	protoCount := make(map[string]int)
	for _, cfg := range configs {
		protoCount[string(cfg.Protocol)]++
	}
	for proto, count := range protoCount {
		fmt.Printf("   %s: %d\n", proto, count)
	}

	// Print total in database
	totalCount, err := store.ConfigCount()
	if err == nil {
		fmt.Printf("\n📊 Total unique configs in database: %d (0 duplicates)\n", totalCount)
	}

	return nil
}

// summarizeProtocols returns a concise string like "vmess:5, vless:3"
func summarizeProtocols(configs []*model.ProxyConfig) string {
	counts := make(map[string]int)
	for _, cfg := range configs {
		counts[string(cfg.Protocol)]++
	}
	var parts []string
	for proto, count := range counts {
		parts = append(parts, fmt.Sprintf("%s:%d", proto, count))
	}
	return strings.Join(parts, ", ")
}
