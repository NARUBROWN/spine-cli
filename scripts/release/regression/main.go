package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

var testName = regexp.MustCompile("^Test[A-Za-z0-9_]+$")

func suiteFor(name string) string {
	if _, ok := optional[name]; ok {
		return "extended"
	}
	if _, ok := generated[name]; ok {
		return "generated"
	}
	return "core"
}

func validSuite(suite string) bool {
	return suite == "core" || suite == "generated" || suite == "extended"
}

func selectTests(output, suite string) (string, error) {
	if !validSuite(suite) {
		return "", fmt.Errorf("알 수 없는 검사 범위: %s", suite)
	}
	var selected, missing []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if !testName.MatchString(line) {
			continue
		}
		seen[line] = true
		if suiteFor(line) == suite {
			selected = append(selected, line)
		}
	}
	for _, group := range []map[string]struct{}{optional, generated} {
		for name := range group {
			if !seen[name] {
				missing = append(missing, name)
			}
		}
	}
	for name := range generated {
		if _, overlap := optional[name]; overlap {
			return "", fmt.Errorf("검사 범위 중복: %s", name)
		}
	}
	for name := range required {
		top, _, _ := strings.Cut(name, "/")
		if !seen[top] {
			missing = append(missing, top)
		}
	}
	if len(missing) != 0 {
		slices.Sort(missing)
		return "", fmt.Errorf("검사 목록과 테스트 목록이 다릅니다: %s", strings.Join(slices.Compact(missing), ", "))
	}
	if len(selected) == 0 {
		return "", errors.New("검사 범위에 테스트가 없습니다")
	}
	slices.Sort(selected)
	return "^(" + strings.Join(selected, "|") + ")$", nil
}

func checkScope(r io.Reader, suite string) error {
	if suite != "all" && !validSuite(suite) {
		return fmt.Errorf("알 수 없는 증거 범위: %s", suite)
	}
	pending := make(map[string]bool)
	for name := range required {
		top, _, _ := strings.Cut(name, "/")
		if suite == "all" || suiteFor(top) == suite {
			pending[name] = true
		}
	}
	reader := bufio.NewReader(r)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var event struct{ Action, Test string }
			if err := json.Unmarshal(line, &event); err != nil {
				return fmt.Errorf("테스트 이벤트 해석: %w", err)
			}
			switch event.Action {
			case "pass":
				if _, tracked := pending[event.Test]; tracked {
					pending[event.Test] = false
				}
			case "fail":
				return fmt.Errorf("회귀 검사 실패: %s", event.Test)
			case "skip":
				for name := event.Test; name != ""; {
					if _, tracked := pending[name]; tracked {
						return fmt.Errorf("필수 회귀 검사 skip: %s", event.Test)
					}
					i := strings.LastIndexByte(name, '/')
					if i < 0 {
						break
					}
					name = name[:i]
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	var missing []string
	for name, absent := range pending {
		if absent {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("필수 회귀 검사 누락: %s", strings.Join(missing, ", "))
	}
	return nil
}

func suiteArgs(suite, pattern string) []string {
	packages := []string{"./internal/cli"}
	if suite == "core" {
		packages = []string{".", "./cmd/...", "./internal/..."}
	}
	args := []string{"test", "-race", "-json"}
	args = append(args, packages...)
	flag := "-run"
	if suite == "core" {
		// CLI의 제외 목록이 다른 패키지의 새 테스트를 제한하지 않도록 한다.
		var excluded []string
		for _, group := range []map[string]struct{}{optional, generated} {
			for name := range group {
				excluded = append(excluded, name)
			}
		}
		slices.Sort(excluded)
		flag, pattern = "-skip", "^("+strings.Join(excluded, "|")+")$"
	}
	return append(args, flag, pattern, "-count=1", "-timeout=30m")
}

func runSuite(suite string) (err error) {
	list := exec.Command("go", "test", "./internal/cli", "-list", "^Test")
	list.Stderr = os.Stderr
	output, err := list.Output()
	if err != nil {
		return err
	}
	pattern, err := selectTests(string(output), suite)
	if err != nil {
		return err
	}
	path := suite + "-regression.jsonl"
	if suite == "extended" {
		path = "regression.jsonl"
	}
	log, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	cmd := exec.Command("go", suiteArgs(suite, pattern)...)
	cmd.Stdout, cmd.Stderr = log, os.Stderr
	return cmd.Run()
}

func run(args []string) error {
	if len(args) == 1 && validSuite(args[0]) {
		return runSuite(args[0])
	}
	if len(args) >= 2 && len(args) <= 3 && args[0] == "check" {
		suite := "all"
		if len(args) == 3 {
			suite = strings.TrimPrefix(args[2], "--")
			if !validSuite(suite) || args[2] != "--"+suite {
				return errors.New("알 수 없는 증거 검사 옵션")
			}
		}
		log, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer log.Close()
		return checkScope(log, suite)
	}
	return errors.New("usage: regression core | generated | extended | regression check FILE [--core|--generated|--extended]")
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
