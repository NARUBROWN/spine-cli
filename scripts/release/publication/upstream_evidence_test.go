package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasegithub"
)

type upstreamFixture struct {
	reuseFixture
	prefix   string
	optional bool
}

func (f *upstreamFixture) CandidateEvidence(version, prefix string, optional bool) (*releasegithub.Evidence, error) {
	if prefix != f.prefix || optional != f.optional {
		return nil, fmt.Errorf("unexpected artifact request for %s: %s, optional=%t", version, prefix, optional)
	}
	return f.evidence, nil
}

func TestCollectUpstreamEvidence(t *testing.T) {
	const nats = core + "-nats-platform"
	const kafka = core + "-kafka-platform"
	const echo = core + "-echo-platform"
	const coreTag = "v0.7.0-preview.8"
	const platformTag = "v0.2.0-preview.1"
	source := strings.Repeat("a", 40)
	for _, tc := range []struct{ name, failure string }{
		{"all platforms", ""}, {"unpinned preview commit", ""}, {"old core", ""},
		{"independent platform", ""}, {"missing core selection", "코어 검증 기록"},
		{"failed workflow", "공개 검증 기록"}, {"mismatched core selection", "코어 검증 기록"},
		{"recipe drift", "레시피"}, {"expired adapter", ""},
		{"readme only", ""}, {"core tooling only", ""}, {"echo tooling only", ""},
		{"other core tooling", "레시피"}, {"other echo tooling", "레시피"},
		{"source changed", "레시피"}, {"head moved", "레시피"}, {"diverged", "레시피"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clients := make(map[string]*upstreamFixture)
			resolved := make(map[string]string)
			for i, module := range modules {
				repository := strings.TrimPrefix(module, "github.com/")
				tag, scope, prefix := platformTag, "platform", "release-manifest-"
				if module == core {
					tag, scope, prefix = coreTag, "core", "core-candidate-"
				}
				components := doc{"core_version": coreTag, "modules": doc{}}
				selected := doc{core: coreTag, module: tag}
				conclusion := "success"
				switch {
				case module == nats && tc.name == "old core":
					components["core_version"], selected[core] = "v0.7.0-preview.2", "v0.7.0-preview.2"
				case module == nats && (tc.name == "independent platform" || tc.name == "missing core selection"):
					delete(components, "core_version")
					if tc.name == "missing core selection" {
						delete(selected, core)
					}
				case module == echo && tc.name == "failed workflow":
					conclusion = "failure"
				case module == echo && tc.name == "mismatched core selection":
					selected[core] = "v0.7.0-preview.2"
				}
				proof := &releasegithub.Evidence{}
				proof.Artifact.ID, proof.Artifact.Digest = int64(i+1), "sha256:fixture"
				proof.Artifact.WorkflowRun.ID, proof.Artifact.WorkflowRun.HeadSHA = 1, source
				var err error
				proof.Manifest, err = json.Marshal(doc{"schema_version": "spine.release-manifest/v1", "repository": repository,
					"module": module, "kind": scope, "channel": "preview", "branch": "preview", "version": tag,
					"source_sha": source, "source_tree": "tree", "workflow_run_id": "1", "workflow_run_attempt": "1", "components": components,
					"validation": doc{"result": "passed", "scope": scope, "version": tag, "source_sha": source, "components": components, "selected_modules": selected}})
				if err != nil {
					t.Fatal(err)
				}
				proof.WorkflowRun = []byte(fmt.Sprintf(`{"id":1,"run_attempt":1,"status":"completed","conclusion":%q,"event":"workflow_dispatch","head_branch":"preview","head_sha":%q,"path":".github/workflows/preview.yml"}`, conclusion, source))
				if module == nats && tc.name == "expired adapter" {
					proof = nil
				}
				clients[repository] = &upstreamFixture{prefix: prefix, optional: module != core, reuseFixture: reuseFixture{source: source, evidence: proof, entries: map[string]any{
					"git/matching-refs/tags/v": []doc{{"ref": "refs/tags/" + tag}},
					"git/commits/" + source:    doc{"tree": doc{"sha": "tree"}},
				}}}
				resolved[module] = tag
			}
			recipe := doc{"modules": doc{core: coreTag, echo: platformTag}}
			if tc.name == "recipe drift" {
				object(recipe, "modules")[core] = "v0.7.0-preview.7"
			}
			if tc.name == "unpinned preview commit" {
				resolved[kafka] = platformTag + ".0.20261108124442-0628cf02bee1"
			}
			selectedModule := core
			head := strings.Repeat("c", 40)
			headSHA := head
			files := []string{"README.zh-Hans.md"}
			status, mergeBase := "ahead", source
			switch tc.name {
			case "core tooling only", "other core tooling":
				files = []string{"scripts/release/release.py", "scripts/release/test_release.py", "scripts/release/verify_public.py"}
				if tc.name == "other core tooling" {
					files = append(files, "scripts/release/other.py")
				}
			case "echo tooling only", "other echo tooling":
				selectedModule = echo
				files = []string{"scripts/release/release.py", "scripts/release/verify_public.py", "test/release/test_release.py"}
				if tc.name == "other echo tooling" {
					files = append(files, "scripts/release/test_release.py")
				}
			case "source changed":
				files = append(files, "go.mod")
			case "head moved":
				headSHA = strings.Repeat("d", 40)
			case "diverged":
				status, mergeBase = "diverged", strings.Repeat("d", 40)
			case "readme only":
			default:
				selectedModule = ""
			}
			if selectedModule != "" {
				selected := resolved[selectedModule] + ".0.20261108124442-" + head[:12]
				resolved[selectedModule] = selected
				object(recipe, "modules")[selectedModule] = selected
				fixture := clients[strings.TrimPrefix(selectedModule, "github.com/")]
				fixture.entries["git/ref/heads/preview"] = doc{"object": doc{"sha": headSHA}}
				changes := make([]doc, 0, len(files))
				for _, path := range files {
					changes = append(changes, doc{"status": "modified", "filename": path})
				}
				fixture.entries["compare/"+source+"..."+headSHA] = doc{"status": status, "merge_base_commit": doc{"sha": mergeBase}, "files": changes}
			}

			got, err := collectUpstreamEvidence(recipe, func(module, query string) (string, error) {
				if query != "preview" || resolved[module] == "" {
					t.Fatalf("unexpected query %s@%s", module, query)
				}
				return resolved[module], nil
			}, func(repository string) (upstreamClient, error) {
				if clients[repository] == nil {
					t.Fatalf("unexpected repository %s", repository)
				}
				return clients[repository], nil
			})
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) || got != nil {
					t.Fatalf("wanted %q, got %v, %v", tc.failure, got, err)
				}
				return
			}
			if err != nil || len(got) != len(modules) {
				t.Fatalf("snapshots %v, error %v", got, err)
			}
			for _, module := range modules {
				item := object(got, module)
				tag := platformTag
				if module == core {
					tag = coreTag
				}
				if item["source_sha"] != source || item["version"] != tag || item["selected_version"] != resolved[module] {
					t.Fatalf("%s: %v", module, item)
				}
				if module == selectedModule && item["selected_source_sha"] != head {
					t.Fatalf("selected commit not recorded: %v", item)
				}

				if module == nats && tc.name == "expired adapter" {
					if item["artifact_id"] != nil || item["digest"] != nil {
						t.Fatal(item)
					}
				} else if item["artifact_id"] == nil || item["digest"] != "sha256:fixture" {
					t.Fatal(item)
				}
			}
		})
	}
}
