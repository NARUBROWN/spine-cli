package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const frontendProviderManifest = "spine-frontend.json"
const frontendProviderSchema = "spine.frontend/v1"

var frontendProviderModuleName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$`)
var frontendNPMName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
var frontendNPMVersion = regexp.MustCompile(`^[0-9A-Za-z*^~><=|+._-]+$`)

type frontendProviderDescriptor struct {
	SchemaVersion   string                `json:"schema_version"`
	Provider        string                `json:"provider"`
	Framework       string                `json:"framework"`
	Engine          string                `json:"engine"`
	JSX             string                `json:"jsx,omitempty"`
	JSXImportSource string                `json:"jsx_import_source,omitempty"`
	EntryCandidates []string              `json:"entry_candidates"`
	Loaders         map[string]string     `json:"loaders,omitempty"`
	NPMDependencies map[string]string     `json:"npm_dependencies,omitempty"`
	StarterFiles    []frontendStarterFile `json:"starter_files,omitempty"`
	Module          selectedModule        `json:"-"`
}

type frontendStarterFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

func loadProjectFrontendDescriptor(root, provider string) (*frontendProviderDescriptor, error) {
	path := filepath.Join(root, frontendProviderManifest)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if provider == "react" {
			return frontendProjectDescriptor(defaultReactFrontendDescriptor())
		}
		return nil, fmt.Errorf("frontend provider %q requires %s under frontend_root", provider, frontendProviderManifest)
	}
	if err != nil {
		return nil, fmt.Errorf("could not inspect frontend provider metadata: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("frontend provider metadata %q must be a regular file", frontendProviderManifest)
	}
	manifest, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open frontend provider metadata: %w", err)
	}
	descriptor, readErr := readFrontendProviderDescriptor(manifest, provider, false)
	closeErr := manifest.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return descriptor, nil
}

func frontendProviderModule(provider string) string {
	if !frontendProviderModuleName.MatchString(provider) {
		return ""
	}
	return "github.com/NARUBROWN/spine-frontend-" + provider
}

func defaultReactFrontendDescriptor() *frontendProviderDescriptor {
	return &frontendProviderDescriptor{
		SchemaVersion:   frontendProviderSchema,
		Provider:        "react",
		Framework:       "React CSR",
		Engine:          "esbuild",
		JSX:             "automatic",
		JSXImportSource: "react",
		EntryCandidates: []string{"src/main.tsx", "src/main.jsx", "src/main.ts", "src/main.js"},
		Loaders:         map[string]string{".js": "jsx"},
		NPMDependencies: map[string]string{"react": "19.3.1", "react-dom": "19.3.1"},
		StarterFiles: []frontendStarterFile{
			{Path: "index.html", Contents: "<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n<title>Spine React App</title>\n</head>\n<body>\n<div id=\"root\"></div>\n</body>\n</html>\n"},
			{Path: "src/main.tsx", Contents: "import { StrictMode, useState } from \"react\";\nimport { createRoot } from \"react-dom/client\";\n\nfunction App() {\n  const [count, setCount] = useState(0);\n  return (\n    <main>\n      <h1>Spine React CSR</h1>\n      <button onClick={() => setCount((value) => value + 1)}>Count: {count}</button>\n    </main>\n  );\n}\n\nconst root = document.getElementById(\"root\");\nif (!root) throw new Error(\"#root element is missing\");\ncreateRoot(root).render(<StrictMode><App /></StrictMode>);\n"},
		},
	}
}

func validateFrontendProviderDescriptor(descriptor *frontendProviderDescriptor, provider string, requireStarterFiles bool) error {
	invalid := func(reason string) error {
		return fmt.Errorf("invalid %s for frontend provider %q: %s", frontendProviderManifest, provider, reason)
	}
	if descriptor.SchemaVersion != frontendProviderSchema || descriptor.Provider != provider || strings.TrimSpace(descriptor.Framework) == "" || len(descriptor.Framework) > 100 || strings.ContainsAny(descriptor.Framework, "\n\r\t") || descriptor.Engine != "esbuild" {
		return invalid("schema_version, provider, framework or engine is unsupported")
	}
	if !frontendProviderModuleName.MatchString(descriptor.Provider) {
		return invalid("provider must be a lowercase provider identifier")
	}
	if descriptor.JSX != "" && descriptor.JSX != "automatic" && descriptor.JSX != "transform" && descriptor.JSX != "preserve" {
		return invalid("jsx must be automatic, transform or preserve")
	}
	if descriptor.JSX == "automatic" && descriptor.JSXImportSource == "" {
		return invalid("jsx_import_source is required for automatic JSX")
	}
	if descriptor.JSXImportSource != "" && (strings.ContainsAny(descriptor.JSXImportSource, "\\\"'\n\r\t ") || strings.HasPrefix(descriptor.JSXImportSource, ".")) {
		return invalid("jsx_import_source must be a package specifier")
	}
	if len(descriptor.EntryCandidates) == 0 || len(descriptor.EntryCandidates) > 32 {
		return invalid("entry_candidates must contain between 1 and 32 paths")
	}
	seenEntries := map[string]bool{}
	for _, entry := range descriptor.EntryCandidates {
		if err := validateProjectRelativePath(entry); err != nil || filepath.Clean(filepath.FromSlash(entry)) == "." || seenEntries[filepath.Clean(filepath.FromSlash(entry))] {
			return invalid("entry_candidates must be unique project-relative file paths")
		}
		seenEntries[filepath.Clean(filepath.FromSlash(entry))] = true
	}
	allowedLoaders := map[string]bool{"js": true, "jsx": true, "ts": true, "tsx": true, "json": true, "text": true, "base64": true, "dataurl": true, "file": true, "binary": true, "copy": true, "css": true, "local-css": true, "global-css": true}
	for extension, loader := range descriptor.Loaders {
		if !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, "/\\\n\r\t") || !allowedLoaders[loader] {
			return invalid("loaders must map file extensions to supported esbuild loaders")
		}
	}
	for name, version := range descriptor.NPMDependencies {
		if !frontendNPMName.MatchString(name) || version == "" || !frontendNPMVersion.MatchString(version) {
			return invalid("npm_dependencies must map package names to plain version ranges")
		}
	}
	if !requireStarterFiles && len(descriptor.StarterFiles) > 0 {
		return invalid("project metadata must not contain starter_files")
	}
	if requireStarterFiles {
		if len(descriptor.StarterFiles) == 0 || len(descriptor.StarterFiles) > 32 {
			return invalid("starter_files must contain between 1 and 32 files")
		}
		seenFiles := map[string]bool{}
		totalBytes := 0
		hasIndex, hasEntry := false, false
		for _, file := range descriptor.StarterFiles {
			clean := filepath.Clean(filepath.FromSlash(file.Path))
			if err := validateProjectRelativePath(file.Path); err != nil || clean == "." || seenFiles[clean] {
				return invalid("starter_files paths must be unique project-relative file paths")
			}
			seenFiles[clean] = true
			totalBytes += len(file.Contents)
			if totalBytes > 1<<20 {
				return invalid("starter_files exceed 1 MiB")
			}
			hasIndex = hasIndex || filepath.ToSlash(clean) == "index.html"
			hasEntry = hasEntry || seenEntries[clean]
		}
		if !hasIndex || !hasEntry {
			return invalid("starter_files must include index.html and at least one entry candidate")
		}
	}
	return nil
}

func readFrontendProviderDescriptor(reader io.Reader, provider string, requireStarterFiles bool) (*frontendProviderDescriptor, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("%s exceeds 1 MiB", frontendProviderManifest)
	}
	var descriptor frontendProviderDescriptor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", frontendProviderManifest, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%s must contain one JSON object", frontendProviderManifest)
	}
	if err := validateFrontendProviderDescriptor(&descriptor, provider, requireStarterFiles); err != nil {
		return nil, err
	}
	return &descriptor, nil
}

func resolveFrontendProvider(ctx context.Context, provider string) (*frontendProviderDescriptor, error) {
	if provider == "react" {
		return defaultReactFrontendDescriptor(), nil
	}
	path := frontendProviderModule(provider)
	if path == "" {
		return nil, fmt.Errorf("invalid frontend provider name %q", provider)
	}
	resolved, err := previewModuleResolver(ctx, []selectedModule{{Path: path, Version: "preview"}})
	if err != nil {
		return nil, fmt.Errorf("search frontend provider %q: %w", provider, err)
	}
	module := resolved[0]
	temporary, err := os.MkdirTemp("", "spine-frontend-provider-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	output, err := platformGo(ctx, temporary, false, "mod", "download", "-json", module.Path+"@"+module.Version)
	if err != nil {
		return nil, fmt.Errorf("download frontend provider %s@%s: %w", module.Path, module.Version, err)
	}
	var downloaded struct{ Dir string }
	if err := json.Unmarshal(output, &downloaded); err != nil || downloaded.Dir == "" {
		return nil, fmt.Errorf("module directory was not returned for frontend provider %s@%s", module.Path, module.Version)
	}
	manifest, err := os.Open(filepath.Join(downloaded.Dir, frontendProviderManifest))
	if err != nil {
		return nil, fmt.Errorf("frontend provider %s@%s has no %s: %w", module.Path, module.Version, frontendProviderManifest, err)
	}
	descriptor, readErr := readFrontendProviderDescriptor(manifest, provider, true)
	closeErr := manifest.Close()
	if readErr != nil {
		return nil, fmt.Errorf("frontend provider %s@%s: %w", module.Path, module.Version, readErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	module.Dir = downloaded.Dir
	descriptor.Module = module
	return descriptor, nil
}

func frontendProjectDescriptor(descriptor *frontendProviderDescriptor) (*frontendProviderDescriptor, error) {
	project := *descriptor
	project.StarterFiles = nil
	project.Module = selectedModule{}
	data, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return nil, err
	}
	return readFrontendProviderDescriptor(bytes.NewReader(data), descriptor.Provider, false)
}
