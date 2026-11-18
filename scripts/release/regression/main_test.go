package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func inventory() string {
	names := map[string]bool{"TestCoreBehavior": true, "TestNewCoreBehavior": true}
	for _, group := range []map[string]struct{}{optional, generated, required} {
		for name := range group {
			top, _, _ := strings.Cut(name, "/")
			names[top] = true
		}
	}
	var result []string
	for name := range names {
		result = append(result, name)
	}
	slices.Sort(result)
	return strings.Join(result, "\n")
}

func TestSuitePartition(t *testing.T) {
	output := inventory()
	patterns := map[string]*regexp.Regexp{}
	for _, suite := range []string{"core", "generated", "extended"} {
		pattern, err := selectTests(output, suite)
		if err != nil {
			t.Fatal(err)
		}
		patterns[suite] = regexp.MustCompile(pattern)
	}
	for _, name := range strings.Split(output, "\n") {
		count := 0
		for suite, pattern := range patterns {
			if pattern.MatchString(name) {
				count++
				if suite != suiteFor(name) {
					t.Fatalf("잘못된 범위: %s %s", name, suite)
				}
			}
		}
		if count != 1 {
			t.Fatalf("검사 누락 또는 중복: %s %d", name, count)
		}
	}
	if !patterns["core"].MatchString("TestNewCoreBehavior") {
		t.Fatal("새 테스트가 기본 범위에서 누락되었습니다")
	}
	for _, suite := range []string{"core", "generated", "extended"} {
		if _, err := selectTests(strings.Replace(output, "TestGeneratedAssemblySmoke\n", "", 1), suite); err == nil {
			t.Fatal("없어진 필수 검사를 허용했습니다")
		}
	}
	generated["TestGeneratedAssemblyPublicMatrix"] = struct{}{}
	defer delete(generated, "TestGeneratedAssemblyPublicMatrix")
	if _, err := selectTests(output, "core"); err == nil {
		t.Fatal("중복된 검사 분류를 허용했습니다")
	}
}

func TestRepositoryInventory(t *testing.T) {
	cmd := exec.Command("go", "test", "./internal/cli", "-list", "^Test")
	cmd.Dir = "../../.."
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("실제 테스트 목록 조회: %v %s", err, output)
	}
	for _, suite := range []string{"core", "generated", "extended"} {
		if _, err := selectTests(string(output), suite); err != nil {
			t.Fatal(err)
		}
	}
}

func evidence(suite string) string {
	var out strings.Builder
	for name := range required {
		top, _, _ := strings.Cut(name, "/")
		if suite == "all" || suiteFor(top) == suite {
			_ = json.NewEncoder(&out).Encode(map[string]string{"Action": "pass", "Test": name})
		}
	}
	return out.String()
}

func TestEvidenceScopes(t *testing.T) {
	for _, suite := range []string{"core", "generated", "extended", "all"} {
		log := evidence(suite)
		if err := checkScope(strings.NewReader(log), suite); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{
			log[strings.IndexByte(log, '\n')+1:],
			log + `{"Action":"fail","Package":"broken"}`,
			log + `{"Action":"skip","Test":"` + firstRequired(suite) + `/child"}`,
			"",
		} {
			if err := checkScope(strings.NewReader(bad), suite); err == nil {
				t.Fatalf("잘못된 %s 증거를 허용했습니다", suite)
			}
		}
		if suite != "all" {
			if err := checkScope(strings.NewReader(log), "all"); err == nil {
				t.Fatal("부분 증거를 전체 검사로 인정했습니다")
			}
		}
	}
	for _, suite := range []string{"generated", "extended"} {
		if err := checkScope(strings.NewReader(evidence("core")), suite); err == nil {
			t.Fatal("다른 범위의 증거를 허용했습니다")
		}
	}
}

func firstRequired(suite string) string {
	for name := range required {
		top, _, _ := strings.Cut(name, "/")
		if suite == "all" || suiteFor(top) == suite {
			return name
		}
	}
	panic("필수 검사가 없습니다")
}

func TestCoreDoesNotRepeatReleasePackages(t *testing.T) {
	args := suiteArgs("core", "unused")
	if slices.Contains(args, "./...") || slices.Contains(args, "-run") || !slices.Contains(args, "-skip") {
		t.Fatalf("core가 다른 패키지의 테스트를 제한하거나 정책 테스트를 중복합니다: %v", args)
	}
	for _, path := range []string{".", "./cmd/...", "./internal/..."} {
		if !slices.Contains(args, path) {
			t.Fatalf("검사 패키지 누락: %s", path)
		}
	}
}

func TestRequiredWorkflowDoesNotWaitForExtended(t *testing.T) {
	data, err := os.ReadFile("../../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"public-platforms", "optional-composition", "  schedule:", "regression extended"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("필수 CI에 확장 실행이 남았습니다: %s", forbidden)
		}
	}
	data, err = os.ReadFile("../../../.github/workflows/ci-extended.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"  pull_request:", "  push:", "  workflow_call:", "verified-source"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("확장 CI가 필수 경로에 연결되었습니다: %s", forbidden)
		}
	}
	for _, required := range []string{"  schedule:", "  workflow_dispatch:", "regression extended", "check regression.jsonl --extended", "SPINE_TEST_KAFKA_BROKER", "SPINE_TEST_RABBITMQ_URL", "SPINE_TEST_NATS_URL"} {
		if !strings.Contains(string(data), required) {
			t.Fatalf("확장 실행 증거 누락: %s", required)
		}
	}
}
