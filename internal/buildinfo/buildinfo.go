// Package buildinfo holds build-time metadata, overridable via -ldflags
// (e.g. -X github.com/meshcore-go/OwlShack/internal/buildinfo.Version=v1.2.3).
package buildinfo

// Version is the build version, set at release time and defaulting to "dev".
var Version = "dev"
