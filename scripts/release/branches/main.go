package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/approval"
	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasegithub"
)

type object = map[string]any
type api interface {
	API(string, string, any, any, bool) (bool, error)
}

var branches = []string{"preview", "rc", "main"}
var shaPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func spec(name, target, ref string, rules []object) object {
	return object{"name": name, "target": target, "enforcement": "active", "bypass_actors": []any{},
		"conditions": object{"ref_name": object{"include": []string{ref}, "exclude": []string{}}}, "rules": rules}
}
func branchSpec(branch string) object {
	methods := []string{"merge"}
	if branch == "preview" {
		methods = append(methods, "squash", "rebase")
	}
	return spec("Spine CLI channel / "+branch, "branch", "refs/heads/"+branch, []object{
		{"type": "deletion"}, {"type": "non_fast_forward"},
		{"type": "pull_request", "parameters": object{
			"allowed_merge_methods": methods, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": false,
			"require_last_push_approval": false, "required_approving_review_count": 0, "required_review_thread_resolution": true}},
		{"type": "required_status_checks", "parameters": object{"strict_required_status_checks_policy": false,
			"required_status_checks": []object{{"context": "Verified Source / " + branch, "integration_id": 15368}, {"context": "Branch Flow / " + branch, "integration_id": 15368}}}},
	})
}
func tagSpec() object {
	return spec("Spine CLI immutable release tags", "tag", "refs/tags/v*", []object{{"type": "deletion"}, {"type": "update"}})
}
func verifyRule(actual, expected object) error {
	data, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	var normalized object
	if err := json.Unmarshal(data, &normalized); err != nil {
		return err
	}
	for _, key := range []string{"name", "target", "enforcement", "bypass_actors", "conditions"} {
		if !reflect.DeepEqual(actual[key], normalized[key]) {
			return fmt.Errorf("브랜치 보호 규칙 불일치: %s / %s", expected["name"], key)
		}
	}
	rules := make(map[string]object)
	actualRules, _ := actual["rules"].([]any)
	for _, value := range actualRules {
		rule, ok := value.(object)
		if !ok {
			return errors.New("잘못된 브랜치 보호 규칙")
		}
		kind, _ := rule["type"].(string)
		rules[kind] = rule
	}
	for _, value := range normalized["rules"].([]any) {
		rule := value.(object)
		kind := rule["type"].(string)
		existing, ok := rules[kind]
		if !ok {
			return fmt.Errorf("필수 브랜치 보호 규칙이 없습니다: %s", kind)
		}
		params, _ := rule["parameters"].(object)
		current, _ := existing["parameters"].(object)
		for key, value := range params {
			if !reflect.DeepEqual(current[key], value) {
				return fmt.Errorf("브랜치 보호 매개변수가 다릅니다: %s", key)
			}
		}
	}
	return nil
}
func request(g api, method, path string, input, output any) error {
	_, err := g.API(method, path, input, output, false)
	return err
}
func bootstrap(g api, sha string) error {
	var ref struct{ Object struct{ SHA string } }
	if !shaPattern.MatchString(sha) {
		return errors.New("초기 전환은 현재 main의 전체 SHA를 지정해야 합니다")
	}
	if err := request(g, "GET", "git/ref/heads/main", nil, &ref); err != nil {
		return err
	}
	if ref.Object.SHA != sha {
		return errors.New("초기 전환은 현재 main의 전체 SHA를 지정해야 합니다")
	}
	for _, path := range []string{".github/release-policy.json", ".github/workflows/ci.yml", ".github/workflows/branch-flow.yml"} {
		if err := request(g, "GET", "contents/"+path+"?ref="+sha, nil, nil); err != nil {
			return err
		}
	}
	var missing []string
	for _, branch := range branches[:2] {
		ref.Object.SHA = ""
		found, err := g.API("GET", "git/ref/heads/"+branch, nil, &ref, true)
		if err != nil {
			return err
		}
		if found && ref.Object.SHA != sha {
			return fmt.Errorf("다른 커밋을 가진 기존 채널 브랜치를 변경하지 않습니다: %s", branch)
		}
		if !found {
			missing = append(missing, branch)
		}
	}
	for _, branch := range missing {
		if err := request(g, "POST", "git/refs", object{"ref": "refs/heads/" + branch, "sha": sha}, nil); err != nil {
			return err
		}
	}
	return nil
}

type policy struct {
	Reviewer string `json:"reviewer_login"`
	Branches map[string]string
}
type branchPolicy struct {
	Name string
	Type *string
}

func (p branchPolicy) matches(branch string) bool {
	return p.Name == branch && (p.Type == nil || *p.Type == "branch")
}
func branchPolicies(g api, environment string) ([]branchPolicy, error) {
	var result struct {
		Policies []branchPolicy `json:"branch_policies"`
	}
	err := request(g, "GET", "environments/"+environment+"/deployment-branch-policies", nil, &result)
	return result.Policies, err
}
func verifyApproval(g api, p policy, channel string) error {
	return approval.Verify(g, p.Reviewer, channel, p.Branches[channel])
}

