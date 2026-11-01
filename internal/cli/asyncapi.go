package cli

import (
	"fmt"
	"strings"
	"unicode"
)

func buildAsyncAPIDocument(contract applicationContract) (map[string]any, error) {
	catalog, err := newAPISchemas(contract.Module)
	if err != nil {
		return nil, err
	}
	channels := map[string]any{}
	operations := map[string]any{}
	for _, registration := range contract.Consumers {
		if err := addAsyncAPIRegistration(catalog, channels, operations, registration, "consumer"); err != nil {
			return nil, err
		}
	}
	for _, registration := range contract.WebSockets {
		if err := addAsyncAPIRegistration(catalog, channels, operations, registration, "websocket"); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"asyncapi": "3.1.0",
		"info": map[string]any{
			"title":       contract.Module,
			"version":     "generated",
			"description": "Generated from the Spine static execution contract. Broker protocol selection, delivery guarantees, and dynamic registration are not inferred.",
		},
		"defaultContentType": "application/json",
		"channels":           channels,
		"operations":         operations,
		"components":         map[string]any{"schemas": catalog.schemas},
		"x-spine-contract":   map[string]any{"schema_version": contract.SchemaVersion, "analysis_mode": contract.AnalysisMode},
	}, nil
}

func addAsyncAPIRegistration(catalog *apiSchemas, channels, operations map[string]any, registration namedRegistration, transport string) error {
	channelID := stableDocumentID(asyncAPIIdentifier(transport+"_"+registration.Name), transport, registration.Name)
	if _, exists := channels[channelID]; exists {
		return fmt.Errorf("AsyncAPI channel identifier collision: %s", channelID)
	}
	messageID := channelID + "Message"
	payloadType := messagePayloadType(registration.Parameters)
	channel := map[string]any{
		"address": registration.Name,
		"messages": map[string]any{
			messageID: map[string]any{
				"name":            messageID,
				"payload":         catalog.schema(payloadType, registration.schemaSource),
				"x-spine-go-type": payloadType,
				"x-spine-handler": registration.Handler,
				"x-spine-source":  registration.Source,
			},
		},
		"x-spine-transport": transport,
	}
	channels[channelID] = channel
	operationID := stableDocumentID(asyncAPIIdentifier("receive_"+registration.Handler+"_"+registration.Name), transport, registration.Handler, registration.Name)
	if _, exists := operations[operationID]; exists {
		return fmt.Errorf("AsyncAPI operation identifier collision: %s", operationID)
	}
	operations[operationID] = map[string]any{
		"action":                     "receive",
		"channel":                    map[string]any{"$ref": "#/channels/" + channelID},
		"messages":                   []any{map[string]any{"$ref": "#/channels/" + channelID + "/messages/" + messageID}},
		"x-spine-handler-parameters": registration.Parameters,
		"x-spine-return-types":       registration.Returns,
	}
	return nil
}

func messagePayloadType(parameters []handlerParameter) string {
	for _, parameter := range parameters {
		if parameter.Binding == "payload" {
			return parameter.Type
		}
	}
	return "unresolved"
}

func asyncAPIIdentifier(value string) string {
	var result strings.Builder
	underscore := false
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			result.WriteRune(character)
			underscore = false
		} else if result.Len() > 0 && !underscore {
			result.WriteByte('_')
			underscore = true
		}
	}
	identifier := strings.Trim(result.String(), "_")
	if identifier == "" {
		return "channel"
	}
	if first := []rune(identifier)[0]; unicode.IsDigit(first) {
		return fmt.Sprintf("channel_%s", identifier)
	}
	return identifier
}
