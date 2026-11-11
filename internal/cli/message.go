package cli

import (
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"
)

func validateMessageFields(fields []sliceField) error {
	if len(fields) == 0 {
		return fmt.Errorf("message generation requires at least one --field name:type")
	}
	return validateSliceFields(fields)
}

func messageModuleFiles(kind string, name resourceName, registrationName string, fields []sliceField, publicContext bool) map[string]string {
	base := filepath.Join("src", kind, name.Package)
	return map[string]string{
		filepath.Join(base, "module.go"):                messageModuleTemplate(kind, name, registrationName),
		filepath.Join(base, name.Package+"_handler.go"): messageHandlerTemplate(kind, name, publicContext),
		filepath.Join(base, name.Package+"_message.go"): messagePayloadTemplate(kind, name, fields),
	}
}

func messageModuleTemplate(kind string, name resourceName, registrationName string) string {
	registry := "Consumers"
	handlerType := name.Type + "Consumer"
	if kind == "websocket" {
		registry = "WebSocket"
		handlerType = name.Type + "Socket"
	}
	return fmt.Sprintf(`package %s

import "github.com/NARUBROWN/spine"

// Register는 생성자와 메시지 수신 계약을 애플리케이션에 등록합니다.
func Register(app spine.App) error {
	app.Constructor(New%s)
	if err := app.%s().Register(%q, (*%s).Handle); err != nil {
		return err
	}
	return nil
}
`, name.Package, handlerType, registry, registrationName, handlerType)
}

// messageContext는 선택한 코어의 공개 타입을 확인하고 지원하는 컨텍스트를 생성합니다.
func messageContext() (bool, error) {
	modules, err := selectedModules(context.Background(), ".", true)
	if err != nil {
		return false, fmt.Errorf("message context module selection: %w", err)
	}
	public := modulesExportSpineContext(modules)
	version := selectedModuleVersion(modules, spineImportPath)
	if !public && semver.IsValid(version) && semver.Compare(version, "v0.7.5-preview.1") >= 0 {
		return false, fmt.Errorf("selected core %s requires pkg/spine.Ctx for message generation; check the selected module source or replace directive before regenerating", version)
	}
	return public, nil
}

func modulesExportSpineContext(modules []selectedModule) bool {
	for _, module := range modules {
		if module.Path != spineImportPath {
			continue
		}
		root := module.Dir
		if module.Replace != nil && module.Replace.Dir != "" {
			root = module.Replace.Dir
		}
		if root == "" {
			return false
		}
		dir := filepath.Join(root, "pkg", "spine")
		pkg, err := build.Default.ImportDir(dir, 0)
		if err != nil {
			return false
		}
		for _, name := range pkg.GoFiles {
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
			if err != nil {
				return false
			}
			object := file.Scope.Lookup("Ctx")
			if object != nil {
				if _, declaredType := object.Decl.(*ast.TypeSpec); !declaredType {
					continue
				}
				return true
			}
		}
	}
	return false
}

func messageHandlerTemplate(kind string, name resourceName, publicContext bool) string {
	contextImport, contextType := "context", "context.Context"
	if publicContext {
		contextImport, contextType = spineImportPath+"/pkg/spine", "spine.Ctx"
	}
	if kind == "consumer" {
		return fmt.Sprintf(`package %s

import (
	%q
	"errors"
)

type %sConsumer struct{}

func New%sConsumer() *%sConsumer { return &%sConsumer{} }

func (c *%sConsumer) Handle(ctx %s, eventName string, event %sEvent) error {
	// TODO: 수신한 이벤트를 처리한 뒤 이 오류를 제거합니다.
	return errors.New("%s consumer handler is not implemented")
}
`, name.Package, contextImport, name.Type, name.Type, name.Type, name.Type, name.Type, contextType, name.Type, name.Package)
	}
	return fmt.Sprintf(`package %s

import (
	%q
	"errors"

	"github.com/NARUBROWN/spine/pkg/ws"
)

type %sSocket struct{}

func New%sSocket() *%sSocket { return &%sSocket{} }

func (s *%sSocket) Handle(ctx %s, connectionID ws.ConnectionID, message %sMessage) error {
	// TODO: 메시지를 처리하고 필요하면 ws.Send로 응답한 뒤 이 오류를 제거합니다.
	return errors.New("%s websocket handler is not implemented")
}
`, name.Package, contextImport, name.Type, name.Type, name.Type, name.Type, name.Type, contextType, name.Type, name.Package)
}

func messagePayloadTemplate(kind string, name resourceName, fields []sliceField) string {
	suffix := "Event"
	if kind == "websocket" {
		suffix = "Message"
	}
	var declarations strings.Builder
	for _, field := range fields {
		fmt.Fprintf(&declarations, "\t%s %s `json:\"%s\"`\n", field.GoName, field.GoType, field.JSONName)
	}
	return fmt.Sprintf(`package %s

type %s%s struct {
%s}
`, name.Package, name.Type, suffix, declarations.String())
}
