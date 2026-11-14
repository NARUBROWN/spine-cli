package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/approval"
	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasenotes"
	"github.com/NARUBROWN/spine-cli/scripts/release/internal/reports"
	"golang.org/x/mod/semver"
)

const releaseTagPattern = `^(v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))(?:-(preview|rc)\.([1-9][0-9]*))?$`

var releaseTag = regexp.MustCompile(releaseTagPattern)
var rcPin = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-rc\.[1-9][0-9]*$`)

type planningRepo interface {
	evidenceRepo
	Releases() ([]json.RawMessage, error)
}

func remotePolicy(repo evidenceRepo, ref string) (doc, error) {
	var blob struct{ Content string }
	if err := fetch(repo, "contents/.github/release-policy.json?ref="+ref, &blob); err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(blob.Content)
	if err != nil {
		return nil, err
	}
	return reports.Decode(raw)
}

func observe(value string, hours int64, now time.Time, reason string) error {
	published, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return errors.New("공개 시각이나 검증 기록 시각이 올바르지 않습니다")
	}
	age := now.Sub(published).Hours()
	if age < 0 {
		return errors.New("공개 시각이 현재보다 뒤인 릴리스는 사용할 수 없습니다")
	}
	if age < float64(hours) && reason == "" {
		return fmt.Errorf("관찰 기간 부족: %.1f시간 경과, %d시간 필요", age, hours)
	}
	return nil
}

func hours(policy doc, key string) int64 {
	n, _ := policy[key].(json.Number)
	result, _ := n.Int64()
	return result
}

func stableHistory(repo planningRepo, base string, interval int64, now time.Time, reason string) error {
	releases, err := repo.Releases()
	if err != nil {
		return err
	}
	var items []struct {
		Tag       string
		Published string
	}
	for _, raw := range releases {
		var item struct {
			Draft, Prerelease bool
			Tag               string `json:"tag_name"`
			Published         string `json:"published_at"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		if !item.Draft && !item.Prerelease && stableTag.MatchString(item.Tag) {
			if semver.Compare(base, item.Tag) <= 0 {
				return errors.New("기존 정식 버전보다 높은 버전만 발행할 수 있습니다")
			}
			items = append(items, struct {
				Tag       string
				Published string
			}{item.Tag, item.Published})
		}
	}
	var latest time.Time
	for _, item := range items {
		published, err := time.Parse(time.RFC3339Nano, item.Published)
		if err != nil {
			return errors.New("공개 시각이나 검증 기록 시각이 올바르지 않습니다")
		}
		if published.After(latest) {
			latest = published
		}
	}
	if latest.IsZero() {
		return nil
	}
	return observe(latest.Format(time.RFC3339Nano), interval, now, reason)
}

