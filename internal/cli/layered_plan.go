package cli

import (
	"crypto/sha256"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type layeredPlanner struct {
	Config   projectConfig
	Module   string
	Sources  layoutSources
	Changes  map[string]fileChange
	Register bool
	Force    bool
}

func (p *layeredPlanner) put(path, contents string, overwrite bool) error {
	path = filepath.ToSlash(filepath.Clean(path))
	if err := regularAncestors(filepath.Dir(path)); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("generation target is not a regular file: %s", path)
	}
	if strings.HasSuffix(path, ".go") {
		formatted, err := format.Source([]byte(contents))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		contents = string(formatted)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, contents, parser.ParseComments)
		if err != nil {
			return err
		}
		p.Sources[path] = &layoutSource{Path: path, File: file, Fset: fset, Package: filepath.ToSlash(filepath.Dir(path))}
	}
	// 동일한 요청은 기존 파일을 다시 쓰지 않고도 재실행할 수 있습니다.
	if old, err := os.ReadFile(path); err == nil && string(old) == contents {
		overwrite = true
	}
	p.Changes[path] = fileChange{Path: path, Contents: contents, AllowOverwrite: overwrite || p.Force}
	return nil
}

func (p *layeredPlanner) update(source *layoutSource) error {
	var b strings.Builder
	if err := format.Node(&b, source.Fset, source.File); err != nil {
		return err
	}
	return p.put(source.Path, b.String(), true)
}

func layeredPlanHash(changes []fileChange, inputHash string) (string, error) {
	base, err := fileChangesBaseHash(changes)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%s\n", layeredLayout, base, inputHash)
	for _, c := range changes {
		fmt.Fprintf(h, "%d:%s\n%d:", len(c.Path), c.Path, len(c.Contents))
		h.Write([]byte(c.Contents))
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// planLayeredGeneration은 소스 변경을 메모리에서 구성하고 적용 전에 검증합니다.
func planLayeredGeneration(request layeredRequest, config projectConfig, module string, sources layoutSources) (*layeredPlanner, error) {
	role, component := roleFor(request.Kind)
	planner := &layeredPlanner{
		Config: config, Module: module, Sources: sources, Changes: map[string]fileChange{},
		Register: request.Register && (!component || role.Constructor), Force: request.Options.Force,
	}
	var err error
	switch request.Kind {
	case "consumer", "websocket":
		err = planner.addMessageModule(request)
	case "route":
		err = planner.addRoute(request)
	case "resource":
		for _, kind := range []string{"domain", "repository", "service", "controller"} {
			dependencies := []string{}
			if kind == "service" || kind == "controller" {
				dependencyKind := "repository"
				if kind == "controller" {
					dependencyKind = "service"
				}
				dependencyRole, _ := roleFor(dependencyKind)
				dependencies = append(dependencies, filepath.ToSlash(filepath.Join(dependencyRole.Root, request.Name.Group))+":"+request.Name.Name.Type+dependencyRole.Suffix)
			}
			if err = planner.addComponent(kind, request.Name, nil, dependencies); err != nil {
				break
			}
		}
	default:
		err = planner.addComponent(request.Kind, request.Name, request.Fields, request.Depends)
	}
	if err != nil {
		return nil, err
	}
	if err = planner.validateGraphs(); err != nil {
		return nil, err
	}
	return planner, nil
}
