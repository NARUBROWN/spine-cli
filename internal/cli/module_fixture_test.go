package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var testModuleCache struct {
	sync.Once
	err error
}

// 정적 분석 fixture는 미리 준비한 공개 모듈 캐시를 사용합니다.
// 제품의 --skip-install과 오프라인 조회에서는 이 준비 코드를 실행하지 않습니다.
func prepareTestModuleCache() error {
	testModuleCache.Do(func() {
		root, err := os.MkdirTemp("", "spine-test-modules-")
		if err != nil {
			testModuleCache.err = err
			return
		}
		defer os.RemoveAll(root)
		for _, requirements := range []string{fmt.Sprintf("require (\n%s %s\n%s %s\n)\n", spineImportPath, platformSpineVersion, echoPlatformPath, echoPlatformVersion), "require " + spineImportPath + " v0.5.2\n"} {
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/fixture\n\ngo 1.25.5\n\n"+requirements), 0o600); err != nil {
				testModuleCache.err = err
				return
			}
			if _, err := platformGo(context.Background(), root, false, "mod", "download", "all"); err != nil {
				testModuleCache.err = err
				return
			}
		}
	})
	return testModuleCache.err
}