func planRelease(repo planningRepo, repository string, p doc, channel, version, source, reason string, now time.Time) (doc, error) {
	match := releaseTag.FindStringSubmatch(version)
	if match == nil {
		return nil, errors.New("버전은 vX.Y.Z-preview.N, vX.Y.Z-rc.N 또는 vX.Y.Z 형식이어야 합니다")
	}
	base, actual := match[1], match[2]
	if actual == "" {
		actual = "stable"
	}
	if base != field(p, "next_version") || actual != channel {
		return nil, errors.New("대상 버전, 채널이 정책의 next_version과 일치하지 않습니다")
	}
	if channel == "stable" {
		for _, pin := range object(object(p, "recipe"), "modules") {
			version, _ := pin.(string)
			if version == "preview" || strings.Contains(version, "-") {
				return nil, errors.New("정식 CLI의 새 기본 레시피에는 정식 모듈만 사용할 수 있습니다. 레시피 변경 후 새 Preview가 필요합니다")
			}
		}
	}
	reason = strings.TrimSpace(reason)
	if reason != "" && utf8.RuneCountInString(reason) < 20 {
		return nil, errors.New("긴급 단축 사유를 20자 이상 기록해야 합니다")
	}
	old, err := repo.TagSHA(version)
	if err != nil {
		return nil, err
	}
	if old != "" {
		return nil, errors.New("이미 존재하는 태그는 이동하거나 재사용하지 않습니다")
	}
	if channel != "stable" {
		old, err := repo.TagSHA(base)
		if err != nil {
			return nil, err
		}
		if old != "" {
			return nil, errors.New("이미 정식 발행한 버전에는 사전 릴리스를 추가할 수 없습니다")
		}
		var refs []struct{ Ref string }
		if err := fetch(repo, "git/matching-refs/tags/"+base+"-"+channel+".", &refs); err != nil {
			return nil, err
		}
		for _, ref := range refs {
			if existing := releaseTag.FindStringSubmatch(strings.TrimPrefix(ref.Ref, "refs/tags/")); existing != nil && existing[3] != "" {
				if semver.Compare("v0.0.0-"+match[3], "v0.0.0-"+existing[3]) <= 0 {
					return nil, errors.New("같은 채널의 새 번호는 기존 번호보다 커야 합니다")
				}
			}
		}
	}
	branches := object(p, "branches")
	branch := field(branches, channel)
	var head struct{ Object struct{ SHA string } }
	if err := fetch(repo, "git/ref/heads/"+branch, &head); err != nil {
		return nil, err
	}
	target := head.Object.SHA
	if !fullSHA.MatchString(target) {
		return nil, errors.New("채널 브랜치가 유효한 커밋을 가리키지 않습니다")
	}
	if workflowSHA := os.Getenv("GITHUB_SHA"); workflowSHA != "" && workflowSHA != target {
		return nil, errors.New("workflow 실행 뒤 채널 HEAD가 바뀌었습니다")
	}
	composition, err := components(p)
	if err != nil {
		return nil, err
	}
	prior, upstream := "", ""
	var pr, proof any
	if channel == "preview" {
		if source != target {
			return nil, errors.New("Preview 출처에는 preview 브랜치 HEAD의 전체 커밋 SHA가 필요합니다")
		}
	} else {
		prior = source
		match := releaseTag.FindStringSubmatch(source)
		if match == nil || match[1] != base {
			return nil, errors.New("다른 버전의 후보를 승격할 수 없습니다")
		}
		predecessor := "preview"
		if channel == "stable" {
			predecessor = "rc"
		}
		evidence, manifest, err := verifySource(repo, repository, source, predecessor, p)
		if err != nil {
			return nil, err
		}
		artifact := evidence.Artifact
		proof = doc{"artifact_id": json.Number(strconv.FormatInt(artifact.ID, 10)), "digest": artifact.Digest, "workflow_run_id": manifest["workflow_run_id"], "created_at": artifact.CreatedAt}
		upstream = field(manifest, "source_sha")
		pr, err = promote(repo, repository, target, upstream, branch, field(branches, predecessor))
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(manifest["components"], composition) {
			return nil, errors.New("후보 검증 뒤 호환 버전 조합이 바뀌었습니다. 새 Preview가 필요합니다")
		}
		if err := observe(artifact.CreatedAt, hours(p, predecessor+"_hours"), now, reason); err != nil {
			return nil, err
		}
	}
	var commit gitCommit
	if err := fetch(repo, "git/commits/"+target, &commit); err != nil {
		return nil, err
	}
	current, err := remotePolicy(repo, target)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(current, p) {
		return nil, errors.New("후보 커밋의 릴리스 정책이 현재 계획과 다릅니다. 대상 SHA를 확인하세요")
	}
	if err := approval.Verify(repo, field(p, "reviewer_login"), channel, branch); err != nil {
		return nil, err
	}
	if channel == "stable" {
		if err := stableHistory(repo, base, hours(p, "stable_interval_hours"), now, reason); err != nil {
			return nil, err
		}
	}
	attempt, ok := os.LookupEnv("GITHUB_RUN_ATTEMPT")
	if !ok {
		attempt = "1"
	}
	plan := doc{"schema_version": "spine-cli.release-manifest/v1", "channel": channel, "version": version, "source_sha": target, "source_tag": prior,
		"branch": branch, "source_tree": commit.Tree.SHA, "upstream_source_sha": upstream, "promotion_pr": pr, "components": composition, "source_evidence": proof,
		"observation_override_reason": reason, "planned_at": formatPlanTime(now),
		"workflow_run_id": os.Getenv("GITHUB_RUN_ID"), "workflow_run_attempt": attempt, "repository": repository, "required_public_os": p["required_public_os"]}
	if cohort := object(p, "rc_cohort"); channel == "rc" && len(cohort) > 0 {
		if len(cohort) != len(modules) {
			return nil, errors.New("RC 조합에는 코어와 일곱 플랫폼의 정확한 RC 태그가 필요합니다")
		}
		for _, module := range modules {
			if !rcPin.MatchString(field(cohort, module)) {
				return nil, errors.New("RC 조합에는 코어와 일곱 플랫폼의 정확한 RC 태그가 필요합니다")
			}
		}
		plan["rc_cohort"] = cohort
	}
	recipe := object(composition, "recipe")
	if proof != nil && plan["rc_cohort"] == nil && len(object(recipe, "queries")) == 0 {
		plan["public_validation_scope"] = "tag-install"
	}
	if channel == "preview" && field(object(p, "recipe"), "id") == "platform-contracts-preview" {
		evidence, err := upstreamEvidence(recipe, os.Getenv("RELEASE_EVIDENCE_TOKEN"))
		if err != nil {
			return nil, err
		}
		plan["upstream_evidence"] = evidence
	}
	body, err := releasenotes.Body(plan, field(p, "notes_path"))
	if err != nil {
		return nil, err
	}
	plan["notes_sha256"] = fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	return plan, nil
}

