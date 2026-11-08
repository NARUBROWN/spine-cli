package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type newPromptPresence struct {
	http, events, websocket, validator, frontend, frontendRoot bool
}

type promptChoice struct {
	value string
	label string
}

func shouldRunNewWizard(input io.Reader, options newProjectOptions) bool {
	if options.Interactive {
		return true
	}
	if options.NoInteractive || options.JSON || options.Write.DryRun || options.Write.IfMatch != "" {
		return false
	}
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func newPromptSelections(args []string) newPromptPresence {
	var present newPromptPresence
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--platform":
			present.http = true
			index++
		case "--event-platform":
			present.events = true
			index++
		case "--websocket-platform":
			present.websocket = true
			index++
		case "--validator":
			present.validator = true
			index++
		case "--frontend":
			present.frontend = true
			index++
		case "--frontend-root":
			present.frontendRoot = true
			index++
		}
	}
	return present
}

func promptNewProjectOptions(args []string, options newProjectOptions, input io.Reader, output io.Writer) (newProjectOptions, error) {
	reader := bufio.NewReader(input)
	present := newPromptSelections(args)
	var err error
	if !present.http {
		options.Platform, err = promptDynamicChoice(reader, output, "HTTP adapter", []promptChoice{
			{value: "nethttp", label: "net/http"},
			{value: "echo", label: "Echo"},
			{value: "none", label: "HTTP 없음, 이벤트 worker"},
		}, options.Platform, true, "spine-<provider>-platform@preview에서 HTTP provider를 찾습니다")
		if err != nil {
			return options, err
		}
	}
	if !present.events {
		options.Events, err = promptEventPlatforms(reader, output, options.Events, options.Platform == "none")
		if err != nil {
			return options, err
		}
	}
	if !present.websocket {
		choices := []promptChoice{{value: "none", label: "사용 안 함"}}
		if options.Platform != "none" {
			choices = append(choices, promptChoice{value: "gorilla", label: "Gorilla WebSocket"})
		}
		options.WebSocketPlatform, err = promptDynamicChoice(reader, output, "WebSocket", choices, options.WebSocketPlatform, options.Platform != "none", "spine-<provider>-platform@preview에서 WebSocket provider를 찾습니다")
		if err != nil {
			return options, err
		}
	}
	if !present.validator {
		options.Validator, err = promptDynamicChoice(reader, output, "Validator", []promptChoice{
			{value: "none", label: "사용 안 함"},
			{value: "playground", label: "Playground"},
		}, options.Validator, true, "spine-validator-<provider>@preview에서 validator를 찾습니다")
		if err != nil {
			return options, err
		}
	}
	if !present.frontend && !present.frontendRoot {
		if !supportsFrontendServingPlatform(options.Platform) {
			if options.Platform == "none" {
				fmt.Fprintln(output, "HTTP adapter가 없어 프론트엔드를 연결할 수 없습니다.")
			} else {
				fmt.Fprintln(output, "현재 프론트엔드 서빙은 Echo와 net/http HTTP adapter만 지원합니다.")
			}
			options.Frontend = "none"
		} else if promptYesNo(reader, output, "Frontend를 사용할까요?", false) {
			options.Frontend, err = promptDynamicChoice(reader, output, "Frontend provider", []promptChoice{
				{value: "react", label: "React CSR"},
			}, "react", true, "spine-frontend-<provider>@preview에서 esbuild provider를 찾습니다")
		} else {
			options.Frontend = "none"
		}
		if err != nil {
			return options, err
		}
		if options.Frontend != "none" {
			options.FrontendRoot, err = promptFrontendRoot(reader, output, options.FrontendRoot)
			if err != nil {
				return options, err
			}
		}
	} else if present.frontendRoot && !present.frontend {
		options.Frontend = "react"
	}
	return options, nil
}

func promptEventPlatforms(reader *bufio.Reader, output io.Writer, existing []eventPlatformSpec, required bool) ([]eventPlatformSpec, error) {
	selected := append([]eventPlatformSpec(nil), existing...)
	for {
		choices := []promptChoice{}
		defaultValue := ""
		if !required && len(selected) == 0 {
			choices = append(choices, promptChoice{value: "none", label: "사용 안 함"})
			defaultValue = "none"
		} else if !required {
			choices = append(choices, promptChoice{value: "done", label: "선택 완료"})
			defaultValue = "done"
		}
		for _, choice := range []promptChoice{
			{value: "kafka", label: "Kafka"},
			{value: "rabbitmq", label: "RabbitMQ"},
			{value: "nats", label: "NATS JetStream"},
		} {
			alreadySelected := false
			for _, event := range selected {
				alreadySelected = alreadySelected || event.Provider == choice.value
			}
			if !alreadySelected {
				choices = append(choices, choice)
			}
		}
		value, err := promptDynamicChoice(reader, output, "Event adapter", choices, defaultValue, true, "spine-<provider>-platform@preview에서 event provider를 찾습니다. name:provider 형식으로 등록 이름도 지정할 수 있습니다")
		if err != nil {
			return selected, err
		}
		if value == "none" || value == "done" {
			if required && len(selected) == 0 {
				fmt.Fprintln(output, "HTTP가 없으면 event provider를 하나 이상 선택해야 합니다.")
				required = true
				continue
			}
			return selected, nil
		}
		spec, err := parseEventPlatform(value)
		if err != nil {
			fmt.Fprintf(output, "유효하지 않은 event provider입니다: %v\n", err)
			continue
		}
		duplicate := false
		for _, event := range selected {
			duplicate = duplicate || event.Name == spec.Name || event.Provider == spec.Provider
		}
		if duplicate {
			fmt.Fprintln(output, "같은 event 이름이나 provider는 중복 선택할 수 없습니다.")
			continue
		}
		selected = append(selected, spec)
		if !promptYesNo(reader, output, "event provider를 더 추가할까요?", false) {
			return selected, nil
		}
		required = false
	}
}

