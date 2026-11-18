package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifiedOutputRequiresExpectedJobs(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		jobs := map[string]map[string]string{}
		for _, name := range []string{"source", "policy", "portable", "core-regression", "generated-regression", "live-composition"} {
			result := "success"
			if reuse && name != "source" && name != "live-composition" {
				result = "skipped"
			}
			jobs[name] = map[string]string{"result": result}
		}
		raw, _ := json.Marshal(jobs)
		if value, err := verifiedOutput(raw, reuse, "test"); err != nil || value != "sha=test\n" {
			t.Fatalf("%q %v", value, err)
		}
		for name, state := range jobs {
			original := state["result"]
			for _, invalid := range []string{"failure", "cancelled", "skipped", "success"} {
				if invalid == original {
					continue
				}
				state["result"] = invalid
				raw, _ = json.Marshal(jobs)
				if value, err := verifiedOutput(raw, reuse, "test"); err == nil || value != "" {
					t.Fatalf("잘못된 결과를 허용: %s %s", name, invalid)
				}
			}
			state["result"] = original
			delete(jobs, name)
			raw, _ = json.Marshal(jobs)
			if _, err := verifiedOutput(raw, reuse, "test"); err == nil {
				t.Fatalf("필수 작업 누락을 허용: %s", name)
			}
			jobs[name] = state
		}
		jobs["public-platforms"] = map[string]string{"result": "success"}
		raw, _ = json.Marshal(jobs)
		if _, err := verifiedOutput(raw, reuse, "test"); err == nil {
			t.Fatal("확장 증거가 필수 CI에 섞였습니다")
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
