package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/zalando/go-keyring"
)

var errCloudSessionRequired = errors.New("no valid Spine Cloud session; run spine cloud auth login")

func currentCloudSession(ctx context.Context, base string, httpClient *http.Client, store cloudSessionStore, now time.Time) (cloudSession, error) {
	var empty cloudSession
	raw, err := store.Get(cloudKeyringService, cloudKeyringAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return empty, errCloudSessionRequired
	}
	if err != nil {
		return empty, errors.New("could not read the system credential store")
	}
	var saved cloudSession
	if json.Unmarshal([]byte(raw), &saved) != nil || saved.ProjectURL != base || !cloudUserID.MatchString(saved.UserID) || saved.AccessToken == "" || saved.RefreshToken == "" || saved.ExpiresAt <= 0 {
		return empty, errCloudSessionRequired
	}
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if saved.ExpiresAt <= now.Add(time.Minute).Unix() {
		data, _ := json.Marshal(map[string]string{"refresh_token": saved.RefreshToken})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/auth/session/refresh", bytes.NewReader(data))
		if err != nil {
			return empty, errors.New("authentication service unavailable")
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return empty, errors.New("authentication service unavailable")
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			return empty, errCloudSessionRequired
		}
		if res.StatusCode != http.StatusOK {
			return empty, errors.New("authentication service unavailable; try again later")
		}
		var token struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresAt    int64  `json:"expires_at"`
			User         struct {
				ID string `json:"id"`
			} `json:"user"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(&token) != nil || token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresAt <= now.Unix() || token.User.ID != saved.UserID {
			return empty, errors.New("authentication service returned an invalid session")
		}
		saved.AccessToken = token.AccessToken
		saved.RefreshToken = token.RefreshToken
		saved.ExpiresAt = token.ExpiresAt
		encoded, err := json.Marshal(saved)
		if err != nil {
			return empty, errors.New("could not prepare session")
		}
		if store.Set(cloudKeyringService, cloudKeyringAccount, string(encoded)) != nil {
			return empty, errors.New("could not save the refreshed session to the system credential store; sign in again")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/auth/session", nil)
	if err != nil {
		return empty, errors.New("authentication service unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+saved.AccessToken)
	res, err := client.Do(req)
	if err != nil {
		return empty, errors.New("authentication service unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return empty, errCloudSessionRequired
	}
	if res.StatusCode != http.StatusOK {
		return empty, errors.New("authentication service unavailable; try again later")
	}
	var user struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&user) != nil || user.ID != saved.UserID {
		return empty, errors.New("could not verify the authenticated account")
	}
	return saved, nil
}

func logoutCloudSession(ctx context.Context, base string, httpClient *http.Client, store cloudSessionStore) error {
	session, err := currentCloudSession(ctx, base, httpClient, store, time.Now())
	if err != nil {
		return err
	}
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/auth/session/logout", nil)
	if err != nil {
		return errors.New("authentication service unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+session.AccessToken)
	res, err := client.Do(req)
	if err != nil {
		return errors.New("could not revoke session; try again")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("could not revoke session; try again")
	}
	if store.Delete(cloudKeyringService, cloudKeyringAccount) != nil {
		return errors.New("session was revoked but could not be removed from the system credential store")
	}
	return nil
}
