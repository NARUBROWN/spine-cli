package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasegithub"
)

func TestStablePlanRequiresVerifiedRCAndApproval(t *testing.T) {
	const repository = "NARUBROWN/spine-cli"
	const version = "v0.4.5"
	const prior = version + "-rc.1"
	source, target := strings.Repeat("a", 40), strings.Repeat("b", 40)
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	t.Setenv("GITHUB_SHA", target)
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("Release notes"), 0600); err != nil {
		t.Fatal(err)
	}
	oses := []any{"linux", "darwin", "windows"}
	for _, tc := range []struct {
		name, lastVersion, failure string
		age, days, interval        int
	}{
		{"valid default interval", "v0.4.2", "", 25, 2, 24},
		{"valid longer interval", "v0.4.2", "", 25, 8, 168},
		{"early RC", "v0.4.2", "관찰 기간 부족", 23, 8, 168},
		{"recent stable", "v0.4.2", "168시간", 25, 2, 168},
		{"downgrade", "v9.0.0", "기존 정식 버전보다 높은", 25, 10, 168},
		{"missing reviewer", "v0.4.2", "지정 리뷰어", 25, 8, 168},
		{"admin bypass", "v0.4.2", "우회 금지", 25, 8, 168},
		{"cannot skip RC", "v0.4.2", "rc 태그", 25, 8, 168},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := doc{"next_version": version, "branches": doc{"preview": "preview", "rc": "rc", "stable": "main"},
				"required_public_os": oses, "reviewer_login": "owner", "notes_path": notes,
				"rc_hours": json.Number("24"), "stable_interval_hours": json.Number(fmt.Sprint(tc.interval)),
				"recipe": doc{"id": "fixed", "modules": doc{core: "v0.7.5"}}}
			recipe, err := components(p)
			if err != nil {
				t.Fatal(err)
			}
			var rows []doc
			for _, os := range oses {
				rows = append(rows, doc{"os": os, "result": "passed", "source_sha": source, "query": prior,
					"components": recipe, "entrypoints": []string{"spine", "spine-cli"}})
			}
			proof := &releasegithub.Evidence{}
			expired := false
			proof.Artifact.Expired, proof.Artifact.ID, proof.Artifact.Name = &expired, 25, "release-manifest-"+prior
			proof.Artifact.CreatedAt = now.Add(-time.Duration(tc.age) * time.Hour).Format(time.RFC3339)
			proof.Artifact.WorkflowRun.ID, proof.Artifact.WorkflowRun.HeadSHA, proof.Artifact.WorkflowRun.HeadBranch = 10, source, "rc"
			candidatePlan := doc{"schema_version": "spine-cli.release-manifest/v1", "repository": repository,
				"version": prior, "channel": "rc", "branch": "rc", "source_sha": source, "source_tree": "tree", "components": recipe,
				"workflow_run_id": "10", "workflow_run_attempt": "2", "required_public_os": oses}
			finished, err := finish(candidatePlan, rows, "2", func(string) (string, error) { return source, nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(t.TempDir(), "release-manifest.json")
			if err := writeDocument(manifestPath, finished); err != nil {
				t.Fatal(err)
			}
			proof.Manifest, err = os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			proof.WorkflowRun = []byte(fmt.Sprintf(`{"id":10,"run_attempt":2,"status":"completed","conclusion":"success","event":"workflow_dispatch","head_branch":"rc","head_sha":%q,"path":".github/workflows/rc.yml"}`, source))
			var merge, candidate gitCommit
			merge.Tree.SHA, candidate.Tree.SHA = "tree", "tree"
			merge.Parents = []struct{ SHA string }{{"previous"}, {source}}
			pull := pullRequest{Number: 1, MergedAt: "date", MergeSHA: target}
			pull.Base.Ref, pull.Head.Ref, pull.Head.Repo.FullName = "main", "rc", repository
			reviewers := []doc{{"type": "User", "reviewer": doc{"login": "owner"}}}
			if tc.name == "missing reviewer" {
				reviewers = nil
			}
			policyJSON, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			repo := &historyFixture{
				tagFixture: tagFixture{tags: map[string]string{prior: source}, reuseFixture: reuseFixture{evidence: proof, entries: map[string]any{
					"git/ref/heads/main":    doc{"object": doc{"sha": target}},
					"git/commits/" + target: merge, "git/commits/" + source: candidate,
					"commits/" + target + "/pulls?per_page=100":          []pullRequest{pull},
					"contents/.github/release-policy.json?ref=" + target: doc{"content": base64.StdEncoding.EncodeToString(policyJSON)},
					"environments/release-stable": doc{"can_admins_bypass": tc.name == "admin bypass", "deployment_branch_policy": doc{"custom_branch_policies": true},
						"protection_rules": []doc{{"type": "required_reviewers", "reviewers": reviewers}}},
					"environments/release-stable/deployment-branch-policies": doc{"branch_policies": []doc{{"name": "main", "type": "branch"}}},
				}}},
				releases: []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"tag_name":%q,"published_at":%q}`, tc.lastVersion, now.AddDate(0, 0, -tc.days).Format(time.RFC3339)))},
			}
			from := prior
			if tc.name == "cannot skip RC" {
				from = version + "-preview.1"
			}
			plan, err := planRelease(repo, repository, p, "stable", version, from, "", now)
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) || plan != nil {
					t.Fatalf("expected %q, got %v %v", tc.failure, plan, err)
				}
			} else if err != nil || plan["source_sha"] != target || plan["source_tag"] != prior {
				t.Fatalf("plan %v, error %v", plan, err)
			}
			if tc.name == "valid default interval" {
				root := t.TempDir()
				t.Chdir(root)
				output, summary := filepath.Join(root, "output"), filepath.Join(root, "summary")
				t.Setenv("GITHUB_OUTPUT", output)
				t.Setenv("GITHUB_STEP_SUMMARY", summary)
				t.Setenv("GITHUB_REF", "refs/heads/main")
				t.Setenv("RELEASE_CHANNEL", "stable")
				t.Setenv("RELEASE_SOURCE", prior)
				t.Setenv("RELEASE_VERSION", "")
				t.Setenv("RELEASE_OVERRIDE_REASON", "")
				if err := preparePlan(repo, repository, p, "plan.json"); err != nil {
					t.Fatal(err)
				}
				for path, want := range map[string]string{
					output:  fmt.Sprintf("sha=%s\nversion=%s\ntag_only=true\nreuse_ci=false\nreuse_common=true\n", target, version),
					summary: fmt.Sprintf("출시 계획: `%s` / `%s`\n\n관찰 기간 단축 사유: 없음\n공통 CI 재사용 근거는 `%s`의 Actions 실행 `10`입니다.\n", version, target, prior),
				} {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != want {
						t.Fatalf("%s: %q, want %q, error %v", path, got, want, err)
					}
				}
			}

		})
	}
}
