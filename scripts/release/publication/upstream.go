package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasegithub"
	"github.com/NARUBROWN/spine-cli/scripts/release/internal/reports"
	"golang.org/x/mod/semver"
)

const core = "github.com/NARUBROWN/spine"

var modules = []string{core, core + "-echo-platform", core + "-nethttp-platform", core + "-gorilla-platform", core + "-kafka-platform", core + "-rabbitmq-platform", core + "-nats-platform", core + "-validator-playground"}
var previewTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-preview\.[1-9][0-9]*$`)
var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var pseudoSuffix = regexp.MustCompile(`[.-][0-9]{14}-([0-9a-f]{12})$`)
var fixedVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

type doc = reports.Document

func field(d doc, key string) string { value, _ := d[key].(string); return value }
func object(d doc, key string) doc   { value, _ := d[key].(doc); return value }

type upstreamClient interface {
	API(string, string, any, any, bool) (bool, error)
	TagSHA(string) (string, error)
	CandidateEvidence(string, string, bool) (*releasegithub.Evidence, error)
}

func get(c upstreamClient, path string, out any) error {
	_, err := c.API("GET", path, nil, out, false)
	return err
}

func latest(c upstreamClient, repository string) (string, error) {
	var refs []struct{ Ref string }
	if err := get(c, "git/matching-refs/tags/v", &refs); err != nil {
		return "", err
	}
	var best string
	for _, ref := range refs {
		tag := strings.TrimPrefix(ref.Ref, "refs/tags/")
		if previewTag.MatchString(tag) && semver.Compare(tag, best) > 0 {
			best = tag
		}
	}
	if best == "" {
		return "", fmt.Errorf("Preview 태그가 없는 선행 저장소입니다: %s", repository)
	}
	return best, nil
}

func compatible(c upstreamClient, tagged, version, module string) (string, error) {
	suffix := pseudoSuffix.FindStringSubmatch(version)
	if suffix == nil {
		return "", nil
	}
	var ref struct{ Object struct{ SHA string } }
	if err := get(c, "git/ref/heads/preview", &ref); err != nil {
		return "", err
	}
	head := ref.Object.SHA
	if !fullSHA.MatchString(head) || !strings.HasPrefix(head, suffix[1]) {
		return "", nil
	}
	var comparison struct {
		Status    string
		MergeBase struct{ SHA string } `json:"merge_base_commit"`
		Files     []struct{ Filename, Status string }
	}
	if err := get(c, "compare/"+tagged+"..."+head, &comparison); err != nil {
		return "", err
	}
	allowed := map[string]bool{"README.md": true, "README.en.md": true, "README.ja.md": true, "README.zh-Hans.md": true, "README.zh-Hant.md": true}
	switch module {
	case core, core + "-echo-platform":
		allowed["scripts/release/release.py"] = true
		allowed["scripts/release/verify_public.py"] = true
		if module == core {
			allowed["scripts/release/test_release.py"] = true
		} else {
			allowed["test/release/test_release.py"] = true
		}
	}
	if comparison.Status != "ahead" || comparison.MergeBase.SHA != tagged || len(comparison.Files) == 0 || len(comparison.Files) > len(allowed) {
		return "", nil
	}
	for _, file := range comparison.Files {
		if file.Status != "modified" || !allowed[file.Filename] {
			return "", nil
		}
	}
	return head, nil
}

func snapshot(c upstreamClient, module string) (doc, error) {
	repository := strings.TrimPrefix(module, "github.com/")
	tag, err := latest(c, repository)
	if err != nil {
		return nil, err
	}
	sha, err := c.TagSHA(tag)
	if err != nil {
		return nil, err
	}
	if !fullSHA.MatchString(sha) {
		return nil, fmt.Errorf("선행 저장소의 Preview 태그가 올바른 커밋을 가리키지 않습니다: %s", repository)
	}
	scope, prefix := "platform", "release-manifest-"
	if module == core {
		scope, prefix = "core", "core-candidate-"
	}
	proof, err := c.CandidateEvidence(tag, prefix, module != core)
	if err != nil {
		return nil, err
	}
	result := doc{"version": tag, "source_sha": sha, "artifact_id": nil, "digest": nil}
	if proof == nil {
		if module == core {
			return nil, errors.New("코어 후보의 발행 검증 기록이 없습니다")
		}
		return result, nil
	}
	manifest, err := reports.Decode(proof.Manifest)
	if err != nil {
		return nil, err
	}
	var run struct {
		ID                              int64
		Status, Conclusion, Event, Path string
		HeadBranch                      string `json:"head_branch"`
		HeadSHA                         string `json:"head_sha"`
		Attempt                         int64  `json:"run_attempt"`
	}
	if err := json.Unmarshal(proof.WorkflowRun, &run); err != nil {
		return nil, err
	}
	var commit struct{ Tree struct{ SHA string } }
	if err := get(c, "git/commits/"+sha, &commit); err != nil {
		return nil, err
	}
	validation, components := object(manifest, "validation"), object(manifest, "components")
	selected := object(validation, "selected_modules")
	workflow, _, _ := strings.Cut(run.Path, "@")
	if field(manifest, "schema_version") != "spine.release-manifest/v1" || field(manifest, "repository") != repository ||
		field(manifest, "module") != module || field(manifest, "kind") != scope || field(manifest, "channel") != "preview" || field(manifest, "branch") != "preview" ||
		field(manifest, "version") != tag || field(manifest, "source_sha") != sha || field(manifest, "source_tree") != commit.Tree.SHA ||
		field(validation, "result") != "passed" || field(validation, "scope") != scope || field(validation, "version") != tag || field(validation, "source_sha") != sha ||
		!reflect.DeepEqual(validation["components"], manifest["components"]) || field(selected, module) != tag ||
		run.Status != "completed" || run.Conclusion != "success" || run.Event != "workflow_dispatch" || run.HeadBranch != "preview" || run.HeadSHA != sha ||
		workflow != ".github/workflows/preview.yml" || strconv.FormatInt(run.ID, 10) != field(manifest, "workflow_run_id") ||
		strconv.FormatInt(run.Attempt, 10) != field(manifest, "workflow_run_attempt") || proof.Artifact.WorkflowRun.ID != run.ID || proof.Artifact.WorkflowRun.HeadSHA != sha {
		return nil, fmt.Errorf("선행 발행의 태그와 공개 검증 기록이 다릅니다: %s", repository)
	}
	pin := field(components, "core_version")
	if module == core && pin != tag {
		return nil, fmt.Errorf("코어 후보의 구성 버전이 태그와 다릅니다: %s", repository)
	}
	if module != core && (field(selected, core) == "" || pin != "" && field(selected, core) != pin) {
		return nil, fmt.Errorf("선행 플랫폼의 코어 검증 기록이 일치하지 않습니다: %s", repository)
	}
	result["artifact_id"] = json.Number(strconv.FormatInt(proof.Artifact.ID, 10))
	result["digest"] = proof.Artifact.Digest
	return result, nil
}

func resolve(module, query string) (string, error) {
	dir, err := os.MkdirTemp("", "spine-cli-preview-recipe-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	env := append(os.Environ(), "GOWORK=off", "GOFLAGS= ", "GOTOOLCHAIN=local")
	command := func(args ...string) ([]byte, error) {
		cmd := exec.Command("go", args...)
		cmd.Dir, cmd.Env = dir, env
		output, err := cmd.Output()
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("명령 실패: go %s\n%s", args[0], strings.TrimSpace(string(exit.Stderr)))
		}
		return output, err
	}
	proxy, err := command("env", "GOPROXY")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(proxy)) == "https://proxy.golang.org,direct" {
		env = append(env, "GOPROXY=direct")
	}
	raw, err := command("list", "-m", "-json", module+"@"+query)
	if err != nil {
		return "", err
	}
	result, err := reports.Decode(raw)
	if err != nil {
		return "", errors.New("Preview 모듈 조회 결과가 올바른 JSON이 아닙니다")
	}
	version := field(result, "Version")
	if field(result, "Path") != module || result["Replace"] != nil || !fixedVersion.MatchString(version) {
		return "", fmt.Errorf("Preview 모듈을 고정 버전으로 해석하지 못했습니다: %s", module)
	}
	return version, nil
}

func upstreamEvidence(recipe doc, token string) (doc, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("선행 저장소의 Actions 검증 기록을 읽을 RELEASE_EVIDENCE_TOKEN이 필요합니다")
	}
	return collectUpstreamEvidence(recipe, resolve, func(repository string) (upstreamClient, error) {
		return releasegithub.New(repository, token)
	})
}

func collectUpstreamEvidence(recipe doc, resolveVersion func(string, string) (string, error), clientFor func(string) (upstreamClient, error)) (doc, error) {
	snapshots := make(doc)
	clients := make(map[string]upstreamClient)
	for _, module := range modules {
		c, err := clientFor(strings.TrimPrefix(module, "github.com/"))
		if err != nil {
			return nil, err
		}
		clients[module] = c
		result, err := snapshot(c, module)
		if err != nil {
			return nil, err
		}
		snapshots[module] = result
	}
	for _, module := range modules {
		selected, err := resolveVersion(module, "preview")
		if err != nil {
			return nil, err
		}
		object(snapshots, module)["selected_version"] = selected
	}
	for module, version := range object(recipe, "modules") {
		item := object(snapshots, module)
		if item == nil {
			continue
		}
		want, _ := version.(string)
		mismatch := fmt.Errorf("CLI 생성 레시피와 선행 태그가 다릅니다: %s", module)
		if field(item, "selected_version") != want {
			return nil, mismatch
		}
		if field(item, "version") == want {
			continue
		}
		head, err := compatible(clients[module], field(item, "source_sha"), want, module)
		if err != nil {
			return nil, err
		}
		if head == "" {
			return nil, mismatch
		}
		item["selected_source_sha"] = head
	}
	return snapshots, nil
}
