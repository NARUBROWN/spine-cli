package cli

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

type frontendSettings struct {
	root      string
	entry     string
	output    string
	apiPrefix string
}

const frontendOutputMarker = ".spine-frontend-output"
const frontendOutputMarkerContents = "frontend output managed by Spine CLI\n"

type frontendBuilder struct {
	settings frontendSettings
	context  api.BuildContext
}

func frontendSettingsOf(builder *frontendBuilder) *frontendSettings {
	if builder == nil {
		return nil
	}
	settings := builder.settings
	return &settings
}

func sameFrontendSettings(left, right *frontendSettings) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.root == right.root && left.entry == right.entry && left.output == right.output && left.apiPrefix == right.apiPrefix
}

var frontendEntryCandidates = []string{
	"src/main.tsx", "src/main.jsx", "src/main.ts", "src/main.js",
}

func validateProjectRelativePath(value string) error {
	if filepath.IsAbs(filepath.FromSlash(value)) || strings.Contains(value, "\\") {
		return fmt.Errorf("must be a project-relative path")
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("must not escape the project directory")
	}
	return nil
}

func resolveFrontendSettings(projectRoot string, config projectConfig) (*frontendSettings, error) {
	root := filepath.Join(projectRoot, filepath.FromSlash(config.FrontendRoot))
	info, err := os.Stat(root)
	if os.IsNotExist(err) && !config.FrontendConfigured {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not inspect frontend directory %q: %w", config.FrontendRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("frontend_root %q is not a directory", config.FrontendRoot)
	}
	entry := config.FrontendEntry
	if entry == "" {
		for _, candidate := range frontendEntryCandidates {
			if candidateInfo, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); statErr == nil && !candidateInfo.IsDir() {
				entry = candidate
				break
			}
		}
	}
	if entry == "" {
		if config.FrontendConfigured {
			return nil, fmt.Errorf("frontend entry was not found under %q; set frontend_entry in spine.toml", config.FrontendRoot)
		}
		return nil, nil
	}
	entryPath := filepath.Join(root, filepath.FromSlash(entry))
	entryRelative, err := filepath.Rel(root, entryPath)
	if err != nil || entryRelative == ".." || strings.HasPrefix(entryRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(entryRelative) {
		return nil, fmt.Errorf("frontend_entry %q must remain inside frontend_root %q", entry, config.FrontendRoot)
	}
	entryInfo, err := os.Stat(entryPath)
	if err != nil {
		return nil, fmt.Errorf("could not find frontend entry %q: %w", filepath.ToSlash(filepath.Join(config.FrontendRoot, entry)), err)
	}
	if entryInfo.IsDir() {
		return nil, fmt.Errorf("frontend_entry %q is a directory", entry)
	}
	output := filepath.Join(projectRoot, filepath.FromSlash(config.FrontendOutput))
	return &frontendSettings{root: root, entry: entryPath, output: output, apiPrefix: config.FrontendAPIPrefix}, nil
}

