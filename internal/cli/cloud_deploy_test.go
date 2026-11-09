package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudDeployUpload(t *testing.T) {
	appID := "11111111-1111-4111-8111-111111111111"
	reqID := "22222222-2222-4222-8222-222222222222"
	body := []byte("example artifact")
	sum := sha256.Sum256(body)
	artifact := cloudArtifact{Binary: body, SHA256: hex.EncodeToString(sum[:]), OS: "linux", Architecture: "arm64"}
	called := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != "POST" || r.URL.Path != "/api/apps/"+appID+"/deployments" || r.Header.Get("Authorization") != "Bearer sample-token" {
			t.Error("incorrect request")
		}
		var received cloudDeployRequest
		if json.NewDecoder(r.Body).Decode(&received) != nil || received.RequestID != reqID || received.SHA256 != artifact.SHA256 || string(received.Artifact) != string(body) {
			t.Error("invalid request content")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(cloudDeployResult{ID: reqID, Status: "queued"})
	}))
	defer server.Close()
	result, err := submitCloudArtifact(context.Background(), server.Client(), server.URL, appID, reqID, "sample-token", artifact)
	if err != nil || !called || result.ID != reqID || result.Status != "queued" {
		t.Fatalf("upload failed: %v", err)
	}
	artifact.SHA256 = string(make([]byte, 64))
	called = false
	if _, err = submitCloudArtifact(context.Background(), server.Client(), server.URL, appID, reqID, "sample-token", artifact); err == nil || called {
		t.Fatal("bad checksum accepted")
	}
}

func TestCloudDeploymentWaitsForSuccess(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	count := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/apps/"+id+"/deployments" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("unexpected status request")
		}
		count++
		status := "queued"
		var publicURL *string
		if count > 1 {
			status = "succeeded"
			value := "https://example.spinecloud.dev"
			publicURL = &value
		}
		_ = json.NewEncoder(w).Encode([]cloudDeploymentStatus{{ID: id, Status: status, PublicURL: publicURL}})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := waitCloudDeployment(ctx, server.Client(), server.URL, id, id, "token", time.Millisecond)
	if err != nil || count < 2 || result.PublicURL == nil || *result.PublicURL != "https://example.spinecloud.dev" {
		t.Fatalf("status wait failed: %v", err)
	}
}
func TestCloudDeployRejectsInsecureOrigins(t *testing.T) {
	for _, value := range []string{"http://example.com", "https://user@example.com", "https://example.com/path", "https://example.com?x=1", "file:///tmp/test"} {
		if validCloudAPIOrigin(value) {
			t.Errorf("accepted %s", value)
		}
	}
	for _, value := range []string{"https://example.com", "http://127.0.0.1:8080", "http://localhost:8080"} {
		if !validCloudAPIOrigin(value) {
			t.Errorf("rejected %s", value)
		}
	}
}
func TestCloudDeployNoCredentialRedirect(t *testing.T) {
	called := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	body := []byte("artifact")
	sum := sha256.Sum256(body)
	artifact := cloudArtifact{Binary: body, SHA256: hex.EncodeToString(sum[:]), OS: "linux", Architecture: "arm64"}
	_, err := submitCloudArtifact(context.Background(), server.Client(), server.URL, "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "sample-token", artifact)
	if err == nil || called {
		t.Fatal("redirect forwarded the deployment")
	}
}

func TestCloudDeploymentCapacityRejections(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct{ reason, expected string }{
		{"insufficient cpu capacity", "host CPU usage is above 80%"},
		{"insufficient memory capacity", "insufficient instance memory capacity"},
		{"capacity measurement unavailable", "host capacity could not be measured"},
		{"other failure", "check the dashboard"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode([]cloudDeploymentStatus{{ID: id, Status: "failed", FailureMessage: &tc.reason}})
			}))
			defer server.Close()
			_, err := waitCloudDeployment(context.Background(), server.Client(), server.URL, id, id, "token", time.Millisecond)
			if err == nil || !strings.Contains(err.Error(), tc.expected) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
