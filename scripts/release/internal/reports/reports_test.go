package reports

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectAndValidateReportMatrix(t *testing.T) {
	for _, tc := range []struct{ scope, mutation, message string }{
		{"", "", ""},
		{"", "duplicate", "OS가 누락되거나 중복"},
		{"", "missing", "OS가 누락되거나 중복"},
		{"", "entrypoints", "결과가 발행 계획과 다릅니다"},
		{"", "upstream_evidence", "코어와 플랫폼의 조합"},
		{"", "combination_checks", "코어와 플랫폼의 조합"},
		{"", "wrong cohort", "전체 RC 조합"},
		{"", "partial checks", "코어와 플랫폼의 조합"},
		{"", "wrong upstream", "코어와 플랫폼의 조합"},
		{"", "rc_cohort", "전체 RC 조합"},
		{"", "rc_combination_checks", "전체 RC 조합"},
		{"tag-install", "", ""},
		{"tag-install", "wrong query", "승격 근거와 정확한 새 태그"},
		{"tag-install", "missing proof", "승격 근거와 정확한 새 태그"},
		{"tag-install", "scope", "결과가 발행 계획과 다릅니다"},
		{"tag-install", "resolved_cli_version", "발행한 태그를 확인하지 못했습니다"},
	} {
		t.Run(tc.scope+"/"+tc.mutation, func(t *testing.T) {
			root := t.TempDir()
			const version = "v0.4.5-rc.1"
			query := version
			plan := Document{"version": version, "required_public_os": []string{"linux", "darwin", "windows"}, "source_sha": strings.Repeat("a", 40),
				"components": Document{"recipe": "pinned"}, "upstream_evidence": Document{"platform": Document{"version": "v1.0.0"}},
				"rc_cohort": Document{"github.com/NARUBROWN/spine": "v0.7.5-rc.1"}}
			if tc.scope != "" {
				plan["public_validation_scope"] = tc.scope
				plan["source_evidence"] = Document{"artifact_id": json.Number("1")}
			}
			if tc.mutation == "missing proof" {
				delete(plan, "source_evidence")
			}
			if tc.mutation == "wrong query" {
				query = plan["source_sha"].(string)
			}
			for _, platform := range []string{"linux", "darwin", "windows"} {
				if tc.mutation == "missing" && platform == "linux" {
					continue
				}
				report := Document{"os": platform, "result": "passed", "query": query, "source_sha": plan["source_sha"],
					"components": plan["components"], "entrypoints": []string{"spine", "spine-cli"}}
				if tc.scope == "tag-install" {
					report["scope"] = tc.scope
					report["resolved_cli_version"] = version
				} else {
					report["upstream_evidence"] = plan["upstream_evidence"]
					report["rc_cohort"] = plan["rc_cohort"]
					report["combination_checks"] = []string{"echo-full", "nethttp-full"}
					report["rc_combination_checks"] = []string{"echo-full", "nethttp-full"}
				}
				if platform == "linux" {
					switch tc.mutation {
					case "duplicate":
						report["os"] = "darwin"
					case "wrong cohort":
						report["rc_cohort"] = Document{}
					case "partial checks":
						report["combination_checks"] = []string{"echo-full"}
					case "wrong upstream":
						report["upstream_evidence"] = Document{}
					default:
						delete(report, tc.mutation)
					}
				}
				dir := filepath.Join(root, "public-after-tag-"+platform)
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "release-validation.json"), data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Collect(root, plan, query)
			if tc.message == "" {
				if err != nil || len(got) != 3 {
					t.Fatalf("%v %v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.message) || got != nil {
				t.Fatalf("expected %q, got %v %v", tc.message, got, err)
			}
		})
	}
}