func frontendSourceState(projectRoot string, config projectConfig) (string, bool, error) {
	settings, err := resolveFrontendSettings(projectRoot, config)
	if err != nil || settings == nil {
		return "", settings != nil, err
	}
	var paths []string
	err = filepath.WalkDir(settings.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == settings.root {
			return nil
		}
		name := entry.Name()
		topLevelGeneratedDirectory := filepath.Dir(path) == settings.root && (name == ".spine" || name == "dist" || name == "build" || name == "coverage")
		if entry.IsDir() && (name == "node_modules" || name == ".git" || topLevelGeneratedDirectory) {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && entry.Type().IsRegular() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", true, fmt.Errorf("could not scan frontend files: %w", err)
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, path := range paths {
		relative, err := filepath.Rel(settings.root, path)
		if err != nil {
			return "", true, err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return "", true, err
		}
		fmt.Fprintf(digest, "%d:%s\n%d:", len(relative), filepath.ToSlash(relative), len(contents))
		_, _ = digest.Write(contents)
		_, _ = digest.Write([]byte{'\n'})
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), true, nil
}

func newFrontendBuilder(projectRoot string, config projectConfig, production bool, errOut io.Writer) (*frontendBuilder, bool, error) {
	settings, err := resolveFrontendSettings(projectRoot, config)
	if err != nil || settings == nil {
		return nil, settings != nil, err
	}
	options := api.BuildOptions{
		AbsWorkingDir:     settings.root,
		EntryPoints:       []string{settings.entry},
		Outdir:            filepath.Join(settings.output, "assets"),
		EntryNames:        "app",
		ChunkNames:        "chunk-[hash]",
		AssetNames:        "[name]-[hash]",
		PublicPath:        "/assets",
		Bundle:            true,
		Platform:          api.PlatformBrowser,
		Format:            api.FormatESModule,
		Target:            api.ES2020,
		JSX:               api.JSXAutomatic,
		JSXImportSource:   "react",
		Metafile:          true,
		Write:             false,
		LogLevel:          api.LogLevelSilent,
		Loader:            map[string]api.Loader{".js": api.LoaderJSX},
		MinifyWhitespace:  production,
		MinifyIdentifiers: production,
		MinifySyntax:      production,
		LegalComments:     api.LegalCommentsLinked,
	}
	if !production {
		options.Sourcemap = api.SourceMapLinked
		options.JSXDev = true
	}
	ctx, contextErr := api.Context(options)
	if contextErr != nil {
		return nil, true, formatEsbuildMessages(contextErr.Errors)
	}
	return &frontendBuilder{settings: *settings, context: ctx}, true, nil
}

func (builder *frontendBuilder) close() {
	if builder != nil && builder.context != nil {
		builder.context.Dispose()
	}
}

func (builder *frontendBuilder) rebuild(errOut io.Writer) error {
	result := builder.context.Rebuild()
	if len(result.Errors) > 0 {
		return formatEsbuildMessages(result.Errors)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintln(errOut, formatEsbuildMessage(warning))
	}
	if err := activateFrontendOutput(builder.settings, result.OutputFiles, result.Metafile); err != nil {
		return err
	}
	return nil
}

func formatEsbuildMessages(messages []api.Message) error {
	var lines []string
	for _, message := range messages {
		lines = append(lines, formatEsbuildMessage(message))
	}
	if len(lines) == 0 {
		return fmt.Errorf("esbuild failed without a diagnostic")
	}
	return fmt.Errorf("esbuild failed:\n%s", strings.Join(lines, "\n"))
}

func formatEsbuildMessage(message api.Message) string {
	if message.Location == nil {
		return message.Text
	}
	return fmt.Sprintf("%s:%d:%d: %s", message.Location.File, message.Location.Line, message.Location.Column, message.Text)
}

func activateFrontendOutput(settings frontendSettings, outputs []api.OutputFile, metafile string) error {
	parent := filepath.Dir(settings.output)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("could not create frontend output parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".spine-frontend-stage-")
	if err != nil {
		return fmt.Errorf("could not create temporary frontend output: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := copyFrontendPublic(filepath.Join(settings.root, "public"), stage); err != nil {
		return err
	}
	var hasCSS bool
	for _, output := range outputs {
		relative, err := filepath.Rel(settings.output, output.Path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("esbuild returned an output outside the configured frontend directory")
		}
		target := filepath.Join(stage, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("could not create frontend output directory: %w", err)
		}
		if err := os.WriteFile(target, output.Contents, 0o644); err != nil {
			return fmt.Errorf("could not write frontend output %q: %w", relative, err)
		}
		if strings.EqualFold(filepath.Ext(relative), ".css") && filepath.Base(relative) == "app.css" {
			hasCSS = true
		}
	}
	if err := writeFrontendLicenseNotices(settings, stage, metafile); err != nil {
		return err
	}
	entry, err := filepath.Rel(settings.root, settings.entry)
	if err != nil {
		return err
	}
	index, err := frontendIndex(filepath.Join(settings.root, "index.html"), hasCSS, filepath.ToSlash(entry))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "index.html"), []byte(index), 0o644); err != nil {
		return fmt.Errorf("could not write frontend index.html: %w", err)
	}
	markerPath := filepath.Join(stage, frontendOutputMarker)
	if _, err := os.Lstat(markerPath); err == nil {
		return fmt.Errorf("frontend public directory contains reserved file %q", frontendOutputMarker)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("could not inspect reserved frontend output file: %w", err)
	}
	if err := os.WriteFile(markerPath, []byte(frontendOutputMarkerContents), 0o644); err != nil {
		return fmt.Errorf("could not write frontend output marker: %w", err)
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return fmt.Errorf("could not set frontend output directory permissions: %w", err)
	}
	return replaceDirectory(settings.output, stage)
}

func frontendIndex(source string, hasCSS bool, entry string) (string, error) {
	contents, err := os.ReadFile(source)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("could not read frontend index.html: %w", err)
	}
	html := string(contents)
	if os.IsNotExist(err) || strings.TrimSpace(html) == "" {
		html = "<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n<title>Spine App</title>\n</head>\n<body>\n<div id=\"root\"></div>\n</body>\n</html>\n"
	}
	html = stripFrontendEntry(html, entry)
	tags := ""
	if hasCSS {
		tags += "<link rel=\"stylesheet\" href=\"/assets/app.css\">\n"
	}
	tags += "<script type=\"module\" src=\"/assets/app.js\"></script>\n"
	if marker := "<!-- spine:assets -->"; strings.Contains(html, marker) {
		return strings.Replace(html, marker, tags, 1), nil
	}
	lower := strings.ToLower(html)
	if index := strings.LastIndex(lower, "</body>"); index >= 0 {
		return html[:index] + tags + html[index:], nil
	}
	return html + "\n" + tags, nil
}

var frontendScriptElement = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
var frontendModuleScript = regexp.MustCompile(`(?i)\btype\s*=\s*["']module["']`)
var frontendScriptSource = regexp.MustCompile(`(?i)\bsrc\s*=\s*["']([^"']+)["']`)

func stripFrontendEntry(html, entry string) string {
	return frontendScriptElement.ReplaceAllStringFunc(html, func(script string) string {
		if !frontendModuleScript.MatchString(script) {
			return script
		}
		match := frontendScriptSource.FindStringSubmatch(script)
		if len(match) != 2 {
			return script
		}
		source := strings.TrimPrefix(strings.SplitN(match[1], "?", 2)[0], "/")
		source = strings.TrimPrefix(source, "./")
		if filepath.ToSlash(filepath.Clean(filepath.FromSlash(source))) == entry {
			return ""
		}
		return script
	})
}

func copyFrontendPublic(source, target string) error {
	info, err := os.Stat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not inspect frontend public directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("frontend public path is not a directory")
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, contents, 0o644)
	})
}

