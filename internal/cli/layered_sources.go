package cli

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// layoutSources는 디스크 소스와 아직 적용하지 않은 변경을 같은 규칙으로 조회합니다.
type layoutSources map[string]*layoutSource

func (sources layoutSources) sortedFiles() []*layoutSource {
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]*layoutSource, 0, len(paths))
	for _, path := range paths {
		result = append(result, sources[path])
	}
	return result
}

func (sources layoutSources) packageFiles(directory string) []*layoutSource {
	result := []*layoutSource{}
	for _, source := range sources.sortedFiles() {
		if source.Package == directory {
			result = append(result, source)
		}
	}
	return result
}

type layoutConstructor struct {
	Output       string
	Dependencies []string
}

// constructorSignatures는 생성 계획과 계약 검사가 공유하는 패키지 기준 생성자 목록입니다.
func (sources layoutSources) constructorSignatures(module string) map[string][]layoutConstructor {
	result := map[string][]layoutConstructor{}
	for _, source := range sources.sortedFiles() {
		for _, declaration := range source.File.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil {
				continue
			}
			signature := layoutConstructor{Dependencies: []string{}}
			if len(fieldTypes(source.Fset, function.Type.Results)) == 1 && function.Type.TypeParams == nil {
				signature.Output = canonicalType(source, function.Type.Results.List[0].Type, module)
			}
			if function.Type.Params != nil {
				for _, field := range function.Type.Params.List {
					count := len(field.Names)
					if count == 0 {
						count = 1
					}
					for index := 0; index < count; index++ {
						signature.Dependencies = append(signature.Dependencies, canonicalType(source, field.Type, module))
					}
				}
			}
			key := modulePackage(module, source.Package) + "." + function.Name.Name
			result[key] = append(result[key], signature)
		}
	}
	return result
}

type layoutSource struct {
	Path    string
	File    *ast.File
	Fset    *token.FileSet
	Package string
}

// readLayoutSources는 프로젝트 코드를 실행하지 않고 현재 빌드에 포함된 소스만 읽습니다.
func readLayoutSources() (layoutSources, string, error) {
	sources := layoutSources{}
	digest := sha256.New()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != "." {
				if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "bin" || entry.Name() == "vendor" || entry.Name() == "node_modules" || entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if strings.HasSuffix(path, ".go") || path == "spine.toml" || path == "go.mod" || path == "go.sum" {
				return fmt.Errorf("source is a symlink: %s", path)
			}
			return nil
		}
		isGo := strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
		if !isGo && path != "go.mod" && path != "go.sum" && path != "spine.toml" {
			return nil
		}
		if isGo {
			match, err := build.Default.MatchFile(filepath.Dir(path), filepath.Base(path))
			if err != nil {
				return err
			}
			if !match {
				return nil
			}
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "%d:%s\n%d:", len(path), filepath.ToSlash(path), len(contents))
		digest.Write(contents)
		if isGo {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, contents, parser.ParseComments)
			if err != nil {
				return err
			}
			sources[filepath.ToSlash(path)] = &layoutSource{Path: path, File: file, Fset: fset, Package: filepath.ToSlash(filepath.Dir(path))}
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	// 등록 해석에 쓰는 환경 값도 계획에 고정하며, 값 자체는 출력하지 않습니다.
	inputs := map[string]string{}
	for _, source := range sources.sortedFiles() {
		imports := importPathsByAlias(source.File)
		ast.Inspect(source.File, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Getenv" {
				return true
			}
			alias, ok := selector.X.(*ast.Ident)
			if !ok || alias.Obj != nil || imports[alias.Name] != "os" {
				return true
			}
			if name, ok := stringLiteral(call.Args[0]); ok {
				inputs[name] = os.Getenv(name)
			}
			return true
		})
	}
	names := make([]string, 0, len(inputs))
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(digest, "env:%d:%s\n%d:%s\n", len(name), name, len(inputs[name]), inputs[name])
	}
	return sources, fmt.Sprintf("%x", digest.Sum(nil)), nil
}

type layoutComponent struct {
	Role    string `json:"role"`
	Group   string `json:"group"`
	Package string `json:"package"`
	Type    string `json:"type"`
}

func layoutInventory(module string) ([]layoutComponent, error) {
	sources, _, err := readLayoutSources()
	if err != nil {
		return nil, err
	}
	result := []layoutComponent{}
	for _, source := range sources {
		for _, role := range componentRoles() {
			if source.Package != role.Root && !strings.HasPrefix(source.Package, role.Root+"/") {
				continue
			}
			group := strings.TrimPrefix(strings.TrimPrefix(source.Package, role.Root), "/")
			for _, declaration := range source.File.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok != token.TYPE {
					continue
				}
				for _, spec := range general.Specs {
					t := spec.(*ast.TypeSpec)
					if ast.IsExported(t.Name.Name) {
						result = append(result, layoutComponent{role.Name, group, module + "/" + source.Package, t.Name.Name})
					}
				}
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Package+":"+result[i].Type < result[j].Package+":"+result[j].Type
	})
	return result, nil
}

func (sources layoutSources) composition(composition string) (*layoutSource, *ast.FuncDecl, int, string, error) {
	paths := []string{}
	target := filepath.ToSlash(filepath.Clean(composition))
	for path, s := range sources {
		if path == target || s.Package == target {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		s := sources[path]

		for _, decl := range s.File.Decls {
			f, ok := decl.(*ast.FuncDecl)
			if !ok || f.Body == nil {
				continue
			}
			for index, stmt := range f.Body.List {
				if name, ok := applicationAssignment(stmt, s.File); ok {
					return s, f, index, name, nil
				}
			}
		}
	}
	return nil, nil, 0, "", fmt.Errorf("could not find a supported top-level Spine application assignment in composition path %q", composition)
}

func canonicalType(s *layoutSource, expression ast.Expr, module string) string {
	switch e := expression.(type) {
	case *ast.ParenExpr:
		return canonicalType(s, e.X, module)
	case *ast.StarExpr:
		return "*" + canonicalType(s, e.X, module)
	case *ast.Ident:
		if types.Universe.Lookup(e.Name) != nil {
			return e.Name
		}
		return modulePackage(module, s.Package) + ":" + e.Name
	case *ast.SelectorExpr:
		if alias, ok := e.X.(*ast.Ident); ok {
			if path := importPathsByAlias(s.File)[alias.Name]; path != "" {
				return path + ":" + e.Sel.Name
			}
		}
	}
	return expressionText(s.Fset, expression)
}
