package cli

import (
	"context"
	"encoding/json"
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

// Snapshot public branch metadata before adding a test-only proxy. Module zips
// and checksums still come from the public upstream; only the fake providers
// bypass checksum verification. This keeps the fixture independent of stale
// branch responses cached by the fallback proxy during tag publication.
func isolatedDynamicPlatformProxy(t *testing.T, sources ...moduleProxySource) {
	t.Helper()
	targets, err := resolvePreviewModules(context.Background(), []selectedModule{{Path: spineImportPath, Version: "preview"}, {Path: echoPlatformPath, Version: "preview"}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string][]byte{}
	for _, target := range targets {
		output, err := platformGo(context.Background(), t.TempDir(), false, "list", "-m", "-json", target.Path+"@"+target.Version)
		if err != nil {
			t.Fatal(err)
		}
		var info struct {
			Version string
			Time    json.RawMessage
		}
		if err := json.Unmarshal(output, &info); err != nil {
			t.Fatal(err)
		}
		metadata[target.Path], err = json.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
	}
	proxy, err := url.Parse(isolatedModuleProxy(t, sources...))
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range metadata {
		escaped, err := module.EscapePath(path)
		if err != nil {
			t.Fatal(err)
		}
		directory := filepath.Join(filepath.FromSlash(proxy.Path), filepath.FromSlash(escaped), "@v")
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "preview.info"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