func promptFrontendRoot(reader *bufio.Reader, output io.Writer, defaultRoot string) (string, error) {
	for {
		value, err := promptText(reader, output, "Frontend root 경로", defaultRoot)
		if err != nil {
			return "", err
		}
		if err := validateProjectRelativePath(value); err != nil || filepath.Clean(filepath.FromSlash(value)) == "." || projectPathContains(value, "bin/frontend") || projectPathContains("bin/frontend", value) {
			fmt.Fprintln(output, "프로젝트 안의 frontend_output과 겹치지 않는 상대 경로를 입력하세요.")
			continue
		}
		return filepath.ToSlash(filepath.Clean(filepath.FromSlash(value))), nil
	}
}

func promptDynamicChoice(reader *bufio.Reader, output io.Writer, title string, choices []promptChoice, defaultValue string, allowCustom bool, customHint string) (string, error) {
	for {
		fmt.Fprintf(output, "\n%s를 선택하세요.\n", title)
		for index, choice := range choices {
			fmt.Fprintf(output, "  %d. %s\n", index+1, choice.label)
		}
		if allowCustom {
			fmt.Fprintln(output, "  0. 목록에 없음, provider 이름 직접 입력")
			if customHint != "" {
				fmt.Fprintf(output, "     %s\n", customHint)
			}
		}
		if defaultValue != "" {
			fmt.Fprintf(output, "선택 또는 이름 검색 [%s]: ", defaultValue)
		} else {
			fmt.Fprint(output, "선택 또는 이름 검색: ")
		}
		line, readErr := reader.ReadString('\n')
		value := strings.TrimSpace(line)
		if value == "" && defaultValue != "" {
			return defaultValue, nil
		}
		if value == "0" || strings.EqualFold(value, "other") || value == "목록에 없음" || value == "목록에없음" {
			if !allowCustom {
				fmt.Fprintln(output, "목록에서 선택하세요.")
			} else {
				custom, err := promptText(reader, output, "provider 이름", "")
				return strings.ToLower(strings.TrimSpace(custom)), err
			}
		} else if value != "" {
			if index, err := strconv.Atoi(value); err == nil {
				if index > 0 && index <= len(choices) {
					return choices[index-1].value, nil
				}
				fmt.Fprintln(output, "목록에 있는 번호를 선택하세요.")
			} else {
				lower := strings.ToLower(value)
				var matches []promptChoice
				for _, choice := range choices {
					if strings.EqualFold(choice.value, value) || strings.EqualFold(choice.label, value) {
						return choice.value, nil
					}
					if strings.Contains(strings.ToLower(choice.value), lower) || strings.Contains(strings.ToLower(choice.label), lower) {
						matches = append(matches, choice)
					}
				}
				if len(matches) == 1 {
					return matches[0].value, nil
				}
				if len(matches) > 1 {
					fmt.Fprintln(output, "일치하는 선택지:")
					for _, choice := range matches {
						fmt.Fprintf(output, "  %s\n", choice.label)
					}
				} else if allowCustom {
					return strings.ToLower(value), nil
				} else {
					fmt.Fprintln(output, "목록에 있는 선택지를 입력하세요.")
				}
			}
		}
		if readErr != nil {
			return "", fmt.Errorf("선택 입력을 읽을 수 없습니다: %w", readErr)
		}
	}
}

func promptText(reader *bufio.Reader, output io.Writer, title, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Fprintf(output, "%s [%s]: ", title, defaultValue)
	} else {
		fmt.Fprintf(output, "%s: ", title)
	}
	line, err := reader.ReadString('\n')
	value := strings.TrimSpace(line)
	if value == "" {
		if defaultValue != "" {
			return defaultValue, nil
		}
		if err != nil {
			return "", fmt.Errorf("%s를 입력해야 합니다", title)
		}
		return "", fmt.Errorf("%s를 입력해야 합니다", title)
	}
	return value, nil
}

func promptYesNo(reader *bufio.Reader, output io.Writer, title string, defaultValue bool) bool {
	for {
		defaultLabel := "[y/N]"
		if defaultValue {
			defaultLabel = "[Y/n]"
		}
		fmt.Fprintf(output, "%s %s: ", title, defaultLabel)
		line, err := reader.ReadString('\n')
		value := strings.ToLower(strings.TrimSpace(line))
		if value == "" {
			return defaultValue
		}
		switch value {
		case "y", "yes", "1":
			return true
		case "n", "no", "0":
			return false
		default:
			fmt.Fprintln(output, "y 또는 n을 입력하세요.")
		}
		if err != nil {
			return defaultValue
		}
	}
}
