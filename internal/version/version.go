// Package version holds the build version, stamped at compile time via
// -ldflags "-X github.com/djdembeck/reddit-upvote-media-downloader/internal/version.Version=<ver>".
// The default "devel" is used for local and CI builds.
package version

// Version is overwritten at build time for release images. It is a var (not
// const) because -ldflags -X can only inject into string vars.
var Version = "devel"