func configure(g api, apply bool, p policy, lookup func(string) (int64, error)) error {
	legacy, err := g.API("GET", "branches/main/protection", nil, nil, true)
	if err != nil {
		return err
	}
	if legacy {
		return errors.New("기존 main 보호를 자동 변경하지 않습니다. 전환 전 호환성을 검토하세요")
	}
	if err := request(g, "GET", "contents/.github/workflows/branch-flow.yml?ref=preview", nil, nil); err != nil {
		return err
	}
	for _, branch := range branches {
		if err := request(g, "GET", "git/ref/heads/"+branch, nil, nil); err != nil {
			return err
		}
	}
	var existing []struct {
		ID   int64
		Name string
	}
	if err := request(g, "GET", "rulesets?per_page=100", nil, &existing); err != nil {
		return err
	}
	ids := make(map[string]int64)
	for _, rule := range existing {
		ids[rule.Name] = rule.ID
	}
	for _, branch := range branches {
		if err := ensureRule(g, ids, branchSpec(branch), apply, "브랜치 보호 규칙을 먼저 적용해야 합니다: "+branch); err != nil {
			return err
		}
		fmt.Println("채널 보호 확인:", branch)
	}
	if err := ensureRule(g, ids, tagSpec(), apply, "발행 태그의 변경과 삭제 방지 규칙이 필요합니다"); err != nil {
		return err
	}
	channels := []string{"preview", "rc", "stable"}
	if apply {
		if err := request(g, "PATCH", "", object{"default_branch": "preview"}, nil); err != nil {
			return err
		}
		id, err := lookup(p.Reviewer)
		if err != nil {
			return err
		}
		for i, channel := range channels {
			if err := applyApproval(g, channel, branches[i], id); err != nil {
				return err
			}
		}
	}
	for _, channel := range channels {
		if err := verifyApproval(g, p, channel); err != nil {
			return err
		}
	}
	var repository struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := request(g, "GET", "", nil, &repository); err != nil {
		return err
	}
	if repository.DefaultBranch != "preview" {
		return errors.New("저장소 기본 브랜치는 preview여야 합니다")
	}
	legacy, err = g.API("GET", "branches/main/protection", nil, nil, true)
	if err != nil {
		return err
	}
	if legacy {
		return errors.New("기존 main 보호와 새 승격 규칙의 호환성을 수동 검토하세요")
	}
	fmt.Println("기본 브랜치와 preview → rc → main 보호 확인 완료")
	return nil
}

func ensureRule(g api, ids map[string]int64, expected object, apply bool, missing string) error {
	id := ids[expected["name"].(string)]
	if apply {
		if id == 0 {
			var created struct{ ID int64 }
			if err := request(g, "POST", "rulesets", expected, &created); err != nil {
				return err
			}
			id = created.ID
		} else if err := request(g, "PUT", fmt.Sprintf("rulesets/%d", id), expected, nil); err != nil {
			return err
		}
	}
	if id == 0 {
		return errors.New(missing)
	}
	var actual object
	if err := request(g, "GET", fmt.Sprintf("rulesets/%d", id), nil, &actual); err != nil {
		return err
	}
	return verifyRule(actual, expected)
}

func applyApproval(g api, channel, branch string, id int64) error {
	name := "release-" + channel
	payload := object{"can_admins_bypass": false, "prevent_self_review": false, "reviewers": []object{{"type": "User", "id": id}},
		"deployment_branch_policy": object{"protected_branches": false, "custom_branch_policies": true}}
	if err := request(g, "PUT", "environments/"+name, payload, nil); err != nil {
		return err
	}
	rules, err := branchPolicies(g, name)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if !rule.matches(branch) {
			return errors.New("기존 승인 환경의 다른 브랜치 규칙을 먼저 검토하세요")
		}
	}
	if len(rules) == 0 {
		return request(g, "POST", "environments/"+name+"/deployment-branch-policies", object{"name": branch, "type": "branch"}, nil)
	}
	return nil
}

func lookup(login string) (int64, error) {
	data, err := exec.Command("gh", "api", "users/"+login).Output()
	if err != nil {
		return 0, err
	}
	var user struct{ ID int64 }
	if err := json.Unmarshal(data, &user); err != nil {
		return 0, err
	}
	if user.ID == 0 {
		return 0, errors.New("지정 리뷰어 ID를 찾을 수 없습니다")
	}
	return user.ID, nil
}
func run() error {
	apply := flag.Bool("apply", false, "apply branch and tag protection")
	source := flag.String("bootstrap-sha", "", "current main commit SHA")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *source != "" && !*apply {
		return errors.New("--bootstrap-sha는 --apply와 함께 사용합니다")
	}
	repository := os.Getenv("GITHUB_REPOSITORY")
	if repository == "" {
		repository = "NARUBROWN/spine-cli"
	}
	github, err := releasegithub.New(repository, "")
	if err != nil {
		return err
	}
	if *source != "" {
		if err := bootstrap(github, *source); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(".github/release-policy.json")
	if err != nil {
		return err
	}
	var p policy
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	return configure(github, *apply, p, lookup)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