func replaceDirectory(target, stage string) error {
	info, statErr := os.Lstat(target)
	if os.IsNotExist(statErr) {
		if err := os.Rename(stage, target); err != nil {
			return fmt.Errorf("could not activate frontend output: %w", err)
		}
		return nil
	} else if statErr != nil {
		return fmt.Errorf("could not inspect previous frontend output: %w", statErr)
	}
	if !info.IsDir() {
		return fmt.Errorf("configured frontend output exists and is not a directory")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return fmt.Errorf("could not inspect previous frontend output: %w", err)
	}
	if len(entries) > 0 {
		markerPath := filepath.Join(target, frontendOutputMarker)
		markerInfo, markerErr := os.Lstat(markerPath)
		if markerErr != nil || !markerInfo.Mode().IsRegular() {
			return fmt.Errorf("frontend_output %q is non-empty and was not generated by Spine; choose another directory", target)
		}
		marker, markerErr := os.ReadFile(markerPath)
		if markerErr != nil || string(marker) != frontendOutputMarkerContents {
			return fmt.Errorf("frontend_output %q is non-empty and was not generated by Spine; choose another directory", target)
		}
	}
	parent := filepath.Dir(target)
	backup, err := os.MkdirTemp(parent, ".spine-frontend-previous-")
	if err != nil {
		return fmt.Errorf("could not prepare frontend rollback path: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("could not preserve previous frontend output: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return fmt.Errorf("could not activate frontend output: %w; restoring previous output also failed: %v", err, restoreErr)
		}
		return fmt.Errorf("could not activate frontend output: %w", err)
	}
	_ = os.RemoveAll(backup)
	return nil
}

func buildFrontend(projectRoot string, config projectConfig, production bool, errOut io.Writer) (*frontendSettings, error) {
	builder, enabled, err := newFrontendBuilder(projectRoot, config, production, errOut)
	if err != nil || !enabled {
		return nil, err
	}
	defer builder.close()
	if err := builder.rebuild(errOut); err != nil {
		return nil, err
	}
	return &builder.settings, nil
}
