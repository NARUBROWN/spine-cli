package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const module = "github.com/NARUBROWN/spine-cli"
const platformPrefix = "github.com/NARUBROWN/spine-"

var entrypoints = []string{"spine", "spine-cli"}
var matrixOptions = []string{"--validator", "playground", "--websocket-platform", "gorilla", "--event-platform", "kafka", "--event-platform", "rabbitmq", "--event-platform", "nats"}

type plan struct {
	SHA        string `json:"source_sha"`
	Version    string
	Scope      string          `json:"public_validation_scope"`
	Source     json.RawMessage `json:"source_evidence"`
	Components json.RawMessage
	Upstream   map[string]json.RawMessage `json:"upstream_evidence"`
	RC         map[string]string          `json:"rc_cohort"`
}

type recipe struct {
	ID      string
	Modules map[string]string
}

type moduleInfo struct {
	Path, Version string
	Indirect      bool
	Replace       json.RawMessage
}

type runner struct {
	work     string
	env      []string
	bin      string
	override func(string, time.Duration, ...string) (string, error)
}

func (r runner) command(dir string, timeout time.Duration, args ...string) (string, error) {
	if r.override != nil {
		return r.override(dir, timeout, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir, cmd.Env = dir, r.env
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", errors.New(out.String() + stderr.String())
	}
	return strings.TrimSpace(out.String()), nil
}

func (r runner) execute(dir string, args ...string) (string, error) {
	fmt.Println("[public] " + strings.Join(args, " "))
	return r.command(dir, 600*time.Second, args...)
}

func decode[T any](text string, err error) (T, error) {
	var value T
	if err == nil {
		err = json.Unmarshal([]byte(text), &value)
	}
	return value, err
}

type result struct {
	BaseHash string `json:"base_hash"`
	Diff     json.RawMessage
}

func (r runner) envelope(cli string, args ...string) (result, error) {
	response, err := decode[struct {
		Schema string `json:"schema_version"`
		OK     bool
		Result result
	}](r.execute(r.work, append(append([]string{cli}, args...), "--json")...))
	if err != nil {
		return result{}, err
	}
	if response.Schema != "spine.cli-result/v1" || !response.OK {
		return result{}, errors.New("CLI 실행 계약을 확인할 수 없습니다")
	}
	return response.Result, nil
}

func moduleGraph(text string) ([]moduleInfo, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	var graph []moduleInfo
	for {
		var item moduleInfo
		err := decoder.Decode(&item)
		if err == io.EOF {
			return graph, nil
		}
		if err != nil {
			return nil, err
		}
		graph = append(graph, item)
	}
}

func assertModules(graph []moduleInfo, expected map[string]string) error {
	selected := make(map[string]string)
	for _, item := range graph {
		if len(item.Replace) > 0 && !bytes.Equal(item.Replace, []byte("null")) && !bytes.Equal(item.Replace, []byte("{}")) {
			return errors.New("공개 설치 검증에는 replace를 사용할 수 없습니다")
		}
		selected[item.Path] = item.Version
	}
	for name, version := range expected {
		if actual, ok := selected[name]; !ok || actual != version {
			return errors.New("생성 레시피의 실제 모듈 버전이 다릅니다: " + name)
		}
	}
	return nil
}

func requiredModules(items []moduleInfo, direct bool) map[string]string {
	required := make(map[string]string)
	for _, item := range items {
		if !direct || !item.Indirect {
			required[item.Path] = item.Version
		}
	}
	return required
}

func expectedDependencies(required map[string]string, rec recipe, p plan, provider string) (map[string]string, error) {
	allowed := make(map[string]string)
	maps.Copy(allowed, rec.Modules)
	for name, raw := range p.Upstream {
		var component struct {
			Version  string
			Selected *string `json:"selected_version"`
		}
		if err := json.Unmarshal(raw, &component); err != nil {
			return nil, err
		}
		version := component.Version
		if component.Selected != nil {
			version = *component.Selected
		}
		allowed[name] = version
	}
	for name, version := range required {
		if v, ok := allowed[name]; !ok || v != version {
			return nil, errors.New("기본 생성 의존성이 계획과 다릅니다: " + name)
		}
	}
	if provider != "" {
		for name := range p.Upstream {
			if name != alternatePlatform(provider) {
				if _, ok := required[name]; !ok {
					return nil, errors.New("전체 플랫폼 조합에 필요한 모듈이 누락됐습니다")
				}
			}
		}
	}
	return allowed, nil
}

func alternatePlatform(provider string) string {
	if provider == "echo" {
		return platformPrefix + "nethttp-platform"
	}
	return platformPrefix + "echo-platform"
}

func (r runner) waitForTag(version string) error {
	target := module + "@" + version
	for _, delay := range []time.Duration{15, 30, 60, 120, 0} {
		_, err := r.command(r.work, 60*time.Second, "go", "mod", "download", "-json", target)
		if err == nil {
			return nil
		}
		message := err.Error()
		if delay == 0 || !strings.Contains(message, target) || !strings.Contains(message, "verifying module") ||
			!strings.Contains(message, "sum.golang.org/lookup/") || !strings.Contains(message, "404 Not Found") {
			return fmt.Errorf("%s 공개 모듈 다운로드 실패\n%s", target, strings.TrimSpace(message))
		}
		fmt.Printf("%s 공개 체크섬을 %d초 뒤 다시 조회합니다.\n", target, delay)
		time.Sleep(delay * time.Second)
	}
	return errors.New("public checksum retries exhausted")
}

func installSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (r runner) install(query, version string, tagOnly bool) error {
	for i, name := range entrypoints {
		target := module
		if i == 0 {
			target += "/cmd/spine"
		}
		if _, err := r.execute(r.work, "go", "install", target+"@"+query); err != nil {
			return err
		}
		binary := filepath.Join(r.bin, name+installSuffix())
		displayed, err := r.execute(r.work, binary, "version")
		if err != nil {
			return err
		}
		if displayed != "spine-cli "+strings.TrimPrefix(version, "v") {
			return errors.New("CLI 표시 버전이 실제 설치 버전과 다릅니다")
		}
		info, err := r.execute(r.work, "go", "version", "-m", binary)
		if err != nil {
			return err
		}
		match := false
		for _, line := range strings.Split(info, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[0] == "mod" && fields[1] == module && fields[2] == version {
				match = true
				break
			}
		}
		if !match {
			return errors.New("CLI 빌드 정보가 공개 모듈 버전과 다릅니다")
		}
		if !tagOnly {
			for _, command := range []string{"schema", "capabilities"} {
				if _, err := r.envelope(binary, command); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func configValue(app, key string) (string, error) {
	data, err := os.ReadFile(filepath.Join(app, "spine.toml"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		name, value, found := strings.Cut(line, "=")
		if found && strings.TrimSpace(name) == key {
			return strconv.Unquote(strings.TrimSpace(value))
		}
	}
	return "", nil
}

func (r runner) requirements(app string, direct bool) (map[string]string, error) {
	parsed, err := decode[struct{ Require []moduleInfo }](r.execute(app, "go", "mod", "edit", "-json"))
	if err != nil {
		return nil, err
	}
	return requiredModules(parsed.Require, direct), nil
}

func (r runner) graph(app string) ([]moduleInfo, error) {
	text, err := r.execute(app, "go", "list", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}
	return moduleGraph(text)
}

func (r runner) composition(cli, layout, provider string, rc bool, rec recipe, p plan) error {
	name := layout + "-http"
	if provider != "" {
		name = layout + "-" + provider + "-full"
	}
	if rc {
		name = "rc-" + layout + "-" + provider
	}
	args := []string{"new", name, "--module", "example.com/" + name, "--layout", layout}
	if rc {
		args = append(args, "--channel", "rc", "--platform", provider, "--skip-install")
		args = append(args, matrixOptions...)
	} else {
		args = append(args, "--skip-install")
		if provider != "" {
			args = append(args, "--platform", provider)
			args = append(args, matrixOptions...)
		}
	}
	generated, err := r.envelope(cli, append(args, "--dry-run")...)
	if err != nil {
		return err
	}
	if !rc && (generated.BaseHash == "" || !hasDiff(generated.Diff)) {
		return errors.New("생성 계획에 검토할 diff와 base_hash가 없습니다")
	}
	if _, err := r.envelope(cli, append(args, "--if-match", generated.BaseHash)...); err != nil {
		return err
	}
	app := filepath.Join(r.work, name)
	key, value := "recipe", rec.ID
	if rc {
		key, value = "dependency_channel", "rc"
	}
	actual, err := configValue(app, key)
	if err != nil {
		return err
	}
	if actual != value {
		if rc {
			return errors.New("생성 프로젝트가 RC 채널을 사용하지 않습니다")
		}
		return errors.New("기본 생성 레시피가 정책과 다릅니다")
	}
	required, err := r.requirements(app, rc)
	if err != nil {
		return err
	}
	expected := maps.Clone(required)
	var allowed map[string]string
	if rc {
		expected = maps.Clone(p.RC)
		delete(expected, alternatePlatform(provider))
		for path, version := range expected {
			if required[path] != version {
				return errors.New("생성 프로젝트가 검증된 RC 조합을 선택하지 않았습니다")
			}
		}
	} else {
		allowed, err = expectedDependencies(required, rec, p, provider)
		if err != nil {
			return err
		}
	}
	for _, step := range []string{"tidy", "verify"} {
		if _, err := r.execute(app, "go", "mod", step); err != nil {
			return err
		}
	}
	graph, err := r.graph(app)
	if err != nil {
		return err
	}
	for _, item := range graph {
		if version, ok := allowed[item.Path]; ok {
			expected[item.Path] = version
		}
	}
	if err := assertModules(graph, expected); err != nil {
		return err
	}
	for _, args := range [][]string{{"context"}, {"contract", "check"}, {"verify"}} {
		if _, err := r.envelope(cli, append(args, "--root", app)...); err != nil {
			return err
		}
	}
	source := filepath.Join(app, "internal", "application", "application.go")
	var original [32]byte
	if !rc {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		original = sha256.Sum256(data)
	}
	update, err := r.envelope(cli, "update", "--root", app, "--dry-run")
	if err != nil {
		return err
	}
	if _, err := r.envelope(cli, "update", "--root", app, "--if-match", update.BaseHash); err != nil {
		return err
	}
	if !rc {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if sha256.Sum256(data) != original {
			return errors.New("update가 앱 소스를 변경했습니다")
		}
		updated, err := r.requirements(app, true)
		if err != nil {
			return err
		}
		if _, err := expectedDependencies(updated, rec, p, provider); err != nil {
			return err
		}
	}
	graph, err = r.graph(app)
	if err != nil {
		return err
	}
	if err := assertModules(graph, expected); err != nil || rc {
		return err
	}
	binary := filepath.Join(app, "server"+installSuffix())
	if _, err := r.execute(app, "go", "build", "-o", binary, "."); err != nil {
		return err
	}
	if provider == "" {
		return r.health(binary, app)
	}
	return nil
}

func hasDiff(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch diff := value.(type) {
	case string:
		return diff != ""
	case []any:
		return len(diff) > 0
	case map[string]any:
		return len(diff) > 0
	case bool:
		return diff
	case float64:
		return diff != 0
	default:
		return false
	}
}

func (r runner) health(binary, app string) (err error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	address := listener.Addr().String()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		return err
	}
	cmd := exec.Command(binary)
	cmd.Dir = app
	cmd.Env = append(append([]string{}, r.env...), "SPINE_PORT="+port)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
		default:
			if runtime.GOOS == "windows" {
				_ = cmd.Process.Kill()
			} else {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
			timer := time.NewTimer(15 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				_ = cmd.Process.Kill()
				<-done
				err = errors.New("생성 앱이 종료 제한 시간을 초과했습니다")
				return
			}
		}
		if conn, dialErr := net.DialTimeout("tcp4", address, time.Second); dialErr == nil {
			conn.Close()
			err = errors.New("생성 앱의 리스너가 계속 연결을 받습니다")
			return
		}
		rebound, bindErr := net.Listen("tcp4", address)
		if bindErr != nil {
			err = errors.New("생성 앱의 리스너가 해제되지 않았습니다")
			return
		}
		rebound.Close()
	}()
	client := http.Client{Timeout: time.Second}
	for range 100 {
		select {
		case <-done:
			return errors.New("공개 설치 CLI가 생성한 앱의 health 확인 실패")
		default:
		}
		response, err := client.Get("http://" + address + "/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("공개 설치 CLI가 생성한 앱의 health 확인 실패")
}

func (r runner) verify(p plan, query string) (map[string]any, error) {
	return r.verifyWithComposition(p, query, r.composition)
}

func (r runner) verifyWithComposition(p plan, query string, check func(string, string, string, bool, recipe, plan) error) (map[string]any, error) {
	resolved, err := decode[moduleInfo](r.execute(r.work, "go", "list", "-m", "-json", module+"@"+query))
	if err != nil {
		return nil, err
	}
	tagOnly := p.Scope == "tag-install"
	if tagOnly && (query != p.Version || !hasDiff(p.Source)) {
		return nil, errors.New("태그 설치 검증에는 승격 근거와 정확한 새 태그가 필요합니다")
	}
	if resolved.Version == "" || query == p.Version && resolved.Version != query {
		return nil, errors.New("공개 모듈이 요청한 태그를 가리키지 않습니다")
	}
	if err := r.install(query, resolved.Version, tagOnly); err != nil {
		return nil, err
	}
	goVersion, err := r.execute(r.work, "go", "version")
	if err != nil {
		return nil, err
	}
	checks := []string{"public installation and build provenance"}
	report := map[string]any{"result": "passed", "source_sha": p.SHA, "query": query, "resolved_cli_version": resolved.Version,
		"components": p.Components, "os": runtime.GOOS, "go_version": goVersion, "entrypoints": entrypoints, "checks": checks}
	if tagOnly {
		report["scope"] = "tag-install"
		return report, nil
	}
	var components struct{ Recipe recipe }
	if err := json.Unmarshal(p.Components, &components); err != nil {
		return nil, err
	}
	cli := filepath.Join(r.bin, "spine"+installSuffix())
	providers := []string{""}
	if p.Upstream != nil {
		providers = append(providers, "echo", "nethttp")
	}
	for _, layout := range []string{"legacy", "layered-v1"} {
		for _, provider := range providers {
			if err := check(cli, layout, provider, false, components.Recipe, p); err != nil {
				return nil, err
			}
		}
	}
	if len(p.RC) > 0 {
		for _, layout := range []string{"legacy", "layered-v1"} {
			for _, provider := range []string{"echo", "nethttp"} {
				if err := check(cli, layout, provider, true, components.Recipe, p); err != nil {
					return nil, err
				}
			}
		}
		report["rc_cohort"] = p.RC
		report["rc_combination_checks"] = []string{"echo-full", "nethttp-full"}
	}
	if p.Upstream != nil {
		report["upstream_evidence"] = p.Upstream
		report["combination_checks"] = []string{"echo-full", "nethttp-full"}
	}
	report["checks"] = append(checks, "two layouts and default HTTP composition", "default recipe and selected public modules", "dry-run/if-match and source-preserving update", "static contracts and generated tests", "HTTP health and listener release")
	return report, nil
}

func run() error {
	planPath := flag.String("plan", "release-plan.json", "release plan")
	query := flag.String("query", "", "public module query")
	output := flag.String("report", "release-validation.json", "verification report")
	flag.Parse()
	if *query == "" || flag.NArg() != 0 {
		return errors.New("--query is required")
	}
	raw, err := os.ReadFile(*planPath)
	if err != nil {
		return err
	}
	var p plan
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if *query != p.SHA && *query != p.Version {
		return errors.New("발행 계획의 SHA 또는 버전만 설치할 수 있습니다")
	}
	work, err := os.MkdirTemp("", "spine-cli-public-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	bin := filepath.Join(work, "bin")
	if err := os.Mkdir(bin, 0755); err != nil {
		return err
	}
	env := append(os.Environ(), "GOWORK=off", "GOFLAGS= ", "GOTOOLCHAIN=local", "GOPROXY=https://proxy.golang.org,direct", "GOSUMDB=sum.golang.org", "GOBIN="+bin, "GOCACHE="+filepath.Join(work, "build-cache"), "GOMODCACHE="+filepath.Join(work, "mod-cache"))
	r := runner{work: work, env: env, bin: bin}
	if *query == p.Version {
		if err := r.waitForTag(*query); err != nil {
			return err
		}
	}
	report, err := r.verify(p, *query)
	if err != nil {
		return err
	}
	var formatted bytes.Buffer
	encoder := json.NewEncoder(&formatted)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	return os.WriteFile(*output, formatted.Bytes(), 0644)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
