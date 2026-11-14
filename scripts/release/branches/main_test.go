package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type mockAPI func(string, string, any, any, bool) (bool, error)

func (f mockAPI) API(method, path string, input, output any, missing bool) (bool, error) {
	return f(method, path, input, output, missing)
}
func response(out, value any) (bool, error) {
	if out == nil {
		return true, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(data, out)
}

func TestBootstrapPrechecksEveryBranchBeforeWriting(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, conflict := range []bool{false, true} {
		var writes []object
		repo := mockAPI(func(method, path string, input, out any, missing bool) (bool, error) {
			switch {
			case method == "POST" && path == "git/refs":
				writes = append(writes, input.(object))
				return true, nil
			case path == "git/ref/heads/main":
				return response(out, object{"object": object{"sha": sha}})
			case path == "git/ref/heads/rc" && conflict:
				return response(out, object{"object": object{"sha": strings.Repeat("b", 40)}})
			case strings.HasPrefix(path, "git/ref/heads/"):
				if !missing {
					t.Fatal("missing channel should be allowed")
				}
				return false, nil
			case strings.HasPrefix(path, "contents/"):
				return true, nil
			}
			t.Fatalf("unexpected request: %s %s", method, path)
			return false, nil
		})
		err := bootstrap(repo, sha)
		if conflict {
			if err == nil || len(writes) != 0 {
				t.Fatalf("%v, %v", err, writes)
			}
		} else {
			want := []object{{"ref": "refs/heads/preview", "sha": sha}, {"ref": "refs/heads/rc", "sha": sha}}
			if err != nil || !reflect.DeepEqual(writes, want) {
				t.Fatalf("%v, %v", err, writes)
			}
		}
	}
}

func TestTagRulesAndBypass(t *testing.T) {
	tags := tagSpec()["rules"].([]object)
	if len(tags) != 2 || tags[0]["type"] != "deletion" || tags[1]["type"] != "update" {
		t.Fatal(tags)
	}
	expected := branchSpec("main")
	var actual object
	if _, err := response(&actual, expected); err != nil {
		t.Fatal(err)
	}
	if err := verifyRule(actual, expected); err != nil {
		t.Fatal(err)
	}
	actual["bypass_actors"] = []any{object{"actor_type": "RepositoryRole", "actor_id": 5}}
	if err := verifyRule(actual, expected); err == nil {
		t.Fatal("bypass accepted")
	}
}

func TestVerifyApprovalRejectsWrongReviewersBypassAndBranches(t *testing.T) {
	p := policy{Reviewer: "owner", Branches: map[string]string{"stable": "main"}}
	for _, variant := range []string{"valid", "bypass", "reviewer", "custom", "branch", "duplicate", "no reviewers", "missing type", "null type", "tag type"} {
		t.Run(variant, func(t *testing.T) {
			repo := mockAPI(func(method, path string, input, out any, missing bool) (bool, error) {
				if method != "GET" {
					t.Fatal(method)
				}
				if path == "environments/release-stable" {
					login := "owner"
					if variant == "reviewer" {
						login = "other"
					}
					reviewers := []object{{"type": "User", "reviewer": object{"login": login}}}
					if variant == "no reviewers" {
						reviewers = nil
					}
					return response(out, object{
						"can_admins_bypass":        variant == "bypass",
						"deployment_branch_policy": object{"custom_branch_policies": variant != "custom"},
						"protection_rules":         []object{{"type": "required_reviewers", "reviewers": reviewers}},
					})
				}
				if path != "environments/release-stable/deployment-branch-policies" {
					t.Fatal(path)
				}
				rules := []object{{"name": "main", "type": "branch"}}
				if variant == "branch" {
					rules[0]["name"] = "rc"
				}
				if variant == "duplicate" {
					rules = append(rules, rules[0])
				}
				switch variant {
				case "missing type":
					delete(rules[0], "type")
				case "null type":
					rules[0]["type"] = nil
				case "tag type":
					rules[0]["type"] = "tag"
				}
				return response(out, object{"branch_policies": rules})
			})
			if err := verifyApproval(repo, p, "stable"); (err == nil) != (variant == "valid" || variant == "missing type") {
				t.Fatalf("%v", err)
			}
		})
	}
}
