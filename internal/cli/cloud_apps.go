package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
)

type cloudApp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var cloudAppName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var errCloudAppNameExists = errors.New("app name already exists")

func cloudAppsRequest(ctx context.Context, client *http.Client, api, token, method, name string) ([]cloudApp, error) {
	if !validCloudAPIOrigin(api) || token == "" || (method != http.MethodGet && method != http.MethodPost) {
		return nil, errors.New("invalid app request")
	}
	var body io.Reader
	if method == http.MethodPost {
		if !cloudAppName.MatchString(name) {
			return nil, errors.New("invalid app name")
		}
		payload, _ := json.Marshal(map[string]string{"name": name})
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, api+"/api/apps", body)
	if err != nil {
		return nil, errors.New("could not prepare app request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := c.Do(req)
	if err != nil {
		return nil, errors.New("could not reach Spine Cloud")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return nil, errCloudSessionRequired
	}
	if res.StatusCode == http.StatusConflict {
		return nil, errCloudAppNameExists
	}
	var apps []cloudApp
	if method == http.MethodGet && res.StatusCode == http.StatusOK {
		err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&apps)
	} else if method == http.MethodPost && res.StatusCode == http.StatusCreated {
		var app cloudApp
		err = json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&app)
		apps = []cloudApp{app}
	} else {
		return nil, errors.New("could not load or create an app")
	}
	if err != nil || len(apps) > 1000 {
		return nil, errors.New("invalid app response")
	}
	for _, app := range apps {
		if !cloudUserID.MatchString(app.ID) || !cloudAppName.MatchString(app.Name) {
			return nil, errors.New("invalid app response")
		}
	}
	return apps, nil
}

func projectCloudAppName() (string, error) {
	config, err := loadProjectConfig()
	if err != nil {
		return "", err
	}
	base := strings.ToLower(path.Base(strings.TrimSuffix(config.Module, "/")))
	var normalized strings.Builder
	dash := false
	for _, current := range base {
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' {
			normalized.WriteRune(current)
			dash = false
			continue
		}
		if normalized.Len() > 0 && !dash {
			normalized.WriteByte('-')
			dash = true
		}
	}
	name := strings.Trim(normalized.String(), "-")
	if len(name) == 1 {
		name += "-app"
	}
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	if !cloudAppName.MatchString(name) {
		return "", errors.New("project module does not produce a valid Spine Cloud app name; use --app with an existing app")
	}
	return name, nil
}

func ensureCloudApp(ctx context.Context, client *http.Client, api, token, name string) (cloudApp, bool, error) {
	var empty cloudApp
	if !cloudAppName.MatchString(name) {
		return empty, false, errors.New("invalid app name")
	}
	apps, err := cloudAppsRequest(ctx, client, api, token, http.MethodGet, "")
	if err != nil {
		return empty, false, err
	}
	for _, app := range apps {
		if app.Name == name {
			return app, false, nil
		}
	}
	created, err := cloudAppsRequest(ctx, client, api, token, http.MethodPost, name)
	if err == nil {
		return created[0], true, nil
	}
	if !errors.Is(err, errCloudAppNameExists) {
		return empty, false, err
	}
	apps, err = cloudAppsRequest(ctx, client, api, token, http.MethodGet, "")
	if err != nil {
		return empty, false, err
	}
	for _, app := range apps {
		if app.Name == name {
			return app, false, nil
		}
	}
	return empty, false, errCloudAppNameExists
}
