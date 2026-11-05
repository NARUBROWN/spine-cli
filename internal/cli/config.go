package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// projectConfig는 CLI 공통 설정만 보관하며 앱별 설정은 애플리케이션에서 관리합니다.
type projectConfig struct {
	Module            string
	Entrypoint        string
	Composition       string
	Port              int
	Layout            string
	Platform          string
	Recipe            string
	Events            []eventPlatformSpec
	Validator         string
	WebSocketPlatform string
}

func defaultProjectConfig(module string) projectConfig {
	return projectConfig{Module: module, Entrypoint: ".", Composition: "./internal/application", Port: 8080, Layout: layeredLayout, Platform: "echo"}
}

func projectConfigTemplate(config projectConfig) string {
	result := fmt.Sprintf("module = %q\nentrypoint = %q\ncomposition = %q\nport = %d\n", config.Module, config.Entrypoint, config.Composition, config.Port)
	if config.Layout != "" {
		result += fmt.Sprintf("layout = %q\n", config.Layout)
	}
	if config.Platform != "" {
		result += fmt.Sprintf("platform = %q\n", config.Platform)
	}
	if config.Recipe != "" {
		result += fmt.Sprintf("recipe = %q\n", config.Recipe)
	}
	if config.WebSocketPlatform != "" {
		result += fmt.Sprintf("websocket_platform = %q\n", config.WebSocketPlatform)
	}
	if config.Validator != "" {
		result += fmt.Sprintf("validator = %q\n", config.Validator)
	}
	if len(config.Events) > 0 {
		specs := []string{}
		for _, spec := range config.Events {
			specs = append(specs, strconv.Quote(spec.Name+":"+spec.Provider))
		}
		result += "event_platforms = [" + strings.Join(specs, ", ") + "]\n"
	}
	return result
}

func loadProjectConfig() (projectConfig, error) {
	contents, err := os.ReadFile("spine.toml")
	if err != nil {
		return projectConfig{}, fmt.Errorf("spine.toml is required: %w", err)
	}
	config := projectConfig{Entrypoint: ".", Composition: ".", Port: 8080}
	for lineNumber, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return projectConfig{}, fmt.Errorf("spine.toml:%d: expected key = value", lineNumber+1)
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch key {
		case "module", "entrypoint", "composition", "layout", "platform", "recipe", "validator", "websocket_platform":
			unquoted, err := strconv.Unquote(value)
			if err != nil || unquoted == "" {
				return projectConfig{}, fmt.Errorf("spine.toml:%d: %s must be a non-empty quoted string", lineNumber+1, key)
			}
			if key == "module" {
				config.Module = unquoted
			} else if key == "entrypoint" {
				config.Entrypoint = unquoted
			} else if key == "composition" {
				config.Composition = unquoted
			} else if key == "platform" {
				if unquoted != "echo" && unquoted != "none" {
					return projectConfig{}, fmt.Errorf("spine.toml:%d: unsupported platform %q", lineNumber+1, unquoted)
				}
				config.Platform = unquoted
			} else if key == "recipe" {
				if unquoted != assemblyRecipe && !isPreviewAssemblyRecipe(unquoted) && unquoted != stableAssemblyRecipe && unquoted != legacyWebSocketRecipe {
					return projectConfig{}, fmt.Errorf("spine.toml:%d: unsupported recipe %q", lineNumber+1, unquoted)
				}
				config.Recipe = unquoted
			} else if key == "websocket_platform" {
				if unquoted != "none" && unquoted != "gorilla" {
					return projectConfig{}, fmt.Errorf("spine.toml:%d: unsupported WebSocket platform %q", lineNumber+1, unquoted)
				}
				config.WebSocketPlatform = unquoted
			} else if key == "validator" {
				config.Validator = unquoted
			} else {
				if unquoted != layeredLayout && unquoted != "legacy" {
					return projectConfig{}, fmt.Errorf("spine.toml:%d: unsupported layout %q", lineNumber+1, unquoted)
				}
				config.Layout = unquoted
			}
		case "event_platforms":
			if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
				return projectConfig{}, fmt.Errorf("spine.toml:%d: event_platforms must be an array of quoted name:provider strings", lineNumber+1)
			}
			body := strings.TrimSpace(value[1 : len(value)-1])
			if body != "" {
				for _, entry := range strings.Split(body, ",") {
					text, err := strconv.Unquote(strings.TrimSpace(entry))
					if err != nil {
						return projectConfig{}, fmt.Errorf("spine.toml:%d: invalid event platform", lineNumber+1)
					}
					spec, err := parseEventPlatform(text)
					if err != nil {
						return projectConfig{}, err
					}
					config.Events = append(config.Events, spec)
				}
			}
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				return projectConfig{}, fmt.Errorf("spine.toml:%d: port must be between 1 and 65535", lineNumber+1)
			}
			config.Port = port
		default:
			return projectConfig{}, fmt.Errorf("spine.toml:%d: unsupported setting %q", lineNumber+1, key)
		}
	}
	if config.Module == "" {
		return projectConfig{}, fmt.Errorf("spine.toml: module is required")
	}
	if config.WebSocketPlatform == "gorilla" && config.Platform != "echo" {
		return config, fmt.Errorf("WebSocket platform requires HTTP echo")
	}
	validator := config.Validator
	if validator == "" {
		validator = "none"
	}
	if err := validateAssemblySelection(config.Platform, config.Events, validator); err != nil {
		return projectConfig{}, err
	}
	return config, nil
}
