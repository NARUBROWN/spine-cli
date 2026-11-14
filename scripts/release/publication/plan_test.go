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
)

type tagFixture struct {
	reuseFixture
	writes []doc
	tags   map[string]string
}

func (f *tagFixture) TagSHA(version string) (string, error) {
	return f.tags[version], nil
}

func (f *tagFixture) Releases() ([]json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected release history lookup")
}

func (f *tagFixture) API(method, path string, in, out any, missing bool) (bool, error) {
	if method == "POST" && path == "git/refs" {
		f.writes = append(f.writes, in.(doc))
		return true, nil
	}
	return f.reuseFixture.API(method, path, in, out, missing)
}

func TestPreviewTagRevalidatesPlanAndReports(t *testing.T) {
	const repository = "NARUBROWN/spine-cli"
	const version = "v0.4.5-preview.3"
	source := strings.Repeat("a", 40)
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("Fixture notes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := doc{"next_version": "v0.4.5", "branches": doc{"preview": "preview"}, "reviewer_login": "owner", "notes_path": notes,
		"required_public_os": []any{"linux", "darwin", "windows"}, "recipe": doc{"id": "fixed", "modules": doc{core: "v0.7.5"}}}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	blob := doc{"content": base64.StdEncoding.EncodeToString(raw)}
	entries := map[string]any{
		"git/matching-refs/tags/v0.4.5-preview.":             []any{},
		"git/ref/heads/preview":                              doc{"object": doc{"sha": source}},
		"git/commits/" + source:                              doc{"tree": doc{"sha": "tree"}},
		"contents/.github/release-policy.json?ref=" + source: blob,
		"contents/.github/release-policy.json?ref=preview":   blob,
		"environments/release-preview": doc{"can_admins_bypass": false, "deployment_branch_policy": doc{"custom_branch_policies": true},
			"protection_rules": []doc{{"type": "required_reviewers", "reviewers": []doc{{"type": "User", "reviewer": doc{"login": "owner"}}}}}},
		"environments/release-preview/deployment-branch-policies": doc{"branch_policies": []doc{{"name": "preview", "type": "branch"}}},
	}
	t.Setenv("GITHUB_SHA", source)
	repo := &tagFixture{reuseFixture: reuseFixture{entries: entries}}
	approved, err := planRelease(repo, repository, policy, "preview", version, source, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if approved["source_sha"] != source || approved["source_evidence"] != nil {
		t.Fatalf("unexpected preview source: %v", approved)
	}
	t.Run("preview plan inputs", func(t *testing.T) {
		for _, invalid := range []string{"main", "HEAD", "--help", source[:12], strings.Repeat("f", 40)} {
			if _, err := planRelease(repo, repository, policy, "preview", version, invalid, "", time.Now()); err == nil {
				t.Fatalf("accepted source %q", invalid)
			}
		}
		for _, invalid := range []string{"v0.4.5", "v0.4.5-rc.1", "v9.9.9-preview.1", "v0.4.5-preview.01", "v01.2.3-preview.1"} {
			if _, err := planRelease(repo, repository, policy, "preview", invalid, source, "", time.Now()); err == nil {
				t.Fatalf("accepted version %q", invalid)
			}
		}
		local := maps.Clone(entries)
		local["git/matching-refs/tags/v0.4.5-preview."] = []doc{{"ref": "refs/tags/" + version}}
		tagged := &tagFixture{reuseFixture: reuseFixture{entries: local}, tags: map[string]string{version: source}}
		for _, target := range []string{"v0.4.5-preview.1", version, "v0.4.5-preview.4"} {
			_, err := planRelease(tagged, repository, policy, "preview", target, source, "", time.Now())
			if (err == nil) != (target == "v0.4.5-preview.4") {
				t.Fatalf("version %s with existing %s: %v", target, version, err)
			}
		}
		local["contents/.github/release-policy.json?ref="+source] = doc{"content": base64.StdEncoding.EncodeToString([]byte(`{"next_version":"v0.4.2"}`))}
		if _, err := planRelease(tagged, repository, policy, "preview", "v0.4.5-preview.4", source, "", time.Now()); err == nil || !strings.Contains(err.Error(), "릴리스 정책") {
			t.Fatalf("outdated policy: %v", err)
		}
		t.Setenv("GITHUB_SHA", strings.Repeat("f", 40))
		if _, err := planRelease(repo, repository, policy, "preview", version, source, "", time.Now()); err == nil || !strings.Contains(err.Error(), "HEAD") {
			t.Fatalf("wrong dispatch SHA: %v", err)
		}
	})
	t.Run("candidate default recipe", func(t *testing.T) {
		pins := maps.Clone(policy)
		pins["recipe"] = doc{"id": "fixed", "modules": doc{core: "v0.7.5-rc.1"}}
		data, err := json.Marshal(pins)
		if err != nil {
			t.Fatal(err)
		}
		local := maps.Clone(entries)
		local["contents/.github/release-policy.json?ref="+source] = doc{"content": base64.StdEncoding.EncodeToString(data)}
		candidate := &tagFixture{reuseFixture: reuseFixture{entries: local}}
		if _, err := planRelease(candidate, repository, pins, "preview", version, source, "", time.Now()); err != nil {
			t.Fatal(err)
		}
		for _, pin := range []string{"v0.7.5-rc.1", "preview"} {
			pins["recipe"] = doc{"id": "fixed", "modules": doc{core: pin}}
			_, err := planRelease(candidate, repository, pins, "stable", "v0.4.5", "v0.4.5-rc.1", "", time.Now())
			if err == nil || !strings.Contains(err.Error(), "정식 모듈") {
				t.Fatalf("stable with %q: %v", pin, err)
			}
		}
	})

	t.Run("workflow outputs", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		t.Setenv("GITHUB_REF", "refs/heads/preview")
		t.Setenv("RELEASE_CHANNEL", "preview")
		t.Setenv("RELEASE_SOURCE", source)
		t.Setenv("RELEASE_VERSION", version)
		t.Setenv("RELEASE_OVERRIDE_REASON", "")
		output, summary := filepath.Join(root, "outputs"), filepath.Join(root, "summary")
		t.Setenv("GITHUB_OUTPUT", output)
		t.Setenv("GITHUB_STEP_SUMMARY", summary)
		local := maps.Clone(entries)
		local["actions/workflows/ci.yml/runs?branch=preview&head_sha="+source+"&event=push&status=success"] = doc{
			"workflow_runs": []doc{{"id": 20, "run_attempt": 2, "status": "completed", "conclusion": "success", "event": "push", "head_branch": "preview", "head_sha": source, "head_repository": doc{"full_name": repository}, "path": ".github/workflows/ci.yml"}},
		}
		if err := preparePlan(&tagFixture{reuseFixture: reuseFixture{entries: local}}, repository, policy, "plan.json"); err != nil {
			t.Fatal(err)
		}
		for path, want := range map[string]string{
			output:              "sha=" + source + "\nversion=" + version + "\ntag_only=false\nreuse_ci=true\nreuse_common=false\n",
			summary:             "출시 계획: `" + version + "` / `" + source + "`\n\n관찰 기간 단축 사유: 없음\n같은 커밋의 Preview CI 실행 `20`를 재사용합니다.\n",
			"release-title.txt": version + "\n",
		} {
			got, err := os.ReadFile(path)
			if err != nil || string(got) != want {
				t.Fatalf("%s: %q, want %q, error %v", path, got, want, err)
			}
		}
	})

	approved["preview_ci_evidence"] = doc{"id": json.Number("20"), "run_attempt": json.Number("2")}
	for _, tc := range []struct{ name, want string }{
		{"valid", ""}, {"wrong verified SHA", "검증한 커밋"}, {"wrong branch", "실행 브랜치"},
		{"head moved", "채널 HEAD"}, {"changed policy", "정책이 바뀌었습니다"},
		{"changed notes hash", "승인한 계획"}, {"wrong attempt", "Preview CI의 실행 기록"},
		{"wrong run SHA", "Preview CI의 실행 기록"}, {"wrong run event", "Preview CI의 실행 기록"}, {"failed run", "Preview CI의 실행 기록"},
		{"wrong query", "결과가 발행 계획"}, {"wrong components", "결과가 발행 계획"},
		{"duplicate OS", "OS가 누락되거나 중복"}, {"missing entrypoint", "결과가 발행 계획"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := maps.Clone(entries)
			repo := &tagFixture{reuseFixture: reuseFixture{entries: local}}
			plan := maps.Clone(approved)
			t.Setenv("VERIFIED_SHA", source)
			t.Setenv("GITHUB_REF", "refs/heads/preview")
			run := doc{"id": 20, "run_attempt": 2, "status": "completed", "conclusion": "success", "event": "push", "head_branch": "preview", "head_sha": source, "head_repository": doc{"full_name": repository}, "path": ".github/workflows/ci.yml"}
			local["actions/runs/20"] = run
			switch tc.name {
			case "wrong verified SHA":
				t.Setenv("VERIFIED_SHA", strings.Repeat("b", 40))
			case "wrong branch":
				t.Setenv("GITHUB_REF", "refs/heads/main")
			case "head moved":
				local["git/ref/heads/preview"] = doc{"object": doc{"sha": strings.Repeat("b", 40)}}
			case "changed policy":
				local["contents/.github/release-policy.json?ref=preview"] = doc{"content": "e30="}
			case "changed notes hash":
				plan["notes_sha256"] = "changed"
			case "wrong attempt":
				run["run_attempt"] = 3
			case "wrong run SHA":
				run["head_sha"] = strings.Repeat("b", 40)
			case "wrong run event":
				run["event"] = "pull_request"
			case "failed run":
				run["conclusion"] = "failure"
			}
			root := t.TempDir()
			for _, platform := range []string{"linux", "darwin", "windows"} {
				report := doc{"os": platform, "query": source, "source_sha": source, "result": "passed", "components": plan["components"], "entrypoints": []string{"spine", "spine-cli"}}
				if platform == "windows" {
					switch tc.name {
					case "wrong query":
						report["query"] = "main"
					case "wrong components":
						report["components"] = doc{}
					case "duplicate OS":
						report["os"] = "linux"
					case "missing entrypoint":
						report["entrypoints"] = []string{"spine"}
					}
				}
				dir := filepath.Join(root, platform)
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := writeDocument(filepath.Join(dir, "release-validation.json"), report); err != nil {
					t.Fatal(err)
				}
			}
			err := publish(repo, repository, policy, plan, root)
			if tc.want == "" {
				if err != nil || !reflect.DeepEqual(repo.writes, []doc{{"ref": "refs/tags/" + version, "sha": source}}) {
					t.Fatalf("writes=%v err=%v", repo.writes, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) || len(repo.writes) != 0 {
				t.Fatalf("writes=%v err=%v want %s", repo.writes, err, tc.want)
			}
		})
	}
}

func TestObservationBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for _, period := range []int64{24, 72} {
		t.Run(fmt.Sprint(period), func(t *testing.T) {
			threshold := time.Duration(period) * time.Hour
			for _, tc := range []struct {
				name    string
				age     time.Duration
				reason  string
				failure string
			}{
				{"before cutoff", threshold - time.Second, "", "관찰 기간 부족"},
				{"at cutoff", threshold, "", ""},
				{"after cutoff", threshold + time.Second, "", ""},
				{"approved exception", time.Hour, "approved", ""},
				{"future timestamp with exception", -time.Second, "approved", "현재보다 뒤"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					err := observe(now.Add(-tc.age).Format(time.RFC3339Nano), period, now, tc.reason)
					if tc.failure == "" && err != nil || tc.failure != "" && (err == nil || !strings.Contains(err.Error(), tc.failure)) {
						t.Fatalf("observe returned %v, wanted %q", err, tc.failure)
					}
				})
			}
		})
	}
}

