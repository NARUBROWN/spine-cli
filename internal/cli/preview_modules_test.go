package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/module"
)

func TestPreviewBranchMovementInvalidatesGenerationPlan(t *testing.T) {
	inTempDir(t)
	resolve := previewModuleResolver
	version := currentAssemblySpineVersion
	previewModuleResolver = func(ctx context.Context, targets []selectedModule) ([]selectedModule, error) {
		resolved, err := resolve(ctx, targets)
		for index := range resolved {
			if resolved[index].Path == spineImportPath {
				resolved[index].Version = version
			}
		}
		return resolved, err
	}
	var out, errOut bytes.Buffer
	args := []string{"sample", "--skip-install", "--json"}
	if code := runNew(append(args, "--dry-run"), &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	var plan map[string]any
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	version = "v0.7.0-preview.3"
	out.Reset()
	errOut.Reset()
	if code := runNew(append(args, "--if-match", plan["base_hash"].(string)), &out, &errOut); code != 4 {
		t.Fatalf("moving Preview branch reused the reviewed plan: %d %s", code, out.String())
	}
	if _, err := os.Stat("sample"); !os.IsNotExist(err) {
		t.Fatal("stale plan published a destination")
	}
}

func TestPreviewResolutionFailureDoesNotPublishScaffold(t *testing.T) {
	inTempDir(t)
	previewModuleResolver = resolvePreviewModules
	t.Setenv("PATH", t.TempDir())
	var out, errOut bytes.Buffer
	if code := runNew([]string{"sample", "--skip-install", "--json"}, &out, &errOut); code != 3 || !strings.Contains(errOut.String(), "resolve Preview branches") {
		t.Fatalf("missing Go tool did not fail resolution: %d %s %s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat("sample"); !os.IsNotExist(err) {
		t.Fatal("failed resolution published a destination")
	}
}

func TestResolvePreviewModulesUsesGoProxyAndCanonicalVersions(t *testing.T) {
	proxy := t.TempDir()
	cache := t.TempDir()
	path, err := module.EscapePath(spineImportPath)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(proxy, filepath.FromSlash(path), "@v")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	version := "v0.7.0-preview.9"
	for name, contents := range map[string]string{
		"preview.info":    `{"Version":"v0.7.0-preview.9","Time":"2026-11-05T00:00:00Z"}`,
		version + ".mod":  "module " + spineImportPath + "\n\ngo 1.25.5\n",
		version + ".info": `{"Version":"v0.7.0-preview.9","Time":"2026-11-05T00:00:00Z"}`,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proxyURL := url.URL{Scheme: "file", Path: filepath.ToSlash(proxy)}
	if filepath.VolumeName(proxy) != "" {
		proxyURL.Path = "/" + proxyURL.Path
	}
	t.Setenv("GOPROXY", proxyURL.String())
	t.Setenv("GOMODCACHE", cache)
	t.Setenv("GOSUMDB", "off")
	targets := []selectedModule{{Path: spineImportPath, Version: "preview"}, {Path: "github.com/labstack/echo/v5", Version: platformEchoVersion}}
	resolved, err := resolvePreviewModules(context.Background(), targets)
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0].Version != version || resolved[1].Version != platformEchoVersion || targets[0].Version != "preview" {
		t.Fatalf("invalid branch resolution: %+v", resolved)
	}
	if _, err := resolvePreviewModules(context.Background(), []selectedModule{{Path: echoPlatformPath, Version: "preview"}}); err == nil {
		t.Fatal("missing Preview branch was silently replaced")
	}
}

func TestPreviewQueryHonorsCustomProxy(t *testing.T) {
	for _, proxy := range []string{"", "https://proxy.golang.org,direct", "https://proxy.example", "file:///offline", "off"} {
		t.Setenv("GOPROXY", proxy)
		want := proxy
		if want == "" || want == "https://proxy.golang.org,direct" {
			want = "direct"
		}
		found := false
		for _, entry := range previewQueryEnvironment() {
			if entry == "GOPROXY="+want {
				found = true
			}
		}
		if !found {
			t.Fatalf("proxy %q was not respected", proxy)
		}
	}
}
