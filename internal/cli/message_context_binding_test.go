package cli

import "testing"

func TestMessageBindingsRecognizeSelectedSpineContextImportAliases(t *testing.T) {
	for _, tc := range []struct {
		name, typ, alias, path, want string
	}{
		{"default", "spine.Ctx", "spine", spineImportPath + "/pkg/spine", "context"},
		{"renamed_import", "handlercontext.Ctx", "handlercontext", spineImportPath + "/pkg/spine", "context"},
		{"unrelated_package", "spine.Ctx", "spine", "example.com/application/spine", "payload"},
		{"unrelated_context", "handlercontext.Ctx", "handlercontext", "example.com/application/context", "payload"},
		{"older_standard_context", "context.Context", "context", "context", "context"},
	} {
		for _, transport := range []string{"consumer", "websocket"} {
			t.Run(tc.name+"/"+transport, func(t *testing.T) {
				parameters := []handlerParameter{{Name: "ctx", Type: tc.typ}, {Name: "message", Type: "Message"}}
				bound := bindMessageParameters(parameters, transport, map[string]string{tc.alias: tc.path})
				if bound[0].Binding != tc.want || bound[1].Binding != "payload" || parameters[0].Binding != "" {
					t.Fatalf("메시지 컨텍스트와 payload를 구분하고 원본은 보존해야 합니다: %+v", bound)
				}
			})
		}
	}
}
