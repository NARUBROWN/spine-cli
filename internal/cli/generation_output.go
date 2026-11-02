package cli

import (
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

type generatedTypeInfo struct {
	Reference   string `json:"reference"`
	Source      string `json:"source"`
	Constructor string `json:"constructor,omitempty"`
}

type generatedRegistrationInfo struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Source string `json:"source"`
}

type generationDetails struct {
	Types         []generatedTypeInfo         `json:"types"`
	Registrations []generatedRegistrationInfo `json:"registrations"`
	Routes        []httpRegistration          `json:"routes"`
}

// 생성 결과에 파일 경로와 실제 타입 참조 및 등록 위치를 표시합니다.
func (p *layeredPlanner) details(request layeredRequest) generationDetails {
	details := generationDetails{Types: []generatedTypeInfo{}, Registrations: []generatedRegistrationInfo{}, Routes: []httpRegistration{}}
	directories := map[string]bool{}
	if role, ok := roleFor(request.Kind); ok {
		directories[filepath.ToSlash(filepath.Join(role.Root, request.Name.Group))] = true
	}
	if request.Kind == "resource" {
		for _, role := range componentRoles() {
			if role.Name != "dto" {
				directories[filepath.ToSlash(filepath.Join(role.Root, request.Name.Group))] = true
			}
		}
	}
	if request.Kind == "consumer" || request.Kind == "websocket" {
		directories[filepath.ToSlash(filepath.Join("src", request.Kind, request.Name.Group, request.Name.Name.Package))] = true
	}
	composition := ""
	if p.Register {
		if source, _, _, _, err := p.composition(); err == nil {
			composition = source.Path
		}
	}
	for _, source := range p.Sources.sortedFiles() {
		if !directories[source.Package] {
			continue
		}
		if _, changed := p.Changes[source.Path]; !changed {
			continue
		}
		for _, node := range source.File.Decls {
			group, ok := node.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, specification := range group.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				if !ast.IsExported(typeSpec.Name.Name) {
					continue
				}
				info := generatedTypeInfo{Reference: source.Package + ":" + typeSpec.Name.Name, Source: source.Path}
				for _, file := range p.Sources.packageFiles(source.Package) {
					for _, declaration := range file.File.Decls {
						function, ok := declaration.(*ast.FuncDecl)
						if ok && function.Recv == nil && function.Name.Name == "New"+typeSpec.Name.Name {
							info.Constructor = source.Package + ":" + function.Name.Name
						}
					}
				}
				details.Types = append(details.Types, info)
				if p.Register && info.Constructor != "" {
					path := composition
					if request.Kind == "consumer" || request.Kind == "websocket" {
						path = filepath.ToSlash(filepath.Join(source.Package, "module.go"))
					}
					details.Registrations = append(details.Registrations, generatedRegistrationInfo{"constructor", info.Constructor, path})
				}
			}
		}
	}
	if request.Kind == "route" {
		details.Routes = append(details.Routes, httpRegistration{Method: request.Method, Path: request.Path, Handler: request.Handler, Source: layeredRoutePath})
		if pkg, handler, ok := strings.Cut(request.Handler, ":"); ok {
			name, _, _ := strings.Cut(handler, ".")
			if dependency, err := p.resolveDependency(pkg + ":" + name); err == nil {
				details.Registrations = append(details.Registrations, generatedRegistrationInfo{"constructor", pkg + ":" + dependency.Factory, composition})
			}
		}
		details.Registrations = append(details.Registrations, generatedRegistrationInfo{"module", "src/route:Register", composition})
	}
	if p.Register && (request.Kind == "consumer" || request.Kind == "websocket") {
		directory := filepath.ToSlash(filepath.Join("src", request.Kind, request.Name.Group, request.Name.Name.Package))
		details.Registrations = append(details.Registrations, generatedRegistrationInfo{"module", directory + ":Register", composition})
	}
	sort.Slice(details.Types, func(i, j int) bool { return details.Types[i].Reference < details.Types[j].Reference })
	return details
}

func printGeneratedFiles(out io.Writer, changes []plannedChange) {
	for _, change := range changes {
		verb := "Created"
		if change.Operation == "modify" {
			verb = "Updated"
		}
		if change.Operation == "unchanged" {
			verb = "Unchanged"
		}
		fmt.Fprintf(out, "  %s %s\n", verb, change.Path)
	}
}

func printGenerationDetails(out io.Writer, details generationDetails) {
	for _, typ := range details.Types {
		fmt.Fprintf(out, "  Type reference: %s\n", typ.Reference)
	}
	for _, registration := range details.Registrations {
		fmt.Fprintf(out, "  Registration (%s): %s -> %s\n", registration.Kind, registration.Target, registration.Source)
	}
	for _, route := range details.Routes {
		fmt.Fprintf(out, "  Route: %s %s -> %s\n", route.Method, route.Path, route.Handler)
	}
}
