package approval

import (
	"encoding/json"
	"errors"
)

func Verify(g interface {
	API(string, string, any, any, bool) (bool, error)
}, login, channel, branch string) error {
	name := "release-" + channel
	var env struct {
		Bypass       *bool `json:"can_admins_bypass"`
		BranchPolicy struct {
			Custom bool `json:"custom_branch_policies"`
		} `json:"deployment_branch_policy"`
		Rules []struct {
			Type      string
			Reviewers []struct {
				Type     string
				Reviewer struct{ Login string }
			}
		} `json:"protection_rules"`
	}
	if _, err := g.API("GET", "environments/"+name, nil, &env, false); err != nil {
		return err
	}
	reviewer := false
	for _, rule := range env.Rules {
		if rule.Type != "required_reviewers" {
			continue
		}
		for _, user := range rule.Reviewers {
			if user.Type == "User" && user.Reviewer.Login == login {
				reviewer = true
			}
		}
	}
	if !reviewer || env.Bypass == nil || *env.Bypass || !env.BranchPolicy.Custom {
		return errors.New("정식 승인 환경에 지정 리뷰어, 우회 금지, 브랜치 제한이 필요합니다")
	}
	var result struct {
		Policies []struct {
			Name string
			Type json.RawMessage
		} `json:"branch_policies"`
	}
	if _, err := g.API("GET", "environments/"+name+"/deployment-branch-policies", nil, &result, false); err != nil {
		return err
	}
	invalidBranch := errors.New("승인 환경은 해당 채널 브랜치에서만 실행할 수 있어야 합니다")
	if len(result.Policies) != 1 || result.Policies[0].Name != branch {
		return invalidBranch
	}
	if rawType := result.Policies[0].Type; len(rawType) != 0 {
		var kind string
		if err := json.Unmarshal(rawType, &kind); err != nil || kind != "branch" {
			return invalidBranch
		}
	}
	return nil
}
