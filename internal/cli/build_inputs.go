package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type buildInputModule struct {
	Main    bool
	Version string
	GoMod   string
	Replace *buildInputModule
}

type buildInputPackage struct {
	Dir                                                                                                                 string
	Standard                                                                                                            bool
	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SwigFiles, SwigCXXFiles, SysoFiles, EmbedFiles []string
	Module                                                                                                              *buildInputModule
	Error                                                                                                               *struct{ Err string }
}

func sourceState(root string) (string, error) {
	return applicationSourceState(context.Background(), root, projectConfig{Entrypoint: "."})
}

// applicationSourceState는 Go가 선택한 빌드 입력만 읽어 실행 중 생성한 로그와 구분합니다.
func applicationSourceState(ctx context.Context, root string, config projectConfig) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fields := "Dir,Standard,GoFiles,CgoFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles,SwigFiles,SwigCXXFiles,SysoFiles,EmbedFiles,Module,Error"
	command := exec.CommandContext(ctx, "go", "list", "-e", "-deps", "-json="+fields, config.Entrypoint)
	command.Dir = root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("could not list Go build inputs: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	tool := exec.CommandContext(ctx, "go", "env", "-json", "GOVERSION", "GOROOT", "GOMODCACHE")
	tool.Dir = root
	identity, err := tool.Output()
	if err != nil {
		return "", fmt.Errorf("could not identify Go toolchain: %w", err)
	}
	var environment struct{ GOMODCACHE string }
	if err := json.Unmarshal(identity, &environment); err != nil {
		return "", fmt.Errorf("invalid Go environment metadata: %w", err)
	}
	paths := map[string]bool{}
	add := func(path string) {
		if path != "" {
			paths[filepath.Clean(path)] = true
		}
	}
	for _, name := range []string{"go.mod", "go.sum", "spine.toml"} {
		add(filepath.Join(root, name))
	}
	var addModule func(*buildInputModule)
	addModule = func(module *buildInputModule) {
		if module == nil {
			return
		}
		add(module.GoMod)
		if strings.HasSuffix(module.GoMod, ".mod") {
			add(strings.TrimSuffix(module.GoMod, ".mod") + ".sum")
		}
		addModule(module.Replace)
	}
	digest := sha256.New()
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg buildInputPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return "", fmt.Errorf("invalid Go package metadata: %w", err)
		}
		if pkg.Error != nil {
			fmt.Fprintf(digest, "package-error:%s\n", pkg.Error.Err)
		}
		if pkg.Standard || immutableModuleCachePackage(pkg, root, environment.GOMODCACHE) {
			continue
		}
		addModule(pkg.Module)
		for _, names := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles} {
			for _, name := range names {
				add(filepath.Join(pkg.Dir, name))
			}
		}
	}
	workspace := os.Getenv("GOWORK")
	if workspace != "off" {
		if workspace == "" || workspace == "auto" {
			for directory := root; ; directory = filepath.Dir(directory) {
				candidate := filepath.Join(directory, "go.work")
				if _, err := os.Stat(candidate); err == nil {
					workspace = candidate
					break
				}
				if filepath.Dir(directory) == directory {
					break
				}
			}
		}
		if filepath.IsAbs(workspace) {
			add(workspace)
			add(workspace + ".sum")
		}
	}
	digest.Write(identity)
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		contents, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			fmt.Fprintf(digest, "missing:%d:%s\n", len(path), path)
			continue
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(digest, "%d:%s\n%d:", len(path), filepath.ToSlash(path), len(contents))
		digest.Write(contents)
		digest.Write([]byte{'\n'})
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

// 모듈 캐시 안의 버전 고정 의존성만 생략하고 vendor, 작업 모듈, 로컬 replace는 유지합니다.
func immutableModuleCachePackage(pkg buildInputPackage, root, cache string) bool {
	if pkg.Module == nil || pkg.Module.Main || (pkg.Module.Replace != nil && pkg.Module.Replace.Version == "") {
		return false
	}
	inside := func(parent, directory string) bool {
		if parent == "" || directory == "" {
			return false
		}
		relative, err := filepath.Rel(parent, directory)
		return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
	}
	return inside(cache, pkg.Dir) && !inside(root, pkg.Dir)
}
