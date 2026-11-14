package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasegithub"
	"github.com/NARUBROWN/spine-cli/scripts/release/internal/reports"
)

type reuseFixture struct {
	entries  map[string]any
	source   string
	evidence *releasegithub.Evidence
}

func (f reuseFixture) API(method, path string, in, out any, missing bool) (bool, error) {
	data, ok := f.entries[path]
	if method != "GET" || !ok || missing {
		return false, fmt.Errorf("unexpected request %s", path)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(encoded, out)
}
func (f reuseFixture) TagSHA(string) (string, error) { return f.source, nil }
func (f reuseFixture) CandidateEvidence(string, string, bool) (*releasegithub.Evidence, error) {
	return f.evidence, nil
}

func TestPromotionReuse(t *testing.T) {
	const repository = "NARUBROWN/spine-cli"
	const tag = "v0.4.5-preview.3"
	source, merge := strings.Repeat("a", 40), strings.Repeat("b", 40)
	oses := []any{"linux", "darwin", "windows"}
	policy := doc{"next_version": "v0.4.5", "branches": doc{"preview": "preview"}, "required_public_os": oses, "recipe": doc{"id": "fixed", "modules": doc{core: "v0.7.5"}}}
	composition, err := components(policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "push", "fork", "wrong head branch", "wrong parent", "changed tree", "untagged", "changed recipe", "failed run"} {
		t.Run(scenario, func(t *testing.T) {
			event := "pull_request"
			pull := &pullRequest{}
			pull.Base.Ref, pull.Head.Ref, pull.Head.SHA, pull.Head.Repo.FullName = "rc", "preview", source, repository
			merged := *pull
			merged.Number, merged.MergedAt, merged.MergeSHA = 8, "date", merge
			tree, head := "tree", source
			candidate := composition
			conclusion := "success"
			tags := []doc{{"ref": "refs/tags/" + tag}}
			switch scenario {
			case "push":
				event, pull = "push", nil
			case "fork":
				pull.Head.Repo.FullName = "fork/spine-cli"
			case "wrong head branch":
				pull.Head.Ref = "feature"
			case "wrong parent":
				head = strings.Repeat("c", 40)
			case "changed tree":
				tree = "changed"
			case "untagged":
				tags = nil
			case "changed recipe":
				candidate = doc{"recipe": "changed"}
			case "failed run":
				conclusion = "failure"
			}
			expired := false
			proof := &releasegithub.Evidence{}
			proof.Artifact.Expired, proof.Artifact.ID, proof.Artifact.Name = &expired, 25, "release-manifest-"+tag
			proof.Artifact.WorkflowRun.ID, proof.Artifact.WorkflowRun.HeadSHA, proof.Artifact.WorkflowRun.HeadBranch = 10, source, "preview"
			proof.WorkflowRun = []byte(fmt.Sprintf(`{"id":10,"run_attempt":2,"status":"completed","conclusion":%q,"event":"workflow_dispatch","head_branch":"preview","head_sha":%q,"path":".github/workflows/preview.yml"}`, conclusion, source))
			var publicReports []doc
			for _, os := range oses {
				publicReports = append(publicReports, doc{"os": os, "result": "passed", "source_sha": source, "query": tag, "components": candidate, "entrypoints": []string{"spine", "spine-cli"}})
			}
			candidatePlan := doc{"schema_version": "spine-cli.release-manifest/v1", "repository": repository, "version": tag, "channel": "preview", "branch": "preview", "source_sha": source, "source_tree": "tree", "components": candidate, "workflow_run_id": "10", "workflow_run_attempt": "2", "required_public_os": oses}
			finished, err := finish(candidatePlan, publicReports, "2", func(string) (string, error) { return source, nil }, nil)
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
			var commit, previous gitCommit
			commit.Tree.SHA, previous.Tree.SHA = tree, "tree"
			commit.Parents = []struct{ SHA string }{{"initial"}, {head}}
			repo := reuseFixture{source: source, evidence: proof, entries: map[string]any{
				"git/commits/" + merge: commit, "git/commits/" + source: previous,
				"git/matching-refs/tags/v0.4.5-preview.": tags, "commits/" + merge + "/pulls?per_page=100": []pullRequest{merged},
			}}
			if scenario == "valid" {
				t.Run("publish promotion", func(t *testing.T) {
					const version = "v0.4.5-rc.1"
					notes := filepath.Join(t.TempDir(), "notes.md")
					if err := os.WriteFile(notes, []byte("Release notes"), 0600); err != nil {
						t.Fatal(err)
					}
					p := doc{"next_version": "v0.4.5", "branches": doc{"preview": "preview", "rc": "rc"}, "required_public_os": oses,
						"reviewer_login": "owner", "notes_path": notes, "preview_hours": json.Number("72"), "recipe": policy["recipe"]}
					data, err := json.Marshal(p)
					if err != nil {
						t.Fatal(err)
					}
					blob := doc{"content": base64.StdEncoding.EncodeToString(data)}
					repo.entries["contents/.github/release-policy.json?ref="+merge] = blob
					repo.entries["contents/.github/release-policy.json?ref=rc"] = blob
					repo.entries["git/ref/heads/rc"] = doc{"object": doc{"sha": merge}}
					repo.entries["git/matching-refs/tags/v0.4.5-rc."] = []doc{}
					repo.entries["environments/release-rc"] = doc{"can_admins_bypass": false, "deployment_branch_policy": doc{"custom_branch_policies": true}, "protection_rules": []doc{{"type": "required_reviewers", "reviewers": []doc{{"type": "User", "reviewer": doc{"login": "owner"}}}}}}
					repo.entries["environments/release-rc/deployment-branch-policies"] = doc{"branch_policies": []doc{{"name": "rc", "type": "branch"}}}
					t.Setenv("GITHUB_SHA", merge)
					t.Setenv("VERIFIED_SHA", merge)
					t.Setenv("GITHUB_REF", "refs/heads/rc")
					fixture := &tagFixture{reuseFixture: repo, tags: map[string]string{tag: source}}
					evidence := *repo.evidence
					fixture.evidence = &evidence
					now := time.Now()
					const override = "서비스 장애 대응을 위한 긴급 수정으로 관찰 기간을 단축합니다"
					for _, tc := range []struct {
						age             time.Duration
						reason, failure string
					}{
						{71 * time.Hour, "", "관찰 기간 부족"},
						{71 * time.Hour, "short", "20자 이상"},
						{71 * time.Hour, override, ""},
						{-time.Hour, override, "현재보다 뒤"},
					} {
						evidence.Artifact.CreatedAt = now.Add(-tc.age).Format(time.RFC3339Nano)
						plan, err := planRelease(fixture, repository, p, "rc", version, tag, tc.reason, now)
						if tc.failure == "" {
							if err != nil || plan["observation_override_reason"] != tc.reason {
								t.Fatalf("override plan %v, error %v", plan, err)
							}
						} else if err == nil || !strings.Contains(err.Error(), tc.failure) {
							t.Fatalf("wanted %q, got %v", tc.failure, err)
						}
					}

					evidence.Artifact.CreatedAt = time.Now().Add(-80 * time.Hour).Format(time.RFC3339)
					plan, err := planRelease(fixture, repository, p, "rc", version, tag, "", time.Now())
					if err != nil || plan["source_sha"] != merge || plan["upstream_source_sha"] != source || plan["public_validation_scope"] != "tag-install" {
						t.Fatalf("plan %v, error %v", plan, err)
					}
					t.Run("workflow outputs", func(t *testing.T) {
						root := t.TempDir()
						t.Chdir(root)
						output, summary := filepath.Join(root, "output"), filepath.Join(root, "summary")
						t.Setenv("GITHUB_OUTPUT", output)
						t.Setenv("GITHUB_STEP_SUMMARY", summary)
						t.Setenv("RELEASE_CHANNEL", "rc")
						t.Setenv("RELEASE_VERSION", version)
						t.Setenv("RELEASE_SOURCE", tag)
						t.Setenv("RELEASE_OVERRIDE_REASON", "")
						if err := preparePlan(fixture, repository, p, "plan.json"); err != nil {
							t.Fatal(err)
						}
						for path, want := range map[string]string{
							output:  fmt.Sprintf("sha=%s\nversion=%s\ntag_only=true\nreuse_ci=false\nreuse_common=true\n", merge, version),
							summary: fmt.Sprintf("출시 계획: `%s` / `%s`\n\n관찰 기간 단축 사유: 없음\n공통 CI 재사용 근거는 `%s`의 Actions 실행 `10`입니다.\n", version, merge, tag),
						} {
							got, err := os.ReadFile(path)
							if err != nil || string(got) != want {
								t.Fatalf("%s: %q, want %q, error %v", path, got, want, err)
							}
						}
					})

					if object(plan, "source_evidence")["artifact_id"] != json.Number("25") {
						t.Fatal(plan)
					}
					originalManifest, originalRun := evidence.Manifest, evidence.WorkflowRun
					manifest, err := reports.Decode(originalManifest)
					if err != nil {
						t.Fatal(err)
					}
					manifest["components"] = doc{"wrong": "versions"}
					evidence.Manifest, err = json.Marshal(manifest)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := planRelease(fixture, repository, p, "rc", version, tag, "", now); err == nil {
						t.Fatal("changed components accepted")
					}
					evidence.Manifest = originalManifest
					evidence.WorkflowRun = []byte(strings.Replace(string(originalRun), `"head_branch":"preview"`, `"head_branch":"main"`, 1))
					if _, err := planRelease(fixture, repository, p, "rc", version, tag, "", now); err == nil {
						t.Fatal("wrong workflow branch accepted")
					}
					evidence.WorkflowRun = originalRun
					for _, condition := range []string{"wrong parent", "changed tree", "wrong direction"} {
						t.Run(condition, func(t *testing.T) {
							local := *fixture
							local.entries = maps.Clone(repo.entries)
							changedCommit, changedPR := commit, merged
							switch condition {
							case "wrong parent":
								changedCommit.Parents = []struct{ SHA string }{{"initial"}, {merge}}
							case "changed tree":
								changedCommit.Tree.SHA = "changed"
							case "wrong direction":
								changedPR.Head.Ref = "feat/bypass"
							}
							local.entries["git/commits/"+merge] = changedCommit
							local.entries["commits/"+merge+"/pulls?per_page=100"] = []pullRequest{changedPR}
							if _, err := planRelease(&local, repository, p, "rc", version, tag, "", now); err == nil {
								t.Fatal("invalid promotion accepted")
							}
						})
					}

					data, err = json.Marshal(plan)
					if err != nil {
						t.Fatal(err)
					}
					approved, err := reports.Decode(data)
					if err != nil {
						t.Fatal(err)
					}
					if err := publish(fixture, repository, p, approved, t.TempDir()); err != nil {
						t.Fatal(err)
					}
					want := []doc{{"ref": "refs/tags/" + version, "sha": merge}}
					if !reflect.DeepEqual(fixture.writes, want) {
						t.Fatal(fixture.writes)
					}
					cohort := doc{}
					for _, module := range modules {
						cohort[module] = "v1.0.0-rc.1"
					}
					cohortPolicy := maps.Clone(p)
					cohortPolicy["rc_cohort"] = cohort
					policyJSON, err := json.Marshal(cohortPolicy)
					if err != nil {
						t.Fatal(err)
					}
					policyRef := "contents/.github/release-policy.json?ref=" + merge
					repo.entries[policyRef] = doc{"content": base64.StdEncoding.EncodeToString(policyJSON)}
					fullPlan, err := planRelease(fixture, repository, cohortPolicy, "rc", version, tag, "", now)
					repo.entries[policyRef] = blob
					if err != nil || !reflect.DeepEqual(fullPlan["rc_cohort"], cohort) || fullPlan["public_validation_scope"] != nil || fullPlan["source_evidence"] == nil {
						t.Fatalf("cohort plan %v, error %v", fullPlan, err)
					}
					for _, row := range publicReports {
						row["query"], row["source_sha"] = version, merge
					}
					if err := reports.Validate(publicReports, fullPlan, version); err == nil || !strings.Contains(err.Error(), "전체 RC 조합") {
						t.Fatalf("incomplete cohort reports: %v", err)
					}

					fixture.writes = nil
					evidence.Artifact.ID++
					if err := publish(fixture, repository, p, approved, t.TempDir()); err == nil || !strings.Contains(err.Error(), "승인한 계획") || len(fixture.writes) != 0 {
						t.Fatalf("replaced artifact: %v, tag writes %v", err, fixture.writes)
					}
				})

				for _, mutation := range []string{"wrong workflow", "wrong tag SHA", "missing validation", "expired", "wrong repository", "wrong artifact run", "wrong artifact SHA", "wrong attempt", "missing OS", "wrong query", "wrong tree"} {
					t.Run(mutation, func(t *testing.T) {
						candidate := repo
						evidence := *repo.evidence
						candidate.evidence = &evidence
						manifest, err := reports.Decode(evidence.Manifest)
						if err != nil {
							t.Fatal(err)
						}
						validation := object(manifest, "validation")
						rows := validation["reports"].([]any)
						switch mutation {
						case "wrong workflow":
							evidence.WorkflowRun = []byte(strings.ReplaceAll(string(evidence.WorkflowRun), "preview.yml", "ci.yml"))
						case "wrong tag SHA":
							candidate.source = merge
						case "missing validation":
							delete(manifest, "validation")
						case "expired":
							expired := true
							evidence.Artifact.Expired = &expired
						case "wrong repository":
							manifest["repository"] = "elsewhere/spine-cli"
						case "wrong artifact run":
							evidence.Artifact.WorkflowRun.ID++
						case "wrong artifact SHA":
							evidence.Artifact.WorkflowRun.HeadSHA = merge
						case "wrong attempt":
							manifest["workflow_run_attempt"] = "1"
						case "missing OS":
							validation["reports"] = rows[:len(rows)-1]
						case "wrong query":
							rows[0].(doc)["query"] = source
						case "wrong tree":
							manifest["source_tree"] = "other"
						}
						evidence.Manifest, err = json.Marshal(manifest)
						if err != nil {
							t.Fatal(err)
						}
						proof, approved, err := verifySource(candidate, repository, tag, "preview", policy)
						if err == nil || proof != nil || approved != nil {
							t.Fatalf("candidate accepted: %v, %v, %v", proof, approved, err)
						}
					})
				}
			}

			if scenario == "push" || scenario == "failed run" {
				t.Run("workflow candidate output", func(t *testing.T) {
					output := filepath.Join(t.TempDir(), "output")
					if err := os.WriteFile(output, []byte("prior=value\n"), 0600); err != nil {
						t.Fatal(err)
					}
					t.Setenv("GITHUB_OUTPUT", output)
					t.Setenv("GITHUB_EVENT_NAME", "push")
					t.Setenv("GITHUB_REF_NAME", "rc")
					t.Setenv("GITHUB_SHA", merge)
					if err := runPromotionCheck(repo, repository, policy); err != nil {
						t.Fatal(err)
					}
					want := "prior=value\n"
					if scenario == "push" {
						want += "candidate=" + tag + "\n"
					}
					data, err := os.ReadFile(output)
					if err != nil || string(data) != want {
						t.Fatalf("output %q, want %q, error %v", data, want, err)
					}
				})
			}

			got, id, err := promotionCandidate(repo, repository, policy, event, "rc", merge, pull)
			if scenario == "valid" || scenario == "push" {
				if err != nil || got != tag || id != 25 {
					t.Fatalf("%s %d %v", got, id, err)
				}
			} else if got != "" || id != 0 || (err != nil) != (scenario == "failed run") {
				t.Fatalf("%s %d %v", got, id, err)
			}
		})
	}
}