func appendOutput(path, text string) error {
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = file.WriteString(text)
	return errors.Join(err, file.Close())
}

func preparePlan(repo planningRepo, repository string, p doc, path string) error {
	channel := os.Getenv("RELEASE_CHANNEL")
	branch := field(object(p, "branches"), channel)
	if branch == "" || os.Getenv("GITHUB_REF") != "refs/heads/"+branch {
		return errors.New("채널에 맞는 preview, rc, main 브랜치에서 workflow를 실행해야 합니다")
	}
	version, source := os.Getenv("RELEASE_VERSION"), os.Getenv("RELEASE_SOURCE")
	if channel == "stable" {
		match := releaseTag.FindStringSubmatch(source)
		if match == nil {
			return errors.New("버전은 vX.Y.Z-preview.N, vX.Y.Z-rc.N 또는 vX.Y.Z 형식이어야 합니다")
		}
		version = match[1]
	}
	plan, err := planRelease(repo, repository, p, channel, version, source, os.Getenv("RELEASE_OVERRIDE_REASON"), time.Now())
	if err != nil {
		return err
	}
	target := field(plan, "source_sha")
	var summary strings.Builder
	reason := field(plan, "observation_override_reason")
	if reason == "" {
		reason = "없음"
	}
	fmt.Fprintf(&summary, "출시 계획: `%s` / `%s`\n\n관찰 기간 단축 사유: %s\n", version, target, reason)
	if channel == "preview" {
		var result struct {
			Runs []workflowRun `json:"workflow_runs"`
		}
		endpoint := "actions/workflows/ci.yml/runs?branch=preview&head_sha=" + target + "&event=push&status=success"
		if err := fetch(repo, endpoint, &result); err != nil {
			fmt.Printf("이전 Preview CI를 조회하지 못해 전체 CI를 실행합니다. %v\n", err)
		} else {
			for _, run := range result.Runs {
				if !matchesPreviewCI(repository, target, run) {
					continue
				}
				plan["preview_ci_evidence"] = doc{"id": run.ID, "run_attempt": run.Attempt}
				fmt.Fprintf(&summary, "같은 커밋의 Preview CI 실행 `%d`를 재사용합니다.\n", run.ID)
				break
			}
		}
	}
	if evidence := object(plan, "source_evidence"); evidence != nil {
		fmt.Fprintf(&summary, "공통 CI 재사용 근거는 `%s`의 Actions 실행 `%s`입니다.\n", field(plan, "source_tag"), field(evidence, "workflow_run_id"))
	}
	if err := releasenotes.WriteReview(plan, field(p, "notes_path"), "."); err != nil {
		return err
	}
	if err := writeDocument(path, plan); err != nil {
		return err
	}
	outputs := fmt.Sprintf("sha=%s\nversion=%s\ntag_only=%t\nreuse_ci=%t\nreuse_common=%t\n", target, version, field(plan, "public_validation_scope") == "tag-install", plan["preview_ci_evidence"] != nil, plan["source_evidence"] != nil)
	if err := appendOutput(os.Getenv("GITHUB_OUTPUT"), outputs); err != nil {
		return err
	}
	return appendOutput(os.Getenv("GITHUB_STEP_SUMMARY"), summary.String())
}

