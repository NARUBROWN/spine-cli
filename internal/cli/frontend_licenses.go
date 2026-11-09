package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type frontendPackageManifest struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	License json.RawMessage `json:"license"`
}

func writeFrontendLicenseNotices(settings frontendSettings, outputRoot string, metafile string) error {
	var metadata struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(metafile), &metadata); err != nil {
		return fmt.Errorf("could not read frontend dependency metadata: %w", err)
	}
	roots := make(map[string]struct{})
	for input := range metadata.Inputs {
		packageRoot := frontendPackageRoot(settings.root, input)
		if packageRoot == "" || frontendRootWithinPackage(packageRoot, settings.root) {
			continue
		}
		roots[packageRoot] = struct{}{}
	}
	if len(roots) == 0 {
		return nil
	}
	paths := make([]string, 0, len(roots))
	for root := range roots {
		paths = append(paths, root)
	}
	sort.Strings(paths)

	var notice strings.Builder
	notice.WriteString("Third-party frontend dependency licenses\n\n")
	for _, root := range paths {
		manifestBytes, err := os.ReadFile(filepath.Join(root, "package.json"))
		if err != nil {
			return fmt.Errorf("could not read bundled frontend dependency metadata at %q: %w", root, err)
		}
		var manifest frontendPackageManifest
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			return fmt.Errorf("could not parse bundled frontend dependency metadata at %q: %w", root, err)
		}
		name := manifest.Name
		if name == "" {
			name = filepath.Base(root)
		}
		version := manifest.Version
		if version == "" {
			version = "version unavailable"
		}
		license := frontendLicenseDeclaration(manifest.License)
		if license == "" {
			license = "license metadata unavailable"
		}
		fmt.Fprintf(&notice, "============================================================\n%s@%s\nLicense: %s\n", name, version, license)
		licensePaths, err := findFrontendPackageLicenses(root)
		if err != nil {
			return fmt.Errorf("could not inspect bundled frontend dependency license at %q: %w", root, err)
		}
		if len(licensePaths) == 0 {
			notice.WriteString("No license text file was found in this package.\n\n")
			continue
		}
		for _, licensePath := range licensePaths {
			contents, err := os.ReadFile(licensePath)
			if err != nil {
				return fmt.Errorf("could not read bundled frontend dependency license %q: %w", licensePath, err)
			}
			fmt.Fprintf(&notice, "License text: %s\n", filepath.Base(licensePath))
			notice.Write(contents)
			if len(contents) == 0 || contents[len(contents)-1] != '\n' {
				notice.WriteByte('\n')
			}
			notice.WriteByte('\n')
		}
	}

	outputPath := filepath.Join(outputRoot, "THIRD-PARTY-LICENSES.txt")
	if existing, err := os.ReadFile(outputPath); err == nil {
		notice.WriteString("\nAdditional notices from the configured frontend public/THIRD-PARTY-LICENSES.txt\n\n")
		notice.Write(existing)
		if len(existing) == 0 || existing[len(existing)-1] != '\n' {
			notice.WriteByte('\n')
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("could not read existing frontend license notices: %w", err)
	}
	if err := os.WriteFile(outputPath, []byte(notice.String()), 0o644); err != nil {
		return fmt.Errorf("could not write frontend dependency license notices: %w", err)
	}
	return nil
}

func frontendPackageRoot(frontendRoot, input string) string {
	path := filepath.FromSlash(input)
	if !filepath.IsAbs(path) {
		path = filepath.Join(frontendRoot, path)
	}
	path = filepath.Clean(path)
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		if contents, err := os.ReadFile(filepath.Join(directory, "package.json")); err == nil {
			var manifest frontendPackageManifest
			if json.Unmarshal(contents, &manifest) == nil && strings.TrimSpace(manifest.Name) != "" {
				return directory
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return ""
		}
	}
}

func frontendRootWithinPackage(packageRoot, frontendRoot string) bool {
	relative, err := filepath.Rel(filepath.Clean(packageRoot), filepath.Clean(frontendRoot))
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)))
}

func frontendLicenseDeclaration(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var expression struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &expression); err == nil {
		return strings.TrimSpace(expression.Type)
	}
	return strings.TrimSpace(string(raw))
}

func findFrontendPackageLicenses(packageRoot string) ([]string, error) {
	entries, err := os.ReadDir(packageRoot)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		upper := strings.ToUpper(entry.Name())
		if upper == "LICENSE" || upper == "LICENCE" || upper == "COPYING" || strings.HasPrefix(upper, "LICENSE.") || strings.HasPrefix(upper, "LICENCE.") || strings.HasPrefix(upper, "COPYING.") || strings.HasPrefix(upper, "LICENSE-") || strings.HasPrefix(upper, "LICENCE-") || strings.HasPrefix(upper, "COPYING-") {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(packageRoot, name))
	}
	return paths, nil
}
