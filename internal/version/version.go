// Package version carries the build identity of the running binaries. The
// values are empty in a plain `go build` and are stamped by the release
// pipeline through -ldflags, so a development build is always recognisable as
// one rather than claiming to be a release.
package version

import "runtime/debug"

var (
	// Version is the release tag, without the leading "v".
	Version = ""
	// Commit is the short revision the release was built from.
	Commit = ""
	// Date is the RFC 3339 build timestamp.
	Date = ""
)

// Current returns the release version, falling back to the version Go records
// when the binary was installed with `go install`, and finally to "dev".
func Current() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// String returns the version with the commit and build date when the pipeline
// recorded them, for `nabu version` and support requests.
func String() string {
	out := Current()
	if Commit != "" {
		out += " (" + Commit
		if Date != "" {
			out += ", " + Date
		}
		out += ")"
	}
	return out
}
