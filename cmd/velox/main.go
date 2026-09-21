package main

import (
	"fmt"
	"os"

	"github.com/AmirAM03/velox/internal/build"
	"github.com/AmirAM03/velox/internal/cmd"
)

func main() {
	rootCmd := cmd.NewRootCmd()

	// Version command
	rootCmd.Version = fmt.Sprintf("%s (built %s)", build.Version, build.Time)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
