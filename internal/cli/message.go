package cli

import (
	"fmt"
	"path/filepath"
	"strings"
)

func validateMessageFields(fields []sliceField) error {
	if len(fields) == 0 {
		return fmt.Errorf("message generation requires at least one --field name:type")
	}
	return validateSliceFields(fields)
}

func messageModuleFiles(kind string, name resourceName, registrationName string, fields []sliceField) map[string]string {
	base := filepath.Join("src", kind, name.Package)
	return map[string]string{
		filepath.Join(base, "module.go"):                messageModuleTemplate(kind, name, registrationName),
		filepath.Join(base, name.Package+"_handler.go"): messageHandlerTemplate(kind, name),
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

func messageHandlerTemplate(kind string, name resourceName) string {
	if kind == "consumer" {
		return fmt.Sprintf(`package %s

import (
	"context"
	"errors"
)

type %sConsumer struct{}

func New%sConsumer() *%sConsumer { return &%sConsumer{} }

func (c *%sConsumer) Handle(ctx context.Context, eventName string, event %sEvent) error {
	// TODO: 수신한 이벤트를 처리한 뒤 이 오류를 제거합니다.
	return errors.New("%s consumer handler is not implemented")
}
`, name.Package, name.Type, name.Type, name.Type, name.Type, name.Type, name.Type, name.Package)
	}
	return fmt.Sprintf(`package %s

import (
	"context"
	"errors"

	"github.com/NARUBROWN/spine/pkg/ws"
)

type %sSocket struct{}

func New%sSocket() *%sSocket { return &%sSocket{} }

func (s *%sSocket) Handle(ctx context.Context, connectionID ws.ConnectionID, message %sMessage) error {
	// TODO: 메시지를 처리하고 필요하면 ws.Send로 응답한 뒤 이 오류를 제거합니다.
	return errors.New("%s websocket handler is not implemented")
}
`, name.Package, name.Type, name.Type, name.Type, name.Type, name.Type, name.Type, name.Package)
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
