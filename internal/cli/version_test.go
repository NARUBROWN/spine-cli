package cli

import (
	"runtime/debug"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	for _, tag := range []string{"v0.4.3-preview.1", "v0.4.3-rc.2", "v0.4.3", "v0.4.3-0.20261103042116-77419ec0a2d2"} {
		info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/NARUBROWN/spine-cli", Version: tag}}
		if got := versionFromBuildInfo(info, true); got != tag[1:] {
			t.Errorf("installed %s reports %s", tag, got)
		}
	}
	for _, tag := range []string{"", "(devel)", "preview", "--help"} {
		info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/NARUBROWN/spine-cli", Version: tag}}
		if got := versionFromBuildInfo(info, true); got != developmentVersion {
			t.Errorf("local build %q reports %s", tag, got)
		}
	}
	if got := versionFromBuildInfo(nil, false); got != developmentVersion {
		t.Fatal(got)
	}
	info := &debug.BuildInfo{Main: debug.Module{Path: "example.com/other", Version: "v1.0.0"}}
	if got := versionFromBuildInfo(info, true); got != developmentVersion {
		t.Fatal(got)
	}
}
