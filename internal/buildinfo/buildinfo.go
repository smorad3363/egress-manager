// Package buildinfo exposes build metadata shared by Egress Manager binaries.
package buildinfo

// These values may be replaced with -ldflags in release builds.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
