package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/storage"
)

func newListCmd() *cobra.Command {
	var (
		limit    int
		protocol string
		showURI  bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List stored proxy configs ranked by score",
		Long: `Show all stored proxy configs ordered by composite score (best first).

Examples:
  velox list
  velox list --limit 20
  velox list --protocol vless
  velox list --show-uri`,
		RunE: runList,
	}

	cmd.Flags().IntVarP(&limit, "limit", "l", 50, "max number of configs to show")
	cmd.Flags().StringVarP(&protocol, "protocol", "p", "", "filter by protocol")
	cmd.Flags().BoolVar(&showURI, "show-uri", false, "show raw URI for each config")

	return cmd
}

func runList(cmd *cobra.Command, args []string) error {
	limit, _ := cmd.Flags().GetInt("limit")
	protocol, _ := cmd.Flags().GetString("protocol")
	showURI, _ := cmd.Flags().GetBool("show-uri")

	// Open database
	store, err := storage.Open(appCfg.DBPath(), logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	// Get total count
	totalCount, _ := store.ConfigCount()
	protoCounts, _ := store.ConfigCountByProtocol()

	fmt.Printf("📦 Database: %d configs", totalCount)
	if len(protoCounts) > 0 {
		var parts []string
		for proto, count := range protoCounts {
			parts = append(parts, fmt.Sprintf("%s:%d", proto, count))
		}
		fmt.Printf(" (%s)", strings.Join(parts, ", "))
	}
	fmt.Println()

	if totalCount == 0 {
		fmt.Println("\nNo configs stored. Run 'velox parse' first.")
		return nil
	}

	// Load configs
	configs, err := store.ListConfigs(limit)
	if err != nil {
		return fmt.Errorf("load configs: %w", err)
	}

	// Filter by protocol
	if protocol != "" {
		protocol = strings.ToLower(protocol)
		var filtered []*model.ProxyConfig
		for _, cfg := range configs {
			if string(cfg.Protocol) == protocol {
				filtered = append(filtered, cfg)
			}
		}
		configs = filtered
	}

	fmt.Printf("\n%-4s %-8s %-40s %-22s %-10s\n", "#", "Proto", "Name", "Server", "Score")
	fmt.Println(strings.Repeat("─", 90))

	for i, cfg := range configs {
		score, _ := store.GetScore(cfg.ID)
		scoreStr := "  —"
		if score != nil {
			scoreStr = fmt.Sprintf("%.4f", score.Composite)
		}

		name := cfg.DisplayName()
		if len(name) > 38 {
			name = name[:35] + "..."
		}

		server := fmt.Sprintf("%s:%d", cfg.Address, cfg.Port)
		if len(server) > 20 {
			server = server[:17] + "..."
		}

		fmt.Printf("%-4d %-8s %-40s %-22s %-10s\n",
			i+1, cfg.Protocol, name, server, scoreStr)

		if showURI && cfg.RawURI != "" {
			uri := cfg.RawURI
			if len(uri) > 100 {
				uri = uri[:97] + "..."
			}
			fmt.Printf("     └─ %s\n", uri)
		}
	}

	return nil
}
