package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/pipeline"
	"github.com/AmirAM03/velox/internal/scorer"
	"github.com/AmirAM03/velox/internal/storage"
)

func newTestCmd() *cobra.Command {
	var (
		targetURLs []string
		limit      int
		protocol   string
	)

	cmd := &cobra.Command{
		Use:   "test",
		Short: "Test stored proxy configs through the pipeline",
		Long: `Run the multi-stage test pipeline against stored proxy configs.
Tests flow through stages: DNS+TCP → TLS → Proxy (HTTP through proxy).

Configs that pass all stages are scored and ranked.

Examples:
  velox test
  velox test --target "https://www.google.com" --target "https://cloudflare.com"
  velox test --limit 100
  velox test --protocol vless`,
		RunE: runTest,
	}

	cmd.Flags().StringArrayVarP(&targetURLs, "target", "t", nil, "target URL(s) for proxy testing (overrides config)")
	cmd.Flags().IntVarP(&limit, "limit", "l", 0, "max number of configs to test (0 = all)")
	cmd.Flags().StringVarP(&protocol, "protocol", "p", "", "filter by protocol (vmess, vless, trojan, ss)")

	return cmd
}

func runTest(cmd *cobra.Command, args []string) error {
	targetURLs, _ := cmd.Flags().GetStringArray("target")
	limit, _ := cmd.Flags().GetInt("limit")
	protocol, _ := cmd.Flags().GetString("protocol")

	// Open database
	store, err := storage.Open(appCfg.DBPath(), logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	// Load configs
	fetchLimit := limit
	if fetchLimit <= 0 {
		fetchLimit = 10000 // reasonable upper bound
	}
	configs, err := store.ListConfigs(fetchLimit)
	if err != nil {
		return fmt.Errorf("load configs: %w", err)
	}

	// Filter by protocol if specified
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

	if len(configs) == 0 {
		fmt.Println("No configs to test. Run 'velox parse' first.")
		return nil
	}

	fmt.Printf("🔬 Testing %d configs...\n\n", len(configs))

	// Determine target URLs
	targets := appCfg.Targets.URLs
	if len(targetURLs) > 0 {
		targets = targetURLs
	}
	fmt.Printf("🎯 Targets: %s\n", strings.Join(targets, ", "))
	fmt.Printf("⚙️  Pipeline: S0(%d workers) → S1(%d) → S2(%d)\n\n",
		appCfg.Pipeline.Stage0Workers,
		appCfg.Pipeline.Stage1Workers,
		appCfg.Pipeline.Stage2Workers,
	)

	// Create Xray-core engine for in-process proxy dialing
	eng := engine.NewXrayEngine(logger)
	defer eng.Close()

	// Create and run pipeline
	p := pipeline.New(&appCfg.Pipeline, eng, targets, appCfg.Targets.ExpectStatus, logger)

	startTime := time.Now()
	results := p.Run(cmd.Context(), configs)
	elapsed := time.Since(startTime)

	// Summarize results
	var passed, failed int
	var stageFailCounts [4]int

	for _, r := range results {
		if r.Failed {
			failed++
			if r.FailedStage >= 0 && int(r.FailedStage) < len(stageFailCounts) {
				stageFailCounts[r.FailedStage]++
			}
		} else {
			passed++
		}
	}

	fmt.Printf("\n📊 Results (%.1fs):\n", elapsed.Seconds())
	fmt.Printf("   ✅ Passed: %d\n", passed)
	fmt.Printf("   ❌ Failed: %d\n", failed)
	fmt.Printf("      Stage 0 (DNS+TCP): %d\n", stageFailCounts[0])
	fmt.Printf("      Stage 1 (TLS):     %d\n", stageFailCounts[1])
	fmt.Printf("      Stage 2 (Proxy):   %d\n", stageFailCounts[2])

	// Score and store results
	sc := scorer.New(appCfg.Scoring)

	for _, r := range results {
		// Store test results
		if err := store.InsertTestResults(r.Results); err != nil {
			logger.Warn("failed to store test results", "error", err, "config", r.Config.ID)
		}

		// Compute and store score
		existing, _ := store.GetScore(r.Config.ID)
		score := sc.Compute(r.Config.ID, r.Results, existing)
		if err := store.UpsertScore(score); err != nil {
			logger.Warn("failed to store score", "error", err, "config", r.Config.ID)
		}
	}

	// Print top 10 passing configs
	if passed > 0 {
		fmt.Printf("\n🏆 Top configs:\n")
		topConfigs, _ := store.ListConfigs(10)
		for i, cfg := range topConfigs {
			score, _ := store.GetScore(cfg.ID)
			scoreStr := "n/a"
			latencyStr := "n/a"
			if score != nil {
				scoreStr = fmt.Sprintf("%.4f", score.Composite)
				latencyStr = fmt.Sprintf("%.0fms", score.LatencyScore*10000)
			}
			fmt.Printf("   %2d. [%s] %s — score: %s, latency: %s\n",
				i+1, cfg.Protocol, cfg.DisplayName(), scoreStr, latencyStr)
		}
	}

	return nil
}
