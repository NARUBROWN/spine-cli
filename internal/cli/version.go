package cli

import (
	"runtime/debug"
	"strings"

	"golang.org/x/mod/semver"
)

const developmentVersion = "0.4.5-dev"

func installedVersion() string {
	info, ok := debug.ReadBuildInfo()
	return versionFromBuildInfo(info, ok)
}

// 공개 설치는 태그와 pseudo-version을 표시하고 로컬 빌드는 개발 버전으로 구분합니다.
func versionFromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if ok && info != nil && info.Main.Path == "github.com/NARUBROWN/spine-cli" && semver.IsValid(info.Main.Version) {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return developmentVersion
}
