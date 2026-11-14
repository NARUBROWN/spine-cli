package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasegithub"
)

const core = "github.com/NARUBROWN/spine"
const cli = core + "-cli"
const runsPath = "actions/workflows/preview.yml/runs?event=workflow_dispatch&branch=preview&per_page=100"

var order = []string{core, core + "-echo-platform", core + "-nethttp-platform", core + "-gorilla-platform", core + "-kafka-platform", core + "-rabbitmq-platform", core + "-nats-platform", core + "-validator-playground", cli}
var tagPattern = regexp.MustCompile(`^(v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*))(?:-(preview|rc)\.([1-9][0-9]*))?$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type candidate struct {
	Version string `json:"version"`
	SHA     string `json:"source_sha"`
}

type repository interface {
	API(method, path string, input, output any, allowMissing bool) (bool, error)
	TagSHA(string) (string, error)
	CandidateEvidence(string, string, bool) (*releasegithub.Evidence, error)
}

type factory func(string) (repository, error)

type workflowRun struct {
	ID         int64
	Attempt    int64 `json:"run_attempt"`
	Status     string
	Conclusion string
	Event      string
	SHA        string `json:"head_sha"`
	Branch     string `json:"head_branch"`
	Path       string
}

func fetch(repo repository, path string, out any) error {
	_, err := repo.API("GET", path, nil, out, false)
	return err
}

func runs(repo repository) ([]workflowRun, error) {
	var response struct {
		Runs []workflowRun `json:"workflow_runs"`
	}
	err := fetch(repo, runsPath, &response)
	return response.Runs, err
}

func busy(runs []workflowRun) bool {
	for _, run := range runs {
		if run.Status != "completed" {
			return true
		}
	}
	return false
}

func head(repo repository) (string, error) {
	var ref struct{ Object struct{ SHA string } }
	err := fetch(repo, "git/ref/heads/preview", &ref)
	return ref.Object.SHA, err
}

func tags(repo repository, prefix string) ([]string, error) {
	var refs []struct{ Ref string }
	if err := fetch(repo, "git/matching-refs/tags/"+prefix, &refs); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		result = append(result, strings.TrimPrefix(ref.Ref, "refs/tags/"))
	}
	return result, nil
}

func numericGreater(left, right string) bool {
	if len(left) != len(right) {
		return len(left) > len(right)
	}
	return left > right
}

func latest(repo repository) (string, error) {
	names, err := tags(repo, "v")
	if err != nil {
		return "", err
	}
	var best []string
	for _, name := range names {
		match := tagPattern.FindStringSubmatch(name)
		if match == nil || match[5] != "preview" {
			continue
		}
		if best == nil {
			best = match
			continue
		}
		for _, index := range []int{2, 3, 4, 6} {
			if match[index] == best[index] {
				continue
			}
			if numericGreater(match[index], best[index]) {
				best = match
			}
			break
		}
	}
	if best == nil {
		return "", errors.New("Preview 태그가 없는 선행 저장소입니다")
	}
	return best[0], nil
}

type components struct {
	CoreVersion string `json:"core_version"`
}

type validation struct {
	Result     string
	Version    string
	SHA        string `json:"source_sha"`
	Components map[string]any
	Scope      string
	Selected   map[string]string `json:"selected_modules"`
}

type manifest struct {
	Schema     string `json:"schema_version"`
	Repository string
	Version    string
	SHA        string `json:"source_sha"`
	Channel    string
	Branch     string
	Validation validation
	Components map[string]any
	RunID      string `json:"workflow_run_id"`
	Attempt    string `json:"workflow_run_attempt"`
	Kind       string
	Module     string
	Tree       string `json:"source_tree"`
}

func checkExisting(repo repository, module string, item candidate, coreVersion string) error {
	prefix := "release-manifest-"
	if module == core {
		prefix = "core-candidate-"
	}
	proof, err := repo.CandidateEvidence(item.Version, prefix, module != core && module != cli)
	if err != nil {
		return err
	}
	if proof == nil {
		if module == core || module == cli {
			return fmt.Errorf("코어 또는 CLI의 발행 검증 기록이 없습니다: %s", module)
		}
		return nil
	}
	var m manifest
	var run workflowRun
	if err := json.Unmarshal(proof.Manifest, &m); err != nil {
		return err
	}
	if err := json.Unmarshal(proof.WorkflowRun, &run); err != nil {
		return err
	}
	report := m.Validation
	schema := "spine.release-manifest/v1"
	if module == cli {
		schema = "spine-cli.release-manifest/v1"
	}
	workflow, _, _ := strings.Cut(run.Path, "@")
	if m.Schema != schema || m.Repository != strings.TrimPrefix(module, "github.com/") ||
		m.Version != item.Version || m.SHA != item.SHA || m.Channel != "preview" || m.Branch != "preview" ||
		report.Result != "passed" || report.Version != item.Version || report.SHA != item.SHA ||
		!reflect.DeepEqual(report.Components, m.Components) ||
		run.Status != "completed" || run.Conclusion != "success" || run.Event != "workflow_dispatch" ||
		run.SHA != item.SHA || run.Branch != "preview" || workflow != ".github/workflows/preview.yml" ||
		proof.Artifact.WorkflowRun.ID != run.ID || proof.Artifact.WorkflowRun.HeadSHA != item.SHA ||
		strconv.FormatInt(run.ID, 10) != m.RunID || strconv.FormatInt(run.Attempt, 10) != m.Attempt {
		return fmt.Errorf("기존 태그의 발행 검증이 완료되지 않았습니다: %s", module)
	}
	if module == cli {
		return nil
	}
	var commit struct{ Tree struct{ SHA string } }
	if err := fetch(repo, "git/commits/"+item.SHA, &commit); err != nil {
		return err
	}
	scope := "platform"
	if module == core {
		scope = "core"
	}
	pinned, _ := m.Components["core_version"].(string)
	if m.Kind != scope || m.Module != module || report.Scope != scope || m.Tree != commit.Tree.SHA ||
		pinned == "" || module == core && pinned != coreVersion || report.Selected[module] != item.Version || report.Selected[core] != pinned {
		return fmt.Errorf("기존 태그의 발행 검증 기록이 일치하지 않습니다: %s", module)
	}
	return nil
}

func loadPlan(data []byte) (map[string]candidate, error) {
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if len(raw) != len(order) {
		return nil, errors.New("코어, 모든 플랫폼, CLI 후보를 정확히 한 번 지정해야 합니다")
	}
	plan := make(map[string]candidate)
	for _, module := range order {
		value, ok := raw[module]
		if !ok {
			return nil, errors.New("코어, 모든 플랫폼, CLI 후보를 정확히 한 번 지정해야 합니다")
		}
		if len(value) != 2 || value["version"] == nil || value["source_sha"] == nil {
			return nil, fmt.Errorf("후보마다 version과 source_sha가 필요합니다: %s", module)
		}
		var item candidate
		if err := json.Unmarshal(value["version"], &item.Version); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(value["source_sha"], &item.SHA); err != nil {
			return nil, err
		}
		plan[module] = item
	}
	return plan, nil
}

func validate(plan map[string]candidate, open factory) error {
	for _, module := range order {
		item := plan[module]
		version := tagPattern.FindStringSubmatch(item.Version)
		if version == nil || version[5] != "preview" || !shaPattern.MatchString(item.SHA) {
			return fmt.Errorf("Preview 버전과 전체 소스 SHA가 필요합니다: %s", module)
		}
		repo, err := open(module)
		if err != nil {
			return err
		}
		tagged, err := repo.TagSHA(item.Version)
		if err != nil {
			return err
		}
		if tagged != "" {
			if tagged != item.SHA {
				return fmt.Errorf("기존 태그가 계획의 커밋과 다릅니다: %s", module)
			}
			if module != cli {
				newest, err := latest(repo)
				if err != nil {
					return err
				}
				if newest != item.Version {
					return fmt.Errorf("계획의 선행 태그가 최신 Preview 태그와 다릅니다: %s", module)
				}
			}
			if err := checkExisting(repo, module, item, plan[core].Version); err != nil {
				return err
			}
			continue
		}
		if err := validateNewCandidate(repo, module, item, plan[core], version); err != nil {
			return err
		}
	}
	return nil
}

func validateNewCandidate(repo repository, module string, item, coreItem candidate, version []string) error {
	source, err := head(repo)
	if err != nil {
		return err
	}
	if source != item.SHA {
		return fmt.Errorf("Preview HEAD가 계획과 다릅니다: %s", module)
	}
	var response struct{ Content string }
	if err := fetch(repo, "contents/.github/release-policy.json?ref="+source, &response); err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(response.Content)
	if err != nil {
		return err
	}
	var policy struct {
		Next     string `json:"next_version"`
		CoreSHA  string `json:"ci_core"`
		Channels map[string]components
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		return err
	}
	if version[1] != policy.Next {
		return fmt.Errorf("후보 버전이 저장소의 다음 버전과 다릅니다: %s", module)
	}
	selected := policy.Channels["preview"].CoreVersion
	if module == core && selected != item.Version {
		return errors.New("코어 정책의 Preview 버전이 계획과 다릅니다")
	}
	if module != core && module != cli && (selected != coreItem.Version || policy.CoreSHA != coreItem.SHA) {
		return fmt.Errorf("플랫폼의 코어 버전 또는 CI 소스가 계획과 다릅니다: %s", module)
	}
	existing, err := tags(repo, version[1]+"-preview.")
	if err != nil {
		return err
	}
	for _, name := range existing {
		if tag := tagPattern.FindStringSubmatch(name); tag != nil && !numericGreater(version[6], tag[6]) {
			return fmt.Errorf("후보 번호가 이미 발행된 번호보다 크지 않습니다: %s", module)
		}
	}
	previous, err := runs(repo)
	if err != nil {
		return err
	}
	if busy(previous) {
		return fmt.Errorf("이미 진행 중인 Preview 발행이 있습니다: %s", module)
	}
	return nil
}

func dispatch(repo repository, module string, item candidate, coreVersion string, clock func() time.Time, sleep func(time.Duration), out io.Writer) error {
	current, err := repo.TagSHA(item.Version)
	if err != nil {
		return err
	}
	if current != "" {
		if current != item.SHA {
			return fmt.Errorf("기존 태그가 요청한 커밋과 다릅니다: %s", module)
		}
		return checkExisting(repo, module, item, coreVersion)
	}
	source, err := head(repo)
	if err != nil {
		return err
	}
	if source != item.SHA {
		return fmt.Errorf("발행 전에 Preview HEAD가 바뀌었습니다: %s", module)
	}
	previous, err := runs(repo)
	if err != nil {
		return err
	}
	if busy(previous) {
		return fmt.Errorf("이미 진행 중인 Preview 발행이 있습니다: %s", module)
	}
	known := make(map[int64]bool)
	for _, run := range previous {
		known[run.ID] = true
	}
	input := struct {
		Ref    string    `json:"ref"`
		Inputs candidate `json:"inputs"`
	}{"preview", item}
	if _, err := repo.API("POST", "actions/workflows/preview.yml/dispatches", input, nil, false); err != nil {
		return err
	}
	deadline := clock().Add(6 * time.Hour)
	var id int64
	for clock().Before(deadline) {
		if id == 0 {
			available, err := runs(repo)
			if err != nil {
				return err
			}
			for _, run := range available {
				if known[run.ID] || run.SHA != item.SHA {
					continue
				}
				if id != 0 {
					return fmt.Errorf("새 발행 실행을 유일하게 식별할 수 없습니다: %s", module)
				}
				id = run.ID
			}
			if id != 0 {
				fmt.Fprintf(out, "%s: 발행 실행 %d\n", module, id)
			}
		} else {
			var run workflowRun
			if err := fetch(repo, "actions/runs/"+strconv.FormatInt(id, 10), &run); err != nil {
				return err
			}
			if run.Status == "completed" {
				if run.Conclusion != "success" {
					return fmt.Errorf("발행 실행 또는 태그 검증에 실패했습니다: %s", module)
				}
				tag, err := repo.TagSHA(item.Version)
				if err != nil {
					return err
				}
				if tag != item.SHA {
					return fmt.Errorf("발행 실행 또는 태그 검증에 실패했습니다: %s", module)
				}
				return checkExisting(repo, module, item, coreVersion)
			}
		}
		sleep(15 * time.Second)
	}
	return fmt.Errorf("발행 완료 대기 시간이 초과됐습니다. 승인과 실행 상태를 확인하세요: %s", module)
}

func runTrain(plan map[string]candidate, execute bool, open factory, publish func(repository, string, candidate, string) error, out io.Writer) error {
	if err := validate(plan, open); err != nil {
		return err
	}
	for _, module := range order {
		item := plan[module]
		if !execute {
			if _, err := fmt.Fprintf(out, "%s: %s %s\n", module, item.Version, item.SHA); err != nil {
				return err
			}
			continue
		}
		repo, err := open(module)
		if err != nil {
			return err
		}
		if err := publish(repo, module, item, plan[core].Version); err != nil {
			return err
		}
	}
	return nil
}

func run() error {
	path := flag.String("plan", "", "저장소별 Preview 버전과 소스 SHA를 담은 JSON")
	execute := flag.Bool("execute", false, "검토한 계획의 발행 workflow를 순서대로 실행")
	flag.Parse()
	if *path == "" || flag.NArg() != 0 {
		return errors.New("--plan is required")
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	plan, err := loadPlan(data)
	if err != nil {
		return err
	}
	open := func(module string) (repository, error) {
		return releasegithub.New(strings.TrimPrefix(module, "github.com/"), "")
	}
	publish := func(repo repository, module string, item candidate, coreVersion string) error {
		return dispatch(repo, module, item, coreVersion, time.Now, time.Sleep, os.Stdout)
	}
	return runTrain(plan, *execute, open, publish, os.Stdout)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "릴리스 중단: "+err.Error())
		os.Exit(1)
	}
}
