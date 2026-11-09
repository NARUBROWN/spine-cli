//go:build linux

package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

var runtimePath = regexp.MustCompile(`^/var/lib/spinecloud-runtime/apps/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/app$`)
var runtimeEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func runtimeEnvAllowed(name string) bool {
	if !runtimeEnvName.MatchString(name) {
		return false
	}
	u := strings.ToUpper(name)
	if strings.HasPrefix(u, "SPINE_") || strings.HasPrefix(u, "SPINECLOUD_") || strings.HasPrefix(u, "LD_") || strings.HasPrefix(u, "DYLD_") || strings.HasPrefix(u, "SYSTEMD_") {
		return false
	}
	switch u {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "CREDENTIALS_DIRECTORY", "NOTIFY_SOCKET", "LISTEN_FDS", "LISTEN_PID":
		return false
	}
	return true
}

func readRuntimeEnv(directory, port string) ([]string, error) {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1024 || n > 65535 {
		return nil, errors.New("invalid runtime port")
	}
	env := []string{"PATH=/usr/bin:/bin", "SPINE_PORT=" + port}
	if directory == "" {
		return env, nil
	}
	if !filepath.IsAbs(directory) {
		return nil, errors.New("invalid runtime credentials")
	}
	f, err := os.Open(filepath.Join(directory, "spine-env"))
	if err != nil {
		return nil, errors.New("runtime credentials unavailable")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return nil, errors.New("invalid runtime credentials")
	}
	var values map[string]string
	if json.Unmarshal(data, &values) != nil || len(values) > 128 {
		return nil, errors.New("invalid runtime credentials")
	}
	keys := make([]string, 0, len(values))
	for name, value := range values {
		if !runtimeEnvAllowed(name) || len(value) > 8192 || strings.ContainsRune(value, 0) {
			return nil, errors.New("invalid runtime credentials")
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		env = append(env, name+"="+values[name])
	}
	return env, nil
}

// startCloudRuntime는 검증된 실행 파일로 프로세스를 교체해 별도 CLI 프로세스를 남기지 않는다.
func startCloudRuntime(path string) error {
	if !runtimePath.MatchString(path) {
		return errors.New("invalid runtime executable")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("runtime executable unavailable")
	}
	env, err := readRuntimeEnv(os.Getenv("CREDENTIALS_DIRECTORY"), os.Getenv("SPINE_PORT"))
	if err != nil {
		return err
	}
	return execCloudRuntime(path, env)
}

func execCloudRuntime(path string, env []string) error {
	return syscall.Exec(path, []string{path}, env)
}
