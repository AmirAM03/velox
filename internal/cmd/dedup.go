package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/AmirAM03/velox/internal/storage"
)

func newDedupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dedup",
		Short: "Scan database and eliminate all duplicate proxy configurations",
		Long: `Scans the entire local SQLite database, re-normalizes all proxy configurations
according to their canonical identity hash, merges any duplicate entries, and
ensures zero duplicate nodes exist in storage.

Examples:
  velox dedup
  velox dedup --data-dir ~/.velox`,
		RunE: runDedup,
	}

	return cmd
}

func runDedup(cmd *cobra.Command, args []string) error {
	store, err := storage.Open(appCfg.DBPath(), logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	fmt.Println("🔍 Scanning database for duplicate configurations...")
	scanned, removed, err := store.Deduplicate()
	if err != nil {
		return fmt.Errorf("deduplicate: %w", err)
	}

	if removed > 0 {
		fmt.Printf("🧹 Deduplication complete: scanned %d configs, removed %d duplicates.\n", scanned, removed)
	} else {
		fmt.Printf("✅ Database is clean: scanned %d configs, 0 duplicates found.\n", scanned)
	}

	// Show current summary
	totalCount, _ := store.ConfigCount()
	protoCounts, _ := store.ConfigCountByProtocol()
	if len(protoCounts) > 0 {
		var parts []string
		for proto, count := range protoCounts {
			parts = append(parts, fmt.Sprintf("%s:%d", proto, count))
		}
		fmt.Printf("📊 Database: %d unique configs (%s)\n", totalCount, strings.Join(parts, ", "))
	}

	return nil
}
