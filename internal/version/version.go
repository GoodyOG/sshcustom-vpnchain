// Package version exposes the SSHCustom-VPNChain version string baked into the
// daemon binary at build time. The value comes from the repository-root VERSION
// file via -ldflags="-X github.com/GoodyOG/SSHCustom_Magisk/internal/version.Version=$(cat VERSION)".
//
// Tools that need the version (Magisk module.prop generation, GitHub Actions
// release naming, the app's versionName) read the same VERSION file. There is
// only one place to edit when bumping a release.
package version

// Version is overridden by -ldflags at build time. The fallback "0.0.0-dev"
// makes uncoordinated `go run` / `go build` invocations identifiable.
var Version = "0.0.0-dev"
