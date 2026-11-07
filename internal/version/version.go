// Package version exposes the build version for logs and diagnostics.
package version

// Version can be stamped at build time with:
//
// go build -ldflags "-X pikpak2directlink/internal/version.Version=personal-v1" ./cmd/server
var Version = "personal-dev"
