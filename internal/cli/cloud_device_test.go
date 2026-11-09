package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeviceExchangeVerifiesOwner(t *testing.T) {
	const owner = "11111111-1111-4111-8111-111111111111"
	for _, mismatch := range []bool{false, true} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("apikey") != "" {
				t.Error("unexpected API key")
			}
			switch r.URL.Path {
			case "/api/auth/session/exchange":
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				if len(body) != 1 || body["code"] != "one-time-token" {
					t.Error("bad verification request")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_at": time.Now().Add(time.Hour).Unix(), "user": map[string]string{"id": owner}})
			case "/api/auth/session":
				if r.Header.Get("Authorization") != "Bearer access" {
					t.Error("missing access token")
				}
				id := owner
				if mismatch {
					id = "22222222-2222-4222-8222-222222222222"
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		session, err := exchangeDeviceApproval(context.Background(), server.Client(), server.URL, deviceApproval{Status: "approved", OwnerID: owner, TokenHash: "one-time-token"})
		server.Close()
		if mismatch && err == nil {
			t.Fatal("accepted mismatched user")
		}
		if !mismatch && (err != nil || session.UserID != owner || session.RefreshToken != "refresh") {
			t.Fatalf("exchange failed: %v", err)
		}
	}
}

func TestDeviceRequestRejectsRedirect(t *testing.T) {
	called := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	var result deviceApproval
	err := deviceRequest(context.Background(), server.Client(), server.URL, map[string]string{"device_secret": strings.Repeat("a", 64)}, &result, http.StatusOK)
	if err == nil || called {
		t.Fatal("redirect forwarded the device secret")
	}
}

func TestDeviceChallengeValidation(t *testing.T) {
	for _, tc := range []struct {
		code, secret string
		ok           bool
	}{
		{"ABCD-EFGH-JKLM-NPQR", strings.Repeat("a", 64), true},
		{"SHORT", strings.Repeat("a", 64), false},
		{"ABCD-EFGH-JKLM-NPQR", "short", false},
	} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(deviceChallenge{UserCode: tc.code, DeviceSecret: tc.secret, ExpiresIn: 300, Interval: 3})
		}))
		_, err := requestDeviceChallenge(context.Background(), server.Client(), server.URL)
		server.Close()
		if (err == nil) != tc.ok {
			t.Fatalf("challenge validation: %v", err)
		}
	}
}
