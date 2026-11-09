package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudAppConflictMessages(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		conflict         bool
	}{
		{"limit", `{"code":"INSTANCE_LIMIT","message":"계정당 앱은 두 개까지입니다.","error":"instance limit"}`, "계정당 앱은 두 개까지입니다.", false},
		{"legacy limit", `{"error":"instance limit reached"}`, "instance limit reached", false},
		{"name collision", `{"code":"APP_NAME_EXISTS","message":"name taken"}`, "name taken", true},
		{"legacy collision", `{"error":"app name already exists"}`, "app name already exists", true},
		{"unknown conflict", `{}`, "could not load or create an app", false},
		{"terminal control", `{"message":"bad\u001b[2J"}`, "could not load or create an app", false},
		{"oversized", `{"message":"` + strings.Repeat("x", maxCloudErrorBytes) + `"}`, "could not load or create an app", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			_, err := cloudAppsRequest(context.Background(), server.Client(), server.URL, "token", http.MethodPost, "example")
			if err == nil || err.Error() != tc.want || errors.Is(err, errCloudAppNameExists) != tc.conflict {
				t.Fatalf("error = %v, name conflict = %t", err, errors.Is(err, errCloudAppNameExists))
			}
		})
	}
}

func TestCloudDeploymentUsesServerMessage(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]cloudDeploymentStatus{{ID: id, Status: "failed", ErrorCode: "NEW_FAILURE", ErrorMessage: "새 오류를 확인하세요."}})
	}))
	defer server.Close()
	_, err := waitCloudDeployment(context.Background(), server.Client(), server.URL, id, id, "token", time.Millisecond)
	if err == nil || err.Error() != "새 오류를 확인하세요." {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloudAppLimitDoesNotRetryAsNameCollision(t *testing.T) {
	gets := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
			_, _ = fmt.Fprint(w, `[]`)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"code":"INSTANCE_LIMIT","message":"instance limit reached"}`)
	}))
	defer server.Close()
	_, _, err := ensureCloudApp(context.Background(), server.Client(), server.URL, "token", "example")
	if err == nil || err.Error() != "instance limit reached" || gets != 1 {
		t.Fatalf("error = %v, list requests = %d", err, gets)
	}
}

func TestCloudAppNameRaceFindsCreatedApp(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	gets := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
			if gets == 1 {
				_, _ = fmt.Fprint(w, `[]`)
			} else {
				_ = json.NewEncoder(w).Encode([]cloudApp{{ID: id, Name: "example"}})
			}
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"code":"APP_NAME_EXISTS","message":"app name already exists"}`)
	}))
	defer server.Close()
	app, created, err := ensureCloudApp(context.Background(), server.Client(), server.URL, "token", "example")
	if err != nil || created || app.ID != id || gets != 2 {
		t.Fatalf("app = %+v, created = %t, error = %v, lists = %d", app, created, err, gets)
	}
}

func TestCloudDeployShowsSubmissionError(t *testing.T) {
	body := []byte("artifact")
	sum := sha256.Sum256(body)
	artifact := cloudArtifact{Binary: body, SHA256: hex.EncodeToString(sum[:]), OS: "linux", Architecture: "arm64"}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"code":"NEW_CONFLICT","message":"새 배포 오류입니다."}`)
	}))
	defer server.Close()
	const id = "11111111-1111-4111-8111-111111111111"
	_, err := submitCloudArtifact(context.Background(), server.Client(), server.URL, id, id, "token", artifact)
	if err == nil || err.Error() != "새 배포 오류입니다." {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloudAppConflictPreservesServerMessage(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `[]`)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"code":"APP_NAME_EXISTS","message":"앱 이름이 사용 중입니다."}`)
	}))
	defer server.Close()
	_, _, err := ensureCloudApp(context.Background(), server.Client(), server.URL, "token", "example")
	if err == nil || err.Error() != "앱 이름이 사용 중입니다." || !errors.Is(err, errCloudAppNameExists) {
		t.Fatalf("unexpected error: %v", err)
	}
}
