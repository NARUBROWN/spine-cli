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

func messageModuleFiles(kind string, name resourceName, registrationName string, fields []sliceField, contextType string) map[string]string {
	base := filepath.Join("src", kind, name.Package)
	if kind == "websocket" && contextType == "spine.WebSocketCtx" {
		return map[string]string{
			filepath.Join(base, "module.go"): fmt.Sprintf(`package %s

import (
    "github.com/NARUBROWN/spine"
    "github.com/NARUBROWN/spine/pkg/constructor"
)

func Register(app spine.App) error {
    app.Constructor(constructor.Controller(&%sSocket{}))
    return nil
}
`, name.Package, name.Type),
			filepath.Join(base, name.Package+"_handler.go"): fmt.Sprintf(`package %s

import (
    "errors"
    "github.com/NARUBROWN/spine/pkg/api"
    "github.com/NARUBROWN/spine/pkg/input"
    "github.com/NARUBROWN/spine/pkg/spine"
    "github.com/NARUBROWN/spine/pkg/ws"
)

type %sSocket struct { sender ws.Sender }

func (*%sSocket) Constructor(sender ws.Sender) *%sSocket {
    return &%sSocket{sender: sender}
}

func (s *%sSocket) RegisterRoutes(r *api.Registry) {

    r.WS(%q, func(ctx spine.WebSocketCtx, connectionID ws.ConnectionID, message input.Message[%sMessage]) error {
        // TODO: Handle the message and use s.sender.Send for replies, then remove this error.
        return errors.New("%s websocket handler is not implemented")
    })

}
`, name.Package, name.Type, name.Type, name.Type, name.Type, name.Type, registrationName, name.Type, name.Package),
			filepath.Join(base, name.Package+"_message.go"): messagePayloadTemplate(kind, name, fields),
		}
	}
	return map[string]string{
		filepath.Join(base, "module.go"):                messageModuleTemplate(kind, name, registrationName),
		filepath.Join(base, name.Package+"_handler.go"): messageHandlerTemplate(kind, name, contextType),
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
func messageContext(kind string) (string, error) {
	modules, err := selectedModules(context.Background(), ".", true)
	if err != nil {
		return "", fmt.Errorf("message context module selection: %w", err)
	}
	typeName := "EventCtx"
	if kind == "websocket" {
		typeName = "WebSocketCtx"
	}
	if modulesExportSpineType(modules, typeName) {
		return "spine." + typeName, nil
	}
	public := modulesExportSpineType(modules, "Ctx")
	version := selectedModuleVersion(modules, spineImportPath)
	if !public && semver.IsValid(version) && semver.Compare(version, "v0.7.5-preview.1") >= 0 {
		return "", fmt.Errorf("selected core %s requires pkg/spine.Ctx for message generation; check the selected module source or replace directive before regenerating", version)
	}
	if public && kind == "consumer" {
		return "spine.Ctx", nil
	}
	return "context.Context", nil
}

func modulesExportSpineType(modules []selectedModule, typeName string) bool {
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
			object := file.Scope.Lookup(typeName)
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

func messageHandlerTemplate(kind string, name resourceName, contextType string) string {
	contextImport := "context"
	if strings.HasPrefix(contextType, "spine.") {
		contextImport = spineImportPath + "/pkg/spine"
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
	// TODO: 메시지를 처리하고 필요하면 주입받은 ws.Sender로 응답한 뒤 이 오류를 제거합니다.
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
