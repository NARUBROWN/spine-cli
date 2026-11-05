package cli

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

type moduleProxySource struct{ Path, Version, Dir string }

// 테스트 전용 프록시이며 로컬 소스를 공개 모듈 설치 증거로 해석하지 않습니다.
func isolatedModuleProxy(t *testing.T, sources ...moduleProxySource) string {
	t.Helper()
	proxy := t.TempDir()
	var paths []string
	for _, source := range sources {
		root, err := filepath.Abs(source.Dir)
		if err != nil {
			t.Fatal(err)
		}
		mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		path, err := module.EscapePath(source.Path)
		if err != nil {
			t.Fatal(err)
		}
		directory := filepath.Join(proxy, filepath.FromSlash(path), "@v")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		info := fmt.Sprintf(`{"Version":%q,"Time":"2026-11-05T00:00:00Z"}`, source.Version)
		for name, contents := range map[string][]byte{
			"preview.info": []byte(info), source.Version + ".info": []byte(info),
			source.Version + ".mod": mod, "list": []byte(source.Version + "\n"),
		} {
			if err := os.WriteFile(filepath.Join(directory, name), contents, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		archive, err := os.Create(filepath.Join(directory, source.Version+".zip"))
		if err != nil {
			t.Fatal(err)
		}
		err = modzip.CreateFromDir(archive, module.Version{Path: source.Path, Version: source.Version}, root)
		closeErr := archive.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("prepare module archive: %v %v", err, closeErr)
		}
		paths = append(paths, source.Path)
	}
	proxyURL := url.URL{Scheme: "file", Path: filepath.ToSlash(proxy)}
	if filepath.VolumeName(proxy) != "" {
		proxyURL.Path = "/" + proxyURL.Path
	}
	upstream := os.Getenv("GOPROXY")
	if upstream == "" {
		upstream = "https://proxy.golang.org,direct"
	}
	t.Setenv("GOPROXY", proxyURL.String()+","+upstream)
	t.Setenv("GONOSUMDB", strings.Trim(os.Getenv("GONOSUMDB")+","+strings.Join(paths, ","), ","))
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	t.Cleanup(func() {
		if err := filepath.WalkDir(cache, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return nil
		}); err != nil {
			t.Error(err)
		}
	})
	return proxyURL.String()
}
