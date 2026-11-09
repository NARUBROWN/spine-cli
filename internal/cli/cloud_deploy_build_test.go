package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
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
	artifact, err := compileCloudArtifact(context.Background(), ".", maxCloudArtifactBytes, &buildErr)
	if err != nil {
		t.Fatalf("build failed: %v: %s", err, buildErr.String())
	}
	sum := sha256.Sum256(artifact.Binary)
	if artifact.OS != "linux" || artifact.Architecture != "arm64" || artifact.SHA256 != hex.EncodeToString(sum[:]) || len(artifact.Binary) == 0 {
		t.Fatal("invalid build metadata")
	}
	if _, err = compileCloudArtifact(context.Background(), ".", 1, &buildErr); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatal("oversize executable was accepted")
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
