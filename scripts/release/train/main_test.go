package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasegithub"
)

type fixtureRepo struct {
	api   func(string, string, any) (any, error)
	tag   string
	proof *releasegithub.Evidence
}

func (r *fixtureRepo) API(method, path string, input, output any, _ bool) (bool, error) {
	value, err := r.api(method, path, input)
	if err != nil || output == nil {
		return err == nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(encoded, output)
}
func (r *fixtureRepo) TagSHA(string) (string, error) { return r.tag, nil }
func (r *fixtureRepo) CandidateEvidence(string, string, bool) (*releasegithub.Evidence, error) {
	return r.proof, nil
}

func TestTrainValidatesBeforePublishingAndStopsOnFailure(t *testing.T) {
	sha := strings.Repeat("a", 40)
	plan := make(map[string]candidate)
	for _, module := range order {
		plan[module] = candidate{"v0.7.0-preview.2", sha}
	}
	for _, scenario := range []string{"success", "missing", "wrong core", "changed head", "sequence", "queued", "dispatch error"} {
		t.Run(scenario, func(t *testing.T) {
			data, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := loadPlan(data)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "missing" {
				delete(loaded, order[1])
				data, err = json.Marshal(loaded)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := loadPlan(data); err == nil {
					t.Fatal("missing adapter accepted")
				}
				return
			}
			open := func(module string) (repository, error) {
				return &fixtureRepo{api: func(_, path string, _ any) (any, error) {
					switch {
					case path == "git/ref/heads/preview":
						head := sha
						if scenario == "changed head" && module == order[1] {
							head = strings.Repeat("b", 40)
						}
						return map[string]any{"object": map[string]string{"sha": head}}, nil
					case strings.HasPrefix(path, "contents/"):
						coreSHA := sha
						if scenario == "wrong core" && module == order[1] {
							coreSHA = strings.Repeat("b", 40)
						}
						policy := `{"next_version":"v0.7.0","ci_core":"` + coreSHA + `","channels":{"preview":{"core_version":"v0.7.0-preview.2"}}}`
						return map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(policy))}, nil
					case strings.HasPrefix(path, "git/matching-refs/"):
						if scenario == "sequence" {
							return []map[string]string{{"ref": "refs/tags/v0.7.0-preview.3"}}, nil
						}
						return []any{}, nil
					case path == runsPath:
						if scenario == "queued" {
							return map[string]any{"workflow_runs": []workflowRun{{Status: "queued"}}}, nil
						}
						return map[string]any{"workflow_runs": []any{}}, nil
					default:
						return nil, errors.New("unexpected request: " + path)
					}
				}}, nil
			}
			var calls []string
			publish := func(_ repository, module string, _ candidate, coreVersion string) error {
				if coreVersion != plan[core].Version {
					t.Errorf("core version %q", coreVersion)
				}
				calls = append(calls, module)
				if scenario == "dispatch error" && module == order[1] {
					return errors.New("publish failed")
				}
				return nil
			}
			err = runTrain(loaded, true, open, publish, io.Discard)
			switch scenario {
			case "success":
				if err != nil || !reflect.DeepEqual(calls, order) {
					t.Fatalf("%v: %v", calls, err)
				}
				var report bytes.Buffer
				if err := runTrain(loaded, false, open, publish, &report); err != nil {
					t.Fatal(err)
				}
				var expected strings.Builder
				for _, module := range order {
					item := plan[module]
					fmt.Fprintf(&expected, "%s: %s %s\n", module, item.Version, item.SHA)
				}
				if report.String() != expected.String() {
					t.Fatalf("dry-run output %q, want %q", report.String(), expected.String())
				}
				if !reflect.DeepEqual(calls, order) {
					t.Fatalf("dry run dispatched releases: %v", calls)
				}

			case "dispatch error":
				if err == nil || !reflect.DeepEqual(calls, order[:2]) {
					t.Fatalf("%v: %v", calls, err)
				}
			default:
				if err == nil || len(calls) != 0 {
					t.Fatalf("%v: %v", calls, err)
				}
			}
		})
	}
}

