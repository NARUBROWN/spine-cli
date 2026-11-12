package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileCloudArtifact(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.invalid/cloud-test\n\ngo 1.25.5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prior, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prior) })
	t.Setenv("GOWORK", "off")
	var buildErr bytes.Buffer
	artifact, err := compileCloudArtifact(context.Background(), ".", maxCloudArtifactBytes, &buildErr, nil)
	if err != nil {
		t.Fatalf("build failed: %v: %s", err, buildErr.String())
	}
	sum := sha256.Sum256(artifact.Binary)
	if artifact.OS != "linux" || artifact.Architecture != "arm64" || artifact.SHA256 != hex.EncodeToString(sum[:]) || len(artifact.Binary) == 0 {
		t.Fatal("invalid build metadata")
	}
	if _, err = compileCloudArtifact(context.Background(), ".", 1, &buildErr, nil); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatal("oversize executable was accepted")
	}
}

func TestBuildCloudArtifactEmbedsFrontend(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Chdir(t.TempDir())
	options, err := parseNewOptions([]string{"cloud-web", "--module", "example.com/cloud-web", "--frontend", "react", "--frontend-root", "web"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := newProjectFiles(options)
	if err != nil {
		t.Fatal(err)
	}
	const scriptMarker = "cloud-frontend-compiled-script"
	const envMarker = "cloud-frontend-root-env"
	const publicMarker = "cloud-frontend-public-file"
	// npm 설치 없이 실제 정적 빌드와 실행 파일 내장을 검증합니다.
	files["web/src/main.tsx"] = `document.body.textContent = "` + scriptMarker + `" + import.meta.env.CLOUD_FRONTEND_TEST;`
	files[".env"] = "CLOUD_FRONTEND_TEST=" + envMarker + "\n"
	t.Setenv("CLOUD_FRONTEND_TEST", "")
	if err := os.Unsetenv("CLOUD_FRONTEND_TEST"); err != nil {
		t.Fatal(err)
	}
	files["web/public/.well-known/cloud.txt"] = publicMarker
	files["bin/frontend/keep.txt"] = "local frontend output"
	for path, contents := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		writeE2EFile(t, path, contents)
	}
	for _, args := range [][]string{{"mod", "tidy"}, {"mod", "download", "all"}} {
		if output, err := exec.Command("go", args...).CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	var out, errOut bytes.Buffer
	artifact, err := buildCloudArtifact(context.Background(), &out, &errOut)
	if err != nil {
		t.Fatalf("cloud build: %v\n%s\n%s", err, out.String(), errOut.String())
	}
	for _, expected := range []string{scriptMarker, envMarker, publicMarker, "/assets/app.js"} {
		if !bytes.Contains(artifact.Binary, []byte(expected)) {
			t.Errorf("missing frontend output %q in the artifact", expected)
		}
	}
	for _, path := range []string{"spine.toml", "internal/application/application.go", "bin/frontend/keep.txt"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != files[path] {
			t.Errorf("cloud build changed %s: %v", path, err)
		}
	}
	output, err := os.ReadDir("bin/frontend")
	if err != nil || len(output) != 1 {
		t.Errorf("cloud build wrote to local frontend output: %v, %v", output, err)
	}
}

func TestBuildCloudExample(t *testing.T) {
	root := os.Getenv("SPINE_CLOUD_EXAMPLE_ROOT")
	if root == "" {
		t.Skip("example path not configured")
	}
	prior, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prior) })
	var out, errOut bytes.Buffer
	artifact, err := buildCloudArtifact(context.Background(), &out, &errOut)
	if err != nil {
		t.Fatalf("example build: %v\n%s\n%s", err, out.String(), errOut.String())
	}
	t.Logf("verified example: %s", artifactSummary(artifact))
}
