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
	channel, http, events, websocket, validator, frontend, frontendRoot bool
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
		case "--channel":
			present.channel = true
			index++
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
	if !present.channel {
		options.DependencyChannel, err = promptDynamicChoice(reader, output, "Dependency channel", []promptChoice{
			{value: "preview", label: "Preview"},
			{value: "rc", label: "RC"},
			{value: "main", label: "Main (release)"},
		}, options.DependencyChannel, false, "")
		if err != nil {
			return options, err
		}
	}
	if !present.http {
		options.Platform, err = promptDynamicChoice(reader, output, "HTTP adapter", []promptChoice{
			{value: "nethttp", label: "net/http"},
			{value: "echo", label: "Echo"},
			{value: "none", label: "No HTTP, event worker"},
		}, options.Platform, true, providerLookupHint("spine-<provider>-platform", options.DependencyChannel))
		if err != nil {
			return options, err
		}
	}
	if !present.events {
		options.Events, err = promptEventPlatforms(reader, output, options.Events, options.Platform == "none", options.DependencyChannel)
		if err != nil {
			return options, err
		}
	}
	if !present.websocket {
		choices := []promptChoice{{value: "none", label: "Disabled"}}
		if options.Platform != "none" {
			choices = append(choices, promptChoice{value: "gorilla", label: "Gorilla WebSocket"})
		}
		options.WebSocketPlatform, err = promptDynamicChoice(reader, output, "WebSocket", choices, options.WebSocketPlatform, options.Platform != "none", providerLookupHint("spine-<provider>-platform", options.DependencyChannel))
		if err != nil {
			return options, err
		}
	}
	if !present.validator {
		options.Validator, err = promptDynamicChoice(reader, output, "Validator", []promptChoice{
			{value: "none", label: "Disabled"},
			{value: "playground", label: "Playground"},
		}, options.Validator, true, providerLookupHint("spine-validator-<provider>", options.DependencyChannel))
		if err != nil {
			return options, err
		}
	}
	if !present.frontend && !present.frontendRoot {
		if !supportsFrontendServingPlatform(options.Platform) {
			if options.Platform == "none" {
				fmt.Fprintln(output, "Frontend integration is unavailable without an HTTP adapter.")
			} else {
				fmt.Fprintln(output, "Frontend serving currently supports only the Echo and net/http HTTP adapters.")
			}
			options.Frontend = "none"
		} else if promptYesNo(reader, output, "Enable a frontend?", false) {
			options.Frontend, err = promptDynamicChoice(reader, output, "Frontend provider", []promptChoice{
				{value: "react", label: "React CSR"},
			}, "react", true, providerLookupHint("spine-frontend-<provider>", options.DependencyChannel))
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

func promptEventPlatforms(reader *bufio.Reader, output io.Writer, existing []eventPlatformSpec, required bool, channel string) ([]eventPlatformSpec, error) {
	selected := append([]eventPlatformSpec(nil), existing...)
	for {
		choices := []promptChoice{}
		defaultValue := ""
		if !required && len(selected) == 0 {
			choices = append(choices, promptChoice{value: "none", label: "Disabled"})
			defaultValue = "none"
		} else if !required {
			choices = append(choices, promptChoice{value: "done", label: "Done"})
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
		hint := providerLookupHint("spine-<provider>-platform", channel) + " Use name:provider to set a registration name."
		value, err := promptDynamicChoice(reader, output, "Event adapter", choices, defaultValue, true, hint)
		if err != nil {
			return selected, err
		}
		if value == "none" || value == "done" {
			if required && len(selected) == 0 {
				fmt.Fprintln(output, "Select at least one event provider when HTTP is disabled.")
				required = true
				continue
			}
			return selected, nil
		}
		spec, err := parseEventPlatform(value)
		if err != nil {
			fmt.Fprintf(output, "Invalid event provider: %v\n", err)
			continue
		}
		duplicate := false
		for _, event := range selected {
			duplicate = duplicate || event.Name == spec.Name || event.Provider == spec.Provider
		}
		if duplicate {
			fmt.Fprintln(output, "Event names and providers must be unique.")
			continue
		}
		selected = append(selected, spec)
		if !promptYesNo(reader, output, "Add another event provider?", false) {
			return selected, nil
		}
		required = false
	}
}

func promptFrontendRoot(reader *bufio.Reader, output io.Writer, defaultRoot string) (string, error) {
	for {
		value, err := promptText(reader, output, "Frontend root path", defaultRoot)
		if err != nil {
			return "", err
		}
		if err := validateProjectRelativePath(value); err != nil || filepath.Clean(filepath.FromSlash(value)) == "." || projectPathContains(value, "bin/frontend") || projectPathContains("bin/frontend", value) {
			fmt.Fprintln(output, "Enter a project-relative path that does not overlap frontend_output.")
			continue
		}
		return filepath.ToSlash(filepath.Clean(filepath.FromSlash(value))), nil
	}
}

func promptDynamicChoice(reader *bufio.Reader, output io.Writer, title string, choices []promptChoice, defaultValue string, allowCustom bool, customHint string) (string, error) {
	for {
		fmt.Fprintf(output, "\nSelect %s.\n", title)
		for index, choice := range choices {
			fmt.Fprintf(output, "  %d. %s\n", index+1, choice.label)
		}
		if allowCustom {
			fmt.Fprintln(output, "  0. Other provider")
			if customHint != "" {
				fmt.Fprintf(output, "     %s\n", customHint)
			}
		}
		if defaultValue != "" {
			fmt.Fprintf(output, "Choice or name [%s]: ", defaultValue)
		} else {
			fmt.Fprint(output, "Choice or name: ")
		}
		line, readErr := reader.ReadString('\n')
		value := strings.TrimSpace(line)
		if value == "" && defaultValue != "" {
			return defaultValue, nil
		}
		if value == "0" || strings.EqualFold(value, "other") {
			if !allowCustom {
				fmt.Fprintln(output, "Select one of the listed choices.")
			} else {
				custom, err := promptText(reader, output, "Provider name", "")
				return strings.ToLower(strings.TrimSpace(custom)), err
			}
		} else if value != "" {
			if index, err := strconv.Atoi(value); err == nil {
				if index > 0 && index <= len(choices) {
					return choices[index-1].value, nil
				}
				fmt.Fprintln(output, "Select a listed number.")
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
					fmt.Fprintln(output, "Matching choices:")
					for _, choice := range matches {
						fmt.Fprintf(output, "  %s\n", choice.label)
					}
				} else if allowCustom {
					return strings.ToLower(value), nil
				} else {
					fmt.Fprintln(output, "Enter one of the listed choices.")
				}
			}
		}
		if readErr != nil {
			return "", fmt.Errorf("could not read the selection: %w", readErr)
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
			return "", fmt.Errorf("%s is required", title)
		}
		return "", fmt.Errorf("%s is required", title)
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
			fmt.Fprintln(output, "Enter y or n.")
		}
		if err != nil {
			return defaultValue
		}
	}
}

func providerLookupHint(module, channel string) string {
	switch channel {
	case "rc":
		return fmt.Sprintf("Looks up the latest published RC or release tag for %s.", module)
	case "main":
		return fmt.Sprintf("Looks up the latest published release tag for %s.", module)
	default:
		return fmt.Sprintf("Looks up %s@preview.", module)
	}
}