func TestDispatchWaitsForNewRunAndTag(t *testing.T) {
	item := candidate{"v0.7.0-preview.2", strings.Repeat("a", 40)}
	repo := &fixtureRepo{}
	var requests []string
	var submitted bool
	repo.api = func(method, path string, input any) (any, error) {
		requests = append(requests, method+" "+path)
		switch path {
		case "git/ref/heads/preview":
			return map[string]any{"object": map[string]string{"sha": item.SHA}}, nil
		case runsPath:
			list := []workflowRun{{ID: 41, Status: "completed", SHA: item.SHA}}
			if submitted {
				list = append(list, workflowRun{ID: 42, Status: "in_progress", SHA: item.SHA})
			}
			// workflow_run input uses GitHub field names.
			rows := make([]map[string]any, 0, len(list))
			for _, run := range list {
				rows = append(rows, map[string]any{"id": run.ID, "status": run.Status, "head_sha": run.SHA})
			}
			return map[string]any{"workflow_runs": rows}, nil
		case "actions/workflows/preview.yml/dispatches":
			if method != "POST" || input == nil {
				t.Error("missing dispatch inputs")
			}
			submitted = true
			return nil, nil
		case "actions/runs/42":
			repo.tag = item.SHA
			return map[string]string{"status": "completed", "conclusion": "success"}, nil
		default:
			return nil, errors.New("unexpected request")
		}
	}
	var output bytes.Buffer
	now := time.Unix(0, 0)
	err := dispatch(repo, order[1], item, item.Version, func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) }, &output)
	if err != nil || !strings.Contains(output.String(), "발행 실행 42") || requests[len(requests)-1] != "GET actions/runs/42" {
		t.Fatalf("%v, %s, %v", err, output.String(), requests)
	}
}

func TestExistingCandidateEvidence(t *testing.T) {
	sha := strings.Repeat("a", 40)
	tree := strings.Repeat("b", 40)
	for _, module := range []string{core, order[1], cli} {
		t.Run(module, func(t *testing.T) {
			item := candidate{"v0.7.0-preview.2", sha}
			scope, schema, pinned := "platform", "spine.release-manifest/v1", "v0.7.0-preview.1"
			if module == core {
				scope, pinned = "core", item.Version
			}
			if module == cli {
				schema = "spine-cli.release-manifest/v1"
			}
			comps := map[string]string{"core_version": pinned}
			payload := map[string]any{
				"schema_version": schema, "repository": strings.TrimPrefix(module, "github.com/"), "module": module, "kind": scope, "source_tree": tree,
				"version": item.Version, "source_sha": sha, "channel": "preview", "branch": "preview", "workflow_run_id": "10", "workflow_run_attempt": "1", "components": comps,
				"validation": map[string]any{"result": "passed", "scope": scope, "version": item.Version, "source_sha": sha, "components": comps,
					"selected_modules": map[string]string{core: pinned, module: item.Version}},
			}
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			proof := &releasegithub.Evidence{Manifest: data, WorkflowRun: []byte(`{"id":10,"run_attempt":1,"status":"completed","conclusion":"success","event":"workflow_dispatch","head_sha":"` + sha + `","head_branch":"preview","path":".github/workflows/preview.yml"}`)}
			proof.Artifact.WorkflowRun.ID, proof.Artifact.WorkflowRun.HeadSHA = 10, sha
			repo := &fixtureRepo{proof: proof, api: func(_, path string, _ any) (any, error) {
				if path != "git/commits/"+sha {
					return nil, errors.New("unexpected API call: " + path)
				}
				return map[string]any{"tree": map[string]string{"sha": tree}}, nil
			}}
			if err := checkExisting(repo, module, item, item.Version); err != nil {
				t.Fatal(err)
			}
			proof.WorkflowRun = bytes.Replace(proof.WorkflowRun, []byte("success"), []byte("failure"), 1)
			if err := checkExisting(repo, module, item, item.Version); err == nil {
				t.Fatal("failed workflow was accepted")
			}
			repo.proof = nil
			if err := checkExisting(repo, module, item, item.Version); (err == nil) != (module != core && module != cli) {
				t.Fatalf("missing proof: %v", err)
			}
		})
	}
}

func TestDispatchRefusesActiveRun(t *testing.T) {
	item := candidate{"v0.7.0-preview.2", strings.Repeat("a", 40)}
	repo := &fixtureRepo{api: func(method, path string, _ any) (any, error) {
		if method != "GET" {
			t.Fatal("unexpected dispatch")
		}
		if path == runsPath {
			return map[string]any{"workflow_runs": []map[string]string{{"status": "in_progress"}}}, nil
		}
		return map[string]any{"object": map[string]string{"sha": item.SHA}}, nil
	}}
	if err := dispatch(repo, core, item, item.Version, time.Now, time.Sleep, io.Discard); err == nil || !strings.Contains(err.Error(), "진행 중인") {
		t.Fatalf("%v", err)
	}
}
