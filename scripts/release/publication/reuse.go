package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasegithub"
	"github.com/NARUBROWN/spine-cli/scripts/release/internal/reports"
	"golang.org/x/mod/semver"
)

type evidenceRepo interface {
	API(string, string, any, any, bool) (bool, error)
	TagSHA(string) (string, error)
	CandidateEvidence(string, string, bool) (*releasegithub.Evidence, error)
}

func fetch(repo evidenceRepo, path string, out any) error {
	_, err := repo.API("GET", path, nil, out, false)
	return err
}

func components(policy doc) (doc, error) {
	raw, err := json.Marshal(policy["recipe"])
	if err != nil {
		return nil, err
	}
	recipe, err := reports.Decode(raw)
	if err != nil {
		return nil, err
	}
	queries := make(doc)
	pins := object(recipe, "modules")
	for module, version := range pins {
		if version != "preview" {
			continue
		}
		selected, err := resolve(module, "preview")
		if err != nil {
			return nil, err
		}
		pins[module] = selected
		queries[module] = "preview"
	}
	if len(queries) > 0 {
		recipe["queries"] = queries
	}
	raw, err = json.Marshal(recipe)
	if err != nil {
		return nil, err
	}
	return doc{"recipe": recipe, "recipe_sha256": fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}

type gitCommit struct {
	Parents []struct{ SHA string }
	Tree    struct{ SHA string }
}
type pullRequest struct {
	Number   int64
	MergedAt string `json:"merged_at"`
	MergeSHA string `json:"merge_commit_sha"`
	Base     struct{ Ref string }
	Head     struct {
		Ref, SHA string
		Repo     struct {
			FullName string `json:"full_name"`
		}
	}
}
type workflowRun struct {
	ID                              int64
	Attempt                         int64 `json:"run_attempt"`
	Status, Conclusion, Event, Path string
	HeadSHA                         string `json:"head_sha"`
	HeadBranch                      string `json:"head_branch"`
	HeadRepository                  struct {
		FullName string `json:"full_name"`
	} `json:"head_repository"`
}

func matchesPreviewCI(repository, source string, run workflowRun) bool {
	return run.Status == "completed" && run.Conclusion == "success" && run.Event == "push" &&
		run.HeadBranch == "preview" && run.HeadSHA == source && run.HeadRepository.FullName == repository && run.Path == ".github/workflows/ci.yml"
}

func promote(repo evidenceRepo, repository, source, upstream, branch, upstreamBranch string) (doc, error) {
	var merge, base gitCommit
	if err := fetch(repo, "git/commits/"+source, &merge); err != nil {
		return nil, err
	}
	if err := fetch(repo, "git/commits/"+upstream, &base); err != nil {
		return nil, err
	}
	if len(merge.Parents) != 2 || merge.Parents[1].SHA != upstream {
		return nil, errors.New("채널 승격은 검증한 후보를 두 번째 부모로 갖는 merge commit이어야 합니다")
	}
	if merge.Tree.SHA == "" || merge.Tree.SHA != base.Tree.SHA {
		return nil, errors.New("승격 merge가 후보 소스를 변경했습니다. 새 Preview에서 다시 검증해야 합니다")
	}
	var pulls []pullRequest
	if err := fetch(repo, "commits/"+source+"/pulls?per_page=100", &pulls); err != nil {
		return nil, err
	}
	for _, pr := range pulls {
		if pr.MergedAt != "" && pr.MergeSHA == source && pr.Base.Ref == branch && pr.Head.Ref == upstreamBranch && pr.Head.Repo.FullName == repository {
			return doc{"number": pr.Number, "head": upstreamBranch, "base": branch, "merge_sha": source, "head_sha": upstream}, nil
		}
	}
	return nil, fmt.Errorf("%s → %s 방향의 병합된 PR이 필요합니다", upstreamBranch, branch)
}

func verifySource(repo evidenceRepo, repository, version, channel string, policy doc) (*releasegithub.Evidence, doc, error) {
	valid := previewTag.MatchString(version)
	if channel == "rc" {
		valid = strings.Contains(version, "-rc.") && previewTag.MatchString(strings.Replace(version, "-rc.", "-preview.", 1))
	}
	if !valid {
		return nil, nil, fmt.Errorf("이 단계의 출처는 %s 태그여야 합니다", channel)
	}
	proof, err := repo.CandidateEvidence(version, "release-manifest-", false)
	if err != nil {
		return nil, nil, err
	}
	if proof == nil {
		return nil, nil, errors.New("후보 검증 기록이 없습니다")
	}
	sha, err := repo.TagSHA(version)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := reports.Decode(proof.Manifest)
	if err != nil {
		return nil, nil, err
	}
	branch := field(object(policy, "branches"), channel)
	validation := object(manifest, "validation")
	mismatch := errors.New("출처 태그와 검증 manifest가 일치하지 않습니다")
	if field(manifest, "schema_version") != "spine-cli.release-manifest/v1" || field(manifest, "version") != version ||
		field(manifest, "channel") != channel || !fullSHA.MatchString(sha) || field(manifest, "source_sha") != sha ||
		field(manifest, "repository") != repository || field(manifest, "branch") != branch {
		return nil, nil, mismatch
	}
	var commit gitCommit
	if err := fetch(repo, "git/commits/"+sha, &commit); err != nil {
		return nil, nil, err
	}
	if field(manifest, "source_tree") != commit.Tree.SHA || field(validation, "result") != "passed" ||
		field(validation, "source_sha") != sha || field(validation, "version") != version || !reflect.DeepEqual(validation["components"], manifest["components"]) {
		return nil, nil, mismatch
	}
	artifact := proof.Artifact
	var run workflowRun
	if err := json.Unmarshal(proof.WorkflowRun, &run); err != nil {
		return nil, nil, err
	}
	path, _, _ := strings.Cut(run.Path, "@")
	if run.Status != "completed" || run.Conclusion != "success" || run.Event != "workflow_dispatch" || run.HeadBranch != branch || run.HeadSHA != sha ||
		strconv.FormatInt(run.ID, 10) != field(manifest, "workflow_run_id") || strconv.FormatInt(run.Attempt, 10) != field(manifest, "workflow_run_attempt") ||
		artifact.Name != "release-manifest-"+version || artifact.Expired == nil || *artifact.Expired || artifact.WorkflowRun.ID != run.ID ||
		artifact.WorkflowRun.HeadSHA != sha || artifact.WorkflowRun.HeadBranch != branch || path != ".github/workflows/"+channel+".yml" {
		return nil, nil, errors.New("출처 태그의 검증 workflow와 artifact가 일치하지 않거나 성공하지 않았습니다")
	}
	if !reflect.DeepEqual(manifest["required_public_os"], policy["required_public_os"]) {
		return nil, nil, errors.New("출처 후보의 공개 설치 OS가 현재 정책과 다릅니다")
	}
	var rows []doc
	values, _ := validation["reports"].([]any)
	for _, raw := range values {
		report, ok := raw.(doc)
		if !ok {
			return nil, nil, errors.New("잘못된 공개 설치 결과")
		}
		rows = append(rows, report)
	}
	if err := reports.Validate(rows, manifest, version); err != nil {
		return nil, nil, err
	}
	return proof, manifest, nil
}

func promotionCandidate(repo evidenceRepo, repository string, policy doc, event, branch, source string, pull *pullRequest) (string, int64, error) {
	upstream := map[string]string{"rc": "preview", "main": "rc"}[branch]
	if upstream == "" || (event != "push" && event != "pull_request") {
		return "", 0, nil
	}
	if event == "pull_request" && (pull == nil || pull.Base.Ref != branch || pull.Head.Ref != upstream || pull.Head.Repo.FullName != repository) {
		return "", 0, nil
	}
	var merge, base gitCommit
	if err := fetch(repo, "git/commits/"+source, &merge); err != nil {
		return "", 0, err
	}
	if len(merge.Parents) != 2 {
		return "", 0, nil
	}
	head := merge.Parents[1].SHA
	if head == "" || pull != nil && head != pull.Head.SHA {
		return "", 0, nil
	}
	if err := fetch(repo, "git/commits/"+head, &base); err != nil {
		return "", 0, err
	}
	if merge.Tree.SHA == "" || merge.Tree.SHA != base.Tree.SHA {
		return "", 0, nil
	}
	prefix := field(policy, "next_version") + "-" + upstream + "."
	var refs []struct{ Ref string }
	if err := fetch(repo, "git/matching-refs/tags/"+prefix, &refs); err != nil {
		return "", 0, err
	}
	var tags []string
	for _, ref := range refs {
		tag := strings.TrimPrefix(ref.Ref, "refs/tags/")
		if strings.HasPrefix(tag, prefix) && previewTag.MatchString(strings.Replace(tag, "-rc.", "-preview.", 1)) {
			tags = append(tags, tag)
		}
	}
	slices.SortFunc(tags, func(a, b string) int { return semver.Compare(b, a) })
	for _, tag := range tags {
		sha, err := repo.TagSHA(tag)
		if err != nil {
			return "", 0, err
		}
		if sha != head {
			continue
		}
		proof, manifest, err := verifySource(repo, repository, tag, upstream, policy)
		if err != nil {
			return "", 0, err
		}
		current, err := components(policy)
		if err != nil {
			return "", 0, err
		}
		if !reflect.DeepEqual(manifest["components"], current) {
			return "", 0, nil
		}
		if event == "push" {
			if _, err := promote(repo, repository, source, head, branch, upstream); err != nil {
				return "", 0, err
			}
		}
		return tag, proof.Artifact.ID, nil
	}
	return "", 0, nil
}

func runPromotionCheck(repo evidenceRepo, repository string, policy doc) error {
	event, branch := os.Getenv("GITHUB_EVENT_NAME"), os.Getenv("GITHUB_REF_NAME")
	var pull *pullRequest
	if event == "pull_request" {
		data, err := os.ReadFile(os.Getenv("GITHUB_EVENT_PATH"))
		if err != nil {
			return err
		}
		var payload struct {
			Pull *pullRequest `json:"pull_request"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		pull = payload.Pull
		if pull == nil {
			return errors.New("pull_request event is missing")
		}
		branch = pull.Base.Ref
	}
	tag, id, err := promotionCandidate(repo, repository, policy, event, branch, os.Getenv("GITHUB_SHA"), pull)
	if err != nil {
		fmt.Printf("이전 검증을 재사용할 수 없어 전체 CI를 실행합니다. %v\n", err)
		return nil
	}
	if tag == "" {
		return nil
	}
	output, err := os.OpenFile(os.Getenv("GITHUB_OUTPUT"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "candidate=%s\n", tag)
	if err := errors.Join(err, output.Close()); err != nil {
		return err
	}
	fmt.Printf("%s의 검증 artifact %d를 재사용합니다.\n", tag, id)
	return nil
}
