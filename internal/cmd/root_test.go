package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitConfig_DefaultDataDir(t *testing.T) {
	rootCmd := NewRootCmd()
	rootCmd.SetArgs([]string{"list"})

	if err := initConfig(rootCmd, nil); err != nil {
		t.Fatalf("initConfig failed: %v", err)
	}

	if appCfg == nil {
		t.Fatal("appCfg should not be nil")
	}

	if appCfg.DataDir == "" {
		t.Error("appCfg.DataDir should not be empty")
	}
}

func TestInitConfig_ExplicitDataDirFlag(t *testing.T) {
	customDir := filepath.Join(os.TempDir(), "velox-custom-test-dir")
	defer os.RemoveAll(customDir)

	rootCmd := NewRootCmd()
	rootCmd.SetArgs([]string{"--data-dir", customDir, "list"})
	// Parse flags on rootCmd
	if err := rootCmd.ParseFlags([]string{"--data-dir", customDir}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	if err := initConfig(rootCmd, nil); err != nil {
		t.Fatalf("initConfig with --data-dir failed: %v", err)
	}

	if appCfg.DataDir != customDir {
		t.Errorf("expected appCfg.DataDir to be %q, got %q", customDir, appCfg.DataDir)
	}
}
