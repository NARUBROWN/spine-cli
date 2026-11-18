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

func selectTests(output string, extended bool) (string, error) {
	var selected, missing []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if !testName.MatchString(line) {
			continue
		}
		seen[line] = true
		if _, excluded := optional[line]; excluded == extended {
			selected = append(selected, line)
		}
	}
	for name := range optional {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		slices.Sort(missing)
		return "", fmt.Errorf("선택형 검사 목록과 테스트 목록이 다릅니다: %s", strings.Join(missing, ", "))
	}
	return "^(" + strings.Join(selected, "|") + ")$", nil
}

func check(r io.Reader, core bool) error {
	return checkScope(r, core, false)
}

func checkScope(r io.Reader, core, extended bool) error {
	pending := make(map[string]bool)
	for name := range required {
		top, _, _ := strings.Cut(name, "/")
		if _, excluded := optional[top]; (!core || !excluded) && (!extended || excluded) {
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

func runSuite(extended bool) (err error) {
	list := exec.Command("go", "test", "./internal/cli", "-list", "^Test")
	list.Stderr = os.Stderr
	output, err := list.Output()
	if err != nil {
		return err
	}
	pattern, err := selectTests(string(output), true)
	if err != nil {
		return err
	}
	path := "core-regression.jsonl"
	if extended {
		path = "regression.jsonl"
	}
	log, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	args := []string{"test", "-race", "-json", "./internal/cli", "-run", pattern, "-count=1", "-timeout=30m"}
	if !extended {
		// Use -skip so tests in other packages are never limited to CLI test names.
		args = []string{"test", "-race", "-json", "./...", "-skip", pattern, "-count=1", "-timeout=30m"}
	}
	cmd := exec.Command("go", args...)
	cmd.Stdout, cmd.Stderr = log, os.Stderr
	return cmd.Run()
}

func run(args []string) error {
	if len(args) == 1 && (args[0] == "core" || args[0] == "extended") {
		return runSuite(args[0] == "extended")
	}
	if (len(args) == 2 || (len(args) == 3 && (args[2] == "--core" || args[2] == "--extended"))) && args[0] == "check" {
		log, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer log.Close()
		return checkScope(log, len(args) == 3 && args[2] == "--core", len(args) == 3 && args[2] == "--extended")
	}
	return errors.New("usage: regression core | regression extended | regression check FILE [--core|--extended]")
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
