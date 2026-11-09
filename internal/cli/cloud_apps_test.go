package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCloudAppsListAndCreate(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/apps" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("unexpected request")
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]cloudApp{{ID: id, Name: "example"}})
		case http.MethodPost:
			var input map[string]string
			if json.NewDecoder(r.Body).Decode(&input) != nil || input["name"] != "example" {
				t.Error("invalid create request")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(cloudApp{ID: id, Name: "example"})
		default:
			t.Error("unexpected method")
		}
	}))
	defer server.Close()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		apps, err := cloudAppsRequest(context.Background(), server.Client(), server.URL, "test-token", method, "example")
		if err != nil || len(apps) != 1 || apps[0].ID != id {
			t.Fatalf("%s: %v", method, err)
		}
	}
	if _, err := cloudAppsRequest(context.Background(), server.Client(), server.URL, "test-token", http.MethodPost, "Bad Name"); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestCloudAppsRejectRedirect(t *testing.T) {
	called := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := cloudAppsRequest(context.Background(), server.Client(), server.URL, "test-token", http.MethodGet, "")
	if err == nil || called {
		t.Fatal("redirect forwarded credentials")
	}
}

func TestProjectCloudAppNameUsesNormalizedModuleBase(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "spine.toml"), []byte("module = \"example.com/My_Service.API\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	name, err := projectCloudAppName()
	if err != nil || name != "my-service-api" {
		t.Fatalf("project name: %q %v", name, err)
	}
}

func TestEnsureCloudAppReusesOrCreatesProjectApp(t *testing.T) {
	const existingID = "11111111-1111-4111-8111-111111111111"
	const createdID = "22222222-2222-4222-8222-222222222222"
	apps := []cloudApp{{ID: existingID, Name: "existing"}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/apps" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("unexpected request")
		}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(apps)
			return
		}
		var input map[string]string
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&input) != nil || input["name"] != "new-project" {
			t.Error("invalid create request")
			return
		}
		created := cloudApp{ID: createdID, Name: input["name"]}
		apps = append(apps, created)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
	}))
	defer server.Close()
	existing, created, err := ensureCloudApp(context.Background(), server.Client(), server.URL, "test-token", "existing")
	if err != nil || created || existing.ID != existingID {
		t.Fatalf("reuse: %+v %t %v", existing, created, err)
	}
	added, created, err := ensureCloudApp(context.Background(), server.Client(), server.URL, "test-token", "new-project")
	if err != nil || !created || added.ID != createdID {
		t.Fatalf("create: %+v %t %v", added, created, err)
	}
}
