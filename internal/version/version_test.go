package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestGetReportsRuntimeIdentity(t *testing.T) {
	info := Get()

	if info.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; info.Platform() != want {
		t.Errorf("Platform() = %q, want %q", info.Platform(), want)
	}
	if info.Version == "" {
		t.Error("Version is empty; the link-time default was lost")
	}
}

func TestStringIncludesBuildIdentity(t *testing.T) {
	info := Get()
	got := info.String()

	for _, want := range []string{info.Version, info.Commit, info.Date, info.GoVersion, info.Platform()} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}
