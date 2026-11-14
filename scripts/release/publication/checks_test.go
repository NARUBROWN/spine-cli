package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifiedOutputRequiresExpectedJobs(t *testing.T) {
	for _, tc := range []struct {
		jobs    string
		reuse   bool
		failure string
	}{
		{`{"policy":{"result":"success"},"live-composition":{"result":"success"}}`, false, ""},
		{`{"policy":{"result":"skipped"},"portable":{"result":"skipped"},"core-regression":{"result":"skipped"},"public-platforms":{"result":"skipped"},"live-composition":{"result":"success"}}`, true, ""},
		{`{"policy":{"result":"skipped"}}`, false, "policy"},
		{`{"policy":{"result":"success"}}`, true, "policy"},
		{`{"policy":{"result":"skipped"},"live-composition":{"result":"skipped"}}`, true, "live-composition"},
		{`{"source":{"result":"cancelled"}}`, true, "source"},
		{`{"source":{"result":"failure"}}`, false, "source"},
	} {
		value, err := verifiedOutput([]byte(tc.jobs), tc.reuse, "test")
		if tc.failure == "" {
			if err != nil || value != "sha=test\n" {
				t.Fatalf("%q %v", value, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.failure) || value != "" {
			t.Fatalf("%q %v", value, err)
		}
	}
}
func TestPolicyFileChecks(t *testing.T) {
	root := t.TempDir()
	const notes = "release/notes/v0.4.5.md"
	p := doc{"next_version": "v0.4.5", "notes_path": notes}
	for _, dir := range []string{"internal/cli", "release/notes"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(root, "internal/cli/version.go")
	for _, version := range []string{"0.4.4-dev", "0.4.5-dev"} {
		if err := os.WriteFile(source, []byte("const developmentVersion = \""+version+"\"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := checkPolicyFiles(root, p); err == nil {
			t.Fatal("incomplete policy accepted")
		}
	}
	if err := os.WriteFile(filepath.Join(root, notes), []byte("draft"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkPolicyFiles(root, p); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyRejectsThirdPartyPreviewPin(t *testing.T) {
	p, err := read("../../../.github/release-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePolicy(p); err != nil {
		t.Fatal(err)
	}
	recipe := object(p, "recipe")
	recipe["id"] = "platform-contracts-preview"
	object(recipe, "modules")["github.com/labstack/echo/v5"] = "preview"
	if err := validatePolicy(p); err == nil || !strings.Contains(err.Error(), "고정 모듈 버전") {
		t.Fatalf("got %v", err)
	}
}
