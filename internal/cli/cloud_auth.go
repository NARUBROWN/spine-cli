package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
)

var cloudKeyringService = "spine-cloud"

const cloudKeyringAccount = "session"

var cloudUserID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type cloudSession struct {
	ProjectURL   string `json:"project_url"`
	UserID       string `json:"user_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

type cloudSessionStore interface {
	Set(string, string, string) error
	Get(string, string) (string, error)
	Delete(string, string) error
}
type systemKeyring struct{}

func (systemKeyring) Set(service, account, value string) error {
	return keyring.Set(service, account, value)
}
func (systemKeyring) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}
func (systemKeyring) Delete(service, account string) error { return keyring.Delete(service, account) }

func lockCloudSession(ctx context.Context) (func(), error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, errors.New("could not locate the session lock directory")
	}
	dir := filepath.Join(base, "spine-cloud")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("could not prepare the session lock")
	}
	lock := flock.New(filepath.Join(dir, "session.lock"))
	ok, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil || !ok {
		return nil, errors.New("could not acquire the session lock")
	}
	return func() { _ = lock.Unlock() }, nil
}

func runCloud(args []string, out, errOut io.Writer) int {
	usage := "Usage: spine cloud auth login|status|logout\n\nApprove a one-time code in your browser. Your session is stored in the system credential store.\n"
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		fmt.Fprint(out, usage)
		return 0
	}
	if len(args) == 2 && args[0] == "auth" && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprint(out, usage)
		return 0
	}
	if len(args) == 3 && args[0] == "auth" && (args[1] == "login" || args[1] == "status" || args[1] == "logout") && (args[2] == "--help" || args[2] == "-h") {
		fmt.Fprint(out, usage)
		return 0
	}
	if len(args) != 2 || args[0] != "auth" || (args[1] != "login" && args[1] != "status" && args[1] != "logout") {
		fmt.Fprint(errOut, usage)
		return 1
	}
	if args[1] != "login" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		unlock, err := lockCloudSession(ctx)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		defer unlock()
		client := &http.Client{Timeout: 15 * time.Second}
		if args[1] == "status" {
			session, err := currentCloudSession(ctx, cloudDeviceAPI, client, systemKeyring{}, time.Now())
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			fmt.Fprintf(out, "Signed in to Spine Cloud as %s. Session expires at %s.\n", session.UserID, time.Unix(session.ExpiresAt, 0).UTC().Format(time.RFC3339))
			return 0
		}
		if err := logoutCloudSession(ctx, cloudDeviceAPI, client, systemKeyring{}); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		fmt.Fprintln(out, "Signed out of this Spine Cloud session.")
		return 0
	}
	return runDeviceLogin(out, errOut)
}
