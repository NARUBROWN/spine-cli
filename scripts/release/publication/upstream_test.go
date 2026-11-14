//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolvePreviewModule(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\ncase \"$1\" in\n env) printf '%s\\n' \"$GOPROXY\";;\n list) [ \"$GOPROXY\" = \"$EXPECTED_PROXY\" ] && [ \"$GOWORK\" = off ] && [ \"$GOTOOLCHAIN\" = local ] || exit 9; printf '%s\\n' \"$MODULE_OUTPUT\";;\n *) exit 8;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	const version = "v0.7.5-preview.2"
	for _, proxy := range []string{"https://proxy.golang.org,direct", "https://proxy.example.com,direct"} {
		t.Run(proxy, func(t *testing.T) {
			want := proxy
			if proxy == "https://proxy.golang.org,direct" {
				want = "direct"
			}
			t.Setenv("GOPROXY", proxy)
			t.Setenv("EXPECTED_PROXY", want)
			for _, response := range []doc{
				{"Path": core, "Version": version},
				{"Path": "example.org/other", "Version": version},
				{"Path": core, "Version": "preview"},
				{"Path": core, "Version": version, "Replace": doc{"Path": "../spine"}},
			} {
				raw, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv("MODULE_OUTPUT", string(raw))
				got, err := resolve(core, "preview")
				valid := response["Path"] == core && response["Version"] == version && response["Replace"] == nil
				if valid && (err != nil || got != version) || !valid && (err == nil || got != "") {
					t.Fatalf("%s: got %q, %v", raw, got, err)
				}
			}

			p := doc{"recipe": doc{"id": "platform-contracts-preview", "modules": doc{core: "preview"}}}
			setVersion := func(version string) {
				t.Helper()
				data, err := json.Marshal(doc{"Path": core, "Version": version})
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv("MODULE_OUTPUT", string(data))
			}

			setVersion(version)
			first, err := components(p)
			if err != nil {
				t.Fatal(err)
			}
			setVersion("v0.7.5-preview.3")
			second, err := components(p)
			if err != nil {
				t.Fatal(err)
			}
			recipe := object(first, "recipe")
			if field(recipe, "id") != "platform-contracts-preview" || field(object(recipe, "modules"), core) != version ||
				field(object(recipe, "queries"), core) != "preview" || first["recipe_sha256"] == second["recipe_sha256"] ||
				!reflect.DeepEqual(object(object(first, "recipe"), "queries"), object(object(second, "recipe"), "queries")) ||
				field(object(object(p, "recipe"), "modules"), core) != "preview" ||
				object(object(p, "recipe"), "queries") != nil {
				t.Fatalf("snapshot changed policy or lost the query: %v %v %v", first, second, p)
			}
		})
	}
}
