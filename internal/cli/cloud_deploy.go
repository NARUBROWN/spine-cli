package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type cloudDeployRequest struct {
	RequestID    string `json:"request_id"`
	SHA256       string `json:"sha256"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Artifact     []byte `json:"artifact"`
}
type cloudDeployResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type cloudDeploymentStatus struct {
	ID        string  `json:"id"`
	Status    string  `json:"status"`
	PublicURL *string `json:"public_url"`
}

func waitCloudDeployment(ctx context.Context, client *http.Client, api, appID, deploymentID, token string, interval time.Duration) (cloudDeploymentStatus, error) {
	var empty cloudDeploymentStatus
	if !validCloudAPIOrigin(api) || !cloudUserID.MatchString(appID) || !cloudUserID.MatchString(deploymentID) || token == "" || interval <= 0 {
		return empty, errors.New("invalid deployment status request")
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/api/apps/"+appID+"/deployments", nil)
		if err != nil {
			return empty, errors.New("invalid deployment status request")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := c.Do(req)
		if err != nil {
			return empty, errors.New("could not check deployment status; check the dashboard")
		}
		var items []cloudDeploymentStatus
		err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&items)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || err != nil {
			return empty, errors.New("could not check deployment status; check the dashboard")
		}
		for _, item := range items {
			if !strings.EqualFold(item.ID, deploymentID) {
				continue
			}
			switch item.Status {
			case "succeeded":
				if item.PublicURL == nil {
					return empty, errors.New("deployment succeeded without a public address; check the dashboard")
				}
				u, err := url.Parse(*item.PublicURL)
				if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
					return empty, errors.New("deployment returned an invalid public address")
				}
				return item, nil
			case "failed":
				return empty, errors.New("deployment failed; check the dashboard for details")
			case "uploading", "queued", "starting", "healthchecking", "publishing":
			default:
				return empty, errors.New("invalid deployment status")
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return empty, errors.New("deployment is still processing or status is unavailable; check the dashboard")
		case <-timer.C:
		}
	}
}

func newCloudRequestID() (string, error) {
	var data [16]byte
	if _, err := io.ReadFull(rand.Reader, data[:]); err != nil {
		return "", err
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	s := hex.EncodeToString(data[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

// submitCloudArtifact는 서버에 검증된 결과물을 전송하며 실행 완료를 추정하지 않는다.
func submitCloudArtifact(ctx context.Context, client *http.Client, api, appID, requestID, accessToken string, artifact cloudArtifact) (cloudDeployResult, error) {
	var empty cloudDeployResult
	if !validCloudAPIOrigin(api) || !cloudUserID.MatchString(appID) || !cloudUserID.MatchString(requestID) || accessToken == "" || len(artifact.Binary) == 0 || int64(len(artifact.Binary)) > maxCloudArtifactBytes || artifact.OS != "linux" || artifact.Architecture != "arm64" || len(artifact.SHA256) != 64 {
		return empty, errors.New("invalid deployment request")
	}
	sum := sha256.Sum256(artifact.Binary)
	if hex.EncodeToString(sum[:]) != artifact.SHA256 {
		return empty, errors.New("invalid deployment artifact checksum")
	}
	body, err := json.Marshal(cloudDeployRequest{RequestID: requestID, SHA256: artifact.SHA256, OS: artifact.OS, Architecture: artifact.Architecture, Artifact: artifact.Binary})
	if err != nil {
		return empty, errors.New("could not prepare deployment request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api+"/api/apps/"+appID+"/deployments", bytes.NewReader(body))
	if err != nil {
		return empty, errors.New("invalid deployment endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := c.Do(req)
	if err != nil {
		return empty, errors.New("deployment request failed; retry using the same request ID")
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case 401:
		return empty, errors.New("authentication failed; run spine cloud auth login")
	case 403, 404:
		return empty, errors.New("app not found or deployment service unavailable")
	case 409:
		return empty, errors.New("request ID conflicts with an existing deployment")
	case 413:
		return empty, errors.New("deployment artifact exceeds the server limit")
	}
	if res.StatusCode != http.StatusAccepted && res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		return empty, errors.New("deployment service unavailable; retry using the same request ID")
	}
	var result cloudDeployResult
	if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&result) != nil || !cloudUserID.MatchString(result.ID) || (result.Status != "uploading" && result.Status != "queued" && result.Status != "starting" && result.Status != "healthchecking" && result.Status != "publishing" && result.Status != "succeeded" && result.Status != "failed") {
		return empty, errors.New("deployment service returned an invalid response")
	}
	return result, nil
}

func runDeploy(args []string, out, errOut io.Writer) int {
	usage := "Usage: spine deploy [--app <app-id>] [--api <https-origin>] [--request-id <uuid>]\n\nSign in, prepare the current project's app, and submit a Linux ARM64 artifact.\n"
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(out, usage)
		return 0
	}
	appID, api, requestID := "", strings.TrimRight(os.Getenv("SPINE_CLOUD_API_URL"), "/"), ""
	if api == "" {
		api = "https://api.spinecloud.dev"
	}
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			fmt.Fprint(errOut, usage)
			return 1
		}
		switch args[i] {
		case "--app":
			if appID != "" {
				fmt.Fprintln(errOut, "--app may only be specified once")
				return 1
			}
			appID = args[i+1]
		case "--api":
			api = strings.TrimRight(args[i+1], "/")
		case "--request-id":
			requestID = args[i+1]
		default:
			fmt.Fprint(errOut, usage)
			return 1
		}
		i++
	}
	if (appID != "" && !cloudUserID.MatchString(appID)) || !validCloudAPIOrigin(api) {
		fmt.Fprint(errOut, usage)
		return 1
	}
	if requestID == "" {
		var err error
		requestID, err = newCloudRequestID()
		if err != nil {
			fmt.Fprintln(errOut, "could not create deployment request ID")
			return 1
		}
	} else if !cloudUserID.MatchString(requestID) {
		fmt.Fprintln(errOut, "invalid request ID")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	sessionCtx, sessionCancel := context.WithTimeout(ctx, 30*time.Second)
	defer sessionCancel()
	unlock, err := lockCloudSession(sessionCtx)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	client := &http.Client{Timeout: 30 * time.Second}
	session, err := currentCloudSession(sessionCtx, cloudDeviceAPI, client, systemKeyring{}, time.Now())
	unlock()
	if errors.Is(err, errCloudSessionRequired) {
		fmt.Fprintln(out, "Sign in to Spine Cloud to continue.")
		if runCloud([]string{"auth", "login"}, out, errOut) != 0 {
			return 1
		}
		sessionCancel()
		sessionCtx, sessionCancel = context.WithTimeout(ctx, 30*time.Second)
		defer sessionCancel()
		unlock, err = lockCloudSession(sessionCtx)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		session, err = currentCloudSession(sessionCtx, cloudDeviceAPI, client, systemKeyring{}, time.Now())
		unlock()
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	artifact, err := buildCloudArtifact(ctx, out, errOut)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if appID == "" {
		name, nameErr := projectCloudAppName()
		if nameErr != nil {
			fmt.Fprintln(errOut, nameErr)
			return 1
		}
		app, created, appErr := ensureCloudApp(ctx, client, api, session.AccessToken, name)
		err = appErr
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		appID = app.ID
		if created {
			fmt.Fprintf(out, "Created Spine Cloud app %s (%s).\n", app.Name, app.ID)
		} else {
			fmt.Fprintf(out, "Using Spine Cloud app %s (%s).\n", app.Name, app.ID)
		}
	}
	fmt.Fprintf(out, "Deployment request ID: %s\n", requestID)
	result, err := submitCloudArtifact(ctx, client, api, appID, requestID, session.AccessToken, artifact)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "Deployment %s submitted with status %s.\n", result.ID, result.Status)
	if result.Status == "failed" {
		return 1
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer waitCancel()
	finished, err := waitCloudDeployment(waitCtx, client, api, appID, result.ID, session.AccessToken, 2*time.Second)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "Deployment is live: %s\n", *finished.PublicURL)
	return 0
}
