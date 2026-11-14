package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/approval"
)

func checkPolicyFiles(root string, p doc) error {
	data, err := os.ReadFile(filepath.Join(root, "internal", "cli", "version.go"))
	if err != nil {
		return err
	}
	expected := strings.TrimPrefix(field(p, "next_version"), "v") + "-dev"
	if !strings.Contains(string(data), fmt.Sprintf("developmentVersion = %q", expected)) {
		return errors.New("개발 버전과 next_version이 다릅니다")
	}
	notes, err := os.Stat(filepath.Join(root, field(p, "notes_path")))
	if err != nil || !notes.Mode().IsRegular() {
		return errors.New("대상 버전의 릴리스 초안이 필요합니다")
	}
	fmt.Println("릴리스 정책 형식 확인 완료")
	return nil
}

func checkApprovalEnvironments(repo evidenceRepo, p doc) error {
	for _, channel := range []string{"preview", "rc", "stable"} {
		if err := approval.Verify(repo, field(p, "reviewer_login"), channel, field(object(p, "branches"), channel)); err != nil {
			return err
		}
	}
	fmt.Println("정식 릴리스 승인 환경 확인 완료")
	return nil
}

func verifiedOutput(raw []byte, reuse bool, sha string) (string, error) {
	var jobs map[string]struct{ Result string }
	if err := json.Unmarshal(raw, &jobs); err != nil {
		return "", err
	}
	if jobs == nil {
		return "", errors.New("CI 결과가 없습니다")
	}
	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		expected := "success"
		if reuse {
			switch name {
			case "policy", "portable", "core-regression", "public-platforms":
				expected = "skipped"
			}
		}
		if actual := jobs[name].Result; actual != expected {
			return "", fmt.Errorf("검사 %s의 결과가 예상과 다릅니다: %s", name, actual)
		}
	}
	return "sha=" + sha + "\n", nil
}

func verifiedSource() error {
	output, err := verifiedOutput([]byte(os.Getenv("RESULTS")), os.Getenv("REUSE_COMMON") == "true", os.Getenv("SOURCE_SHA"))
	if err != nil {
		return err
	}
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return errors.New("GITHUB_OUTPUT is missing")
	}
	return appendOutput(path, output)
}
