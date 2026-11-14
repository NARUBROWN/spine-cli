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

func selection(output string) (string, error) {
	var selected, missing []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if !testName.MatchString(line) {
			continue
		}
		seen[line] = true
		if _, excluded := optional[line]; !excluded {
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
	pending := make(map[string]bool)
	for name := range required {
		top, _, _ := strings.Cut(name, "/")
		if _, excluded := optional[top]; !core || !excluded {
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

func runCore() (err error) {
	list := exec.Command("go", "test", "./internal/cli", "-list", "^Test")
	list.Stderr = os.Stderr
	output, err := list.Output()
	if err != nil {
		return err
	}
	pattern, err := selection(string(output))
	if err != nil {
		return err
	}
	log, err := os.Create("core-regression.jsonl")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	cmd := exec.Command("go", "test", "-race", "-json", "./...", "-run", pattern, "-count=1", "-timeout=30m")
	cmd.Stdout, cmd.Stderr = log, os.Stderr
	return cmd.Run()
}

func run(args []string) error {
	if len(args) == 1 && args[0] == "core" {
		return runCore()
	}
	if (len(args) == 2 || (len(args) == 3 && args[2] == "--core")) && args[0] == "check" {
		log, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer log.Close()
		return check(log, len(args) == 3)
	}
	return errors.New("usage: regression core | regression check FILE [--core]")
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