func publish(repo planningRepo, repository string, p, plan doc, directory string) error {
	target, version := field(plan, "source_sha"), field(plan, "version")
	if field(plan, "public_validation_scope") != "tag-install" {
		if _, err := reports.Collect(directory, plan, target); err != nil {
			return err
		}
	}
	if evidence := object(plan, "preview_ci_evidence"); len(evidence) > 0 {
		id, _ := evidence["id"].(json.Number)
		attempt, _ := evidence["run_attempt"].(json.Number)
		var run workflowRun
		if err := fetch(repo, "actions/runs/"+id.String(), &run); err != nil {
			return err
		}
		if field(plan, "channel") != "preview" || !matchesPreviewCI(repository, target, run) ||
			strconv.FormatInt(run.ID, 10) != id.String() || strconv.FormatInt(run.Attempt, 10) != attempt.String() {
			return errors.New("Preview CI의 실행 기록이 발행 계획과 다릅니다")
		}
	}
	if target == "" || os.Getenv("VERIFIED_SHA") != target {
		return errors.New("검증한 커밋과 발행 대상이 다릅니다")
	}
	branch := field(plan, "branch")
	if os.Getenv("GITHUB_REF") != "refs/heads/"+branch {
		return errors.New("릴리스 workflow의 실행 브랜치가 채널과 다릅니다")
	}
	current, err := remotePolicy(repo, branch)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, p) {
		return errors.New("계획을 만든 뒤 채널 브랜치의 릴리스 정책이 바뀌었습니다")
	}
	source := field(plan, "source_tag")
	if source == "" {
		source = target
	}
	fresh, err := planRelease(repo, repository, p, field(plan, "channel"), version, source, field(plan, "observation_override_reason"), time.Now())
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(fresh)
	if err != nil {
		return err
	}
	fresh, err = reports.Decode(encoded)
	if err != nil {
		return err
	}
	for _, key := range []string{"source_sha", "source_tree", "branch", "promotion_pr", "source_evidence", "channel", "version", "components", "notes_sha256", "required_public_os", "upstream_evidence", "rc_cohort", "public_validation_scope"} {
		if !reflect.DeepEqual(fresh[key], plan[key]) {
			return errors.New("승인한 계획이 바뀌었습니다. 새 실행이 필요합니다")
		}
	}
	_, err = repo.API("POST", "git/refs", doc{"ref": "refs/tags/" + version, "sha": target}, nil, false)
	return err
}

func formatPlanTime(now time.Time) string {
	now = now.UTC().Truncate(time.Microsecond)
	if now.Nanosecond() == 0 {
		return now.Format("2006-01-02T15:04:05-07:00")
	}
	return now.Format("2006-01-02T15:04:05.000000-07:00")
}
