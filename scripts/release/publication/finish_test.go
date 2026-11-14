package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasenotes"
)

func TestFinishCommandRecordsReportsAndPublishesOnlyStable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GitHub command fixture uses a POSIX shell")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gh"), []byte(`#!/bin/sh
case "$1 $2" in
  "api --method") printf '{"object":{"type":"commit","sha":"%s"}}\n' "$EXPECTED_SHA";;
  "release create") printf '%s\n' "$@" >> "$GH_CALL";;
  *) exit 1;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	source := strings.Repeat("a", 40)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GITHUB_REPOSITORY", "NARUBROWN/spine-cli")
	t.Setenv("EXPECTED_SHA", source)
	t.Setenv("GITHUB_RUN_ID", "99")
	t.Setenv("GITHUB_RUN_ATTEMPT", "3")
	policyPath := "../../../.github/release-policy.json"
	policy, err := read(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Dir(field(policy, "notes_path")), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(field(policy, "notes_path"), []byte("Release notes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	policyPath = filepath.Join(root, "policy.json")
	if err := writeDocument(policyPath, policy); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		channel, version string
		missing          bool
	}{
		{"preview", "v0.4.5-preview.1", false},
		{"rc", "v0.4.5-rc.1", false},
		{"stable", "v0.4.5", false},
		{"stable", "v0.4.5", true},
	} {
		t.Run(fmt.Sprintf("%s/missing=%t", tc.channel, tc.missing), func(t *testing.T) {
			dir := t.TempDir()
			call := filepath.Join(dir, "gh-call")
			t.Setenv("GH_CALL", call)
			plan := doc{"channel": tc.channel, "version": tc.version, "source_sha": source, "workflow_run_id": "17", "workflow_run_attempt": "1",
				"required_public_os": []string{"linux", "darwin", "windows"}, "components": doc{"recipe_sha256": "fixture"}}
			body, err := releasenotes.Body(plan, field(policy, "notes_path"))
			if err != nil {
				t.Fatal(err)
			}
			plan["notes_sha256"] = fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
			planPath, manifestPath := filepath.Join(dir, "plan.json"), filepath.Join(dir, "release-manifest.json")
			if err := writeDocument(planPath, plan); err != nil {
				t.Fatal(err)
			}
			for _, osName := range []string{"linux", "darwin", "windows"} {
				if tc.missing && osName == "windows" {
					continue
				}
				dest := filepath.Join(dir, osName)
				if err := os.Mkdir(dest, 0700); err != nil {
					t.Fatal(err)
				}
				row := doc{"os": osName, "source_sha": source, "query": tc.version, "result": "passed", "components": plan["components"], "entrypoints": []string{"spine", "spine-cli"}}
				if err := writeDocument(filepath.Join(dest, "release-validation.json"), row); err != nil {
					t.Fatal(err)
				}
			}
			err = run([]string{"finish", "--policy", policyPath, "--plan", planPath, "--reports", dir, "--manifest", manifestPath})
			if tc.missing {
				if err == nil || !strings.Contains(err.Error(), "OS가 누락되거나 중복") {
					t.Fatalf("missing report: %v", err)
				}
				if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
					t.Fatalf("manifest stat: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				manifest, err := read(manifestPath)
				validation := object(manifest, "validation")
				rows, _ := validation["reports"].([]any)
				if err != nil || manifest["version"] != tc.version || manifest["workflow_run_id"] != "17" ||
					manifest["workflow_run_attempt"] != "3" || validation["result"] != "passed" || len(rows) != 3 {
					t.Fatalf("manifest %v: %v", manifest, err)
				}
				original, err := read(planPath)
				if err != nil || original["workflow_run_attempt"] != "1" {
					t.Fatalf("plan %v: %v", original, err)
				}
			}
			data, err := os.ReadFile(call)
			if tc.channel != "stable" || tc.missing {
				if !os.IsNotExist(err) {
					t.Fatalf("release command %q: %v", data, err)
				}
				return
			}
			if err != nil || !strings.HasPrefix(string(data), "release\ncreate\n"+tc.version+"\n"+manifestPath+"\n") ||
				!strings.Contains(string(data), "\n--verify-tag\n") || !strings.HasSuffix(string(data), "\n--latest\n") ||
				strings.Contains(string(data), "--prerelease") || strings.Count(string(data), "release\ncreate\n") != 1 {
				t.Fatalf("release command %q: %v", data, err)
			}
		})
	}
}