type historyFixture struct {
	tagFixture
	releases []json.RawMessage
}

func (f *historyFixture) Releases() ([]json.RawMessage, error) {
	return f.releases, nil
}

func TestStableHistoryUsesLatestPublishedStable(t *testing.T) {
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	repo := &historyFixture{releases: []json.RawMessage{
		json.RawMessage(`{"tag_name":"v0.4.3","published_at":"2026-11-06T00:00:00Z"}`),
		json.RawMessage(`{"tag_name":"v0.4.2","published_at":"2026-11-12T00:00:00Z"}`),
		json.RawMessage(`{"tag_name":"v9.0.0","draft":true,"published_at":null}`),
		json.RawMessage(`{"tag_name":"v8.0.0","prerelease":true,"published_at":null}`),
	}}
	for _, tc := range []struct {
		name, version, reason, failure string
		interval                       int64
	}{
		{"interval elapsed", "v0.4.4", "", "", 48},
		{"recent lower version still counts", "v0.4.4", "", "관찰 기간 부족", 168},
		{"explicit override", "v0.4.4", "approved", "", 168},
		{"downgrade even with override", "v0.4.2", "approved", "기존 정식 버전보다 높은", 0},
		{"same version even with override", "v0.4.3", "approved", "기존 정식 버전보다 높은", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := stableHistory(repo, tc.version, tc.interval, now, tc.reason)
			if tc.failure == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.failure) {
				t.Fatalf("stableHistory returned %v, wanted %q", err, tc.failure)
			}
		})
	}
}

func TestPlanTimeMatchesLegacyISOFormat(t *testing.T) {
	at := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for offset, want := range map[time.Duration]string{
		0:                "2026-11-14T00:00:00+00:00",
		time.Microsecond: "2026-11-14T00:00:00.000001+00:00",
	} {
		if got := formatPlanTime(at.Add(offset)); got != want {
			t.Fatalf("timestamp %q, want %q", got, want)
		}
	}
}
