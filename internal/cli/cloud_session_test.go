package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudSessionRefreshAndLogout(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	now := time.Now().UTC()
	for _, tc := range []struct {
		name          string
		expire        bool
		refreshStatus int
		wrongUser     bool
		logoutStatus  int
		failSave      bool
		wantErr       bool
	}{
		{name: "valid", logoutStatus: 204}, {name: "refresh", expire: true, refreshStatus: 200, logoutStatus: 204},
		{name: "refresh rejected", expire: true, refreshStatus: 400, wantErr: true}, {name: "refresh unavailable", expire: true, refreshStatus: 503, wantErr: true},
		{name: "refresh different account", expire: true, refreshStatus: 200, wrongUser: true, wantErr: true},
		{name: "logout unavailable", logoutStatus: 503, wantErr: true}, {name: "refresh save fails", expire: true, refreshStatus: 200, failSave: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refreshes, logouts := 0, 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("apikey") != "" {
					t.Error("unexpected API key")
				}
				switch r.URL.Path {
				case "/api/auth/session/refresh":
					refreshes++
					if r.Method != http.MethodPost || r.URL.RawQuery != "" {
						t.Error("invalid grant")
					}
					var body map[string]string
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["refresh_token"] != "old-refresh" {
						t.Error("invalid refresh token")
					}
					w.WriteHeader(tc.refreshStatus)
					if tc.refreshStatus != 200 {
						return
					}
					uid := id
					if tc.wrongUser {
						uid = "22222222-2222-4222-8222-222222222222"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_at": now.Add(time.Hour).Unix(), "user": map[string]string{"id": uid}})
				case "/api/auth/session":
					want := "Bearer old-access"
					if tc.expire {
						want = "Bearer new-access"
					}
					if r.Header.Get("Authorization") != want {
						t.Error("wrong access token")
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
				case "/api/auth/session/logout":
					logouts++
					if r.Method != http.MethodPost || r.Header.Get("Authorization") == "" {
						t.Error("logout must not revoke other sessions")
					}
					w.WriteHeader(tc.logoutStatus)
				default:
					t.Error("unexpected path")
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			expiry := now.Add(time.Hour).Unix()
			if tc.expire {
				expiry = now.Add(-time.Second).Unix()
			}
			raw, _ := json.Marshal(cloudSession{ProjectURL: srv.URL, UserID: id, AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: expiry})
			store := &memoryCloudStore{value: string(raw)}
			if tc.failSave {
				store.err = errors.New("private-store-error")
			}
			err := logoutCloudSession(context.Background(), srv.URL, srv.Client(), store)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-store-error") {
				t.Fatal("store error leaked")
			}
			if tc.wantErr && store.value == "" {
				t.Fatal("failed logout discarded credentials")
			}
			if !tc.wantErr && (store.value != "" || logouts != 1) {
				t.Fatal("successful logout did not revoke and clear")
			}
			if tc.expire && refreshes != 1 {
				t.Fatal("expired session was not refreshed")
			}
		})
	}
}

func TestCloudSessionRejectsUnknownProjectAndMissingSession(t *testing.T) {
	store := &memoryCloudStore{}
	if _, err := currentCloudSession(context.Background(), "https://example.com", http.DefaultClient, store, time.Now()); !errors.Is(err, errCloudSessionRequired) {
		t.Fatal("missing session should require login")
	}
	raw, _ := json.Marshal(cloudSession{ProjectURL: "https://other.example", UserID: "11111111-1111-4111-8111-111111111111", AccessToken: "a", RefreshToken: "b", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	store.value = string(raw)
	if _, err := currentCloudSession(context.Background(), "https://example.com", http.DefaultClient, store, time.Now()); !errors.Is(err, errCloudSessionRequired) {
		t.Fatal("session for a different project accepted")
	}
}

func TestCloudSessionLockSerializesAccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	release, err := lockCloudSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	other, err := lockCloudSession(ctx)
	if err == nil {
		other()
		release()
		t.Fatal("concurrent access acquired lock")
	}
	release()
	later, err := lockCloudSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	later()
}
