// Package build contains build-time injected variables.
package build

// Version is the semantic version, injected at build time via ldflags.
var Version = "dev"

// Time is the build timestamp, injected at build time via ldflags.
var Time = "unknown"
