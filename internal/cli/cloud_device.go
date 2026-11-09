package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"time"
)

var cloudDeviceAPI = "https://api.spinecloud.dev"
var cloudDevicePage = "https://spinecloud.dev/activate"

var cloudDeviceCode = regexp.MustCompile(`^[A-Z2-7]{4}(-[A-Z2-7]{4}){3}$`)
var cloudDeviceSecret = regexp.MustCompile(`^[0-9a-f]{64}$`)

type deviceChallenge struct {
	UserCode     string `json:"user_code"`
	DeviceSecret string `json:"device_secret"`
	ExpiresIn    int    `json:"expires_in"`
	Interval     int    `json:"interval"`
}
type deviceApproval struct {
	Status    string `json:"status"`
	OwnerID   string `json:"owner_id"`
	TokenHash string `json:"token_hash"`
}

func deviceRequest(ctx context.Context, client *http.Client, endpoint string, body any, result any, accept int) error {
	data, err := json.Marshal(body)
	if err != nil {
		return errors.New("could not prepare authorization request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid authorization endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := c.Do(req)
	if err != nil {
		return errors.New("authorization service unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != accept {
		return errors.New("authorization failed or expired; start again")
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(result) != nil {
		return errors.New("invalid authorization response")
	}
	return nil
}

func requestDeviceChallenge(ctx context.Context, client *http.Client, api string) (deviceChallenge, error) {
	var result deviceChallenge
	if !validCloudAPIOrigin(api) {
		return result, errors.New("invalid authorization endpoint")
	}
	if err := deviceRequest(ctx, client, api+"/api/auth/device/start", map[string]string{}, &result, http.StatusCreated); err != nil {
		return deviceChallenge{}, err
	}
	if !cloudDeviceCode.MatchString(result.UserCode) || !cloudDeviceSecret.MatchString(result.DeviceSecret) || result.ExpiresIn < 1 || result.ExpiresIn > 300 || result.Interval < 1 || result.Interval > 30 {
		return deviceChallenge{}, errors.New("invalid authorization response")
	}
	return result, nil
}

func exchangeDeviceApproval(ctx context.Context, client *http.Client, base string, approval deviceApproval) (cloudSession, error) {
	var result cloudSession
	if !validCloudAPIOrigin(base) || !cloudUserID.MatchString(approval.OwnerID) || approval.TokenHash == "" {
		return result, errors.New("invalid authorization response")
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    int64  `json:"expires_at"`
		User         struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := deviceRequest(ctx, client, base+"/api/auth/session/exchange", map[string]string{"code": approval.TokenHash}, &token, http.StatusOK); err != nil {
		return result, err
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresAt <= time.Now().Unix() || token.User.ID != approval.OwnerID {
		return result, errors.New("invalid authenticated session")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/auth/session", nil)
	if err != nil {
		return result, errors.New("could not verify account")
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := c.Do(req)
	if err != nil {
		return result, errors.New("could not verify account")
	}
	defer res.Body.Close()
	var user struct {
		ID string `json:"id"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&user) != nil || user.ID != approval.OwnerID {
		return result, errors.New("could not verify account")
	}
	return cloudSession{ProjectURL: base, UserID: user.ID, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: token.ExpiresAt}, nil
}

func openDeviceBrowser() {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", cloudDevicePage)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", cloudDevicePage)
	default:
		cmd = exec.Command("xdg-open", cloudDevicePage)
	}
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}

func runDeviceLogin(out, errOut io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := &http.Client{Timeout: 15 * time.Second}
	challenge, err := requestDeviceChallenge(ctx, client, cloudDeviceAPI)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "Open %s and enter this one-time code:\n\n  %s\n\nThe code expires in %d minutes.\nWaiting for approval...\n", cloudDevicePage, challenge.UserCode, challenge.ExpiresIn/60)
	openDeviceBrowser()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(challenge.ExpiresIn)*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Duration(challenge.Interval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(errOut, "authorization cancelled or expired")
			return 1
		case <-ticker.C:
			var approval deviceApproval
			if err := deviceRequest(ctx, client, cloudDeviceAPI+"/api/auth/device/poll", map[string]string{"device_secret": challenge.DeviceSecret}, &approval, http.StatusOK); err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			if approval.Status == "pending" {
				continue
			}
			if approval.Status != "approved" {
				fmt.Fprintln(errOut, "authorization cancelled or expired")
				return 1
			}
			session, err := exchangeDeviceApproval(ctx, client, cloudDeviceAPI, approval)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			unlock, err := lockCloudSession(ctx)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			encoded, err := json.Marshal(session)
			if err == nil {
				err = (systemKeyring{}).Set(cloudKeyringService, cloudKeyringAccount, string(encoded))
			}
			unlock()
			if err != nil {
				fmt.Fprintln(errOut, "could not save session to the system credential store")
				return 1
			}
			fmt.Fprintf(out, "Signed in to Spine Cloud as %s.\n", session.UserID)
			return 0
		}
	}
}

func validCloudAPIOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}
