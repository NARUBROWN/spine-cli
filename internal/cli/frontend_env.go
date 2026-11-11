package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/joho/godotenv"
)

const frontendEnvModule = "spine:frontend-env"

func readFrontendEnvFile(projectRoot string) ([]byte, error) {
	contents, err := os.ReadFile(filepath.Join(projectRoot, ".env"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read project root .env: %w", err)
	}
	return contents, nil
}

func frontendEnvironment(projectRoot string) (map[string]string, error) {
	contents, err := readFrontendEnvFile(projectRoot)
	if err != nil {
		return nil, err
	}
	values, err := godotenv.Unmarshal(string(contents))
	if err != nil {
		// 파서 오류에 포함될 수 있는 설정값은 진단에 출력하지 않습니다.
		return nil, fmt.Errorf("could not parse project root .env: invalid dotenv syntax")
	}
	for name := range values {
		if value, found := os.LookupEnv(name); found {
			values[name] = value
		}
	}
	return values, nil
}

func frontendEnvPlugin(projectRoot string) api.Plugin {
	return api.Plugin{Name: "spine-frontend-env", Setup: func(build api.PluginBuild) {
		build.OnResolve(api.OnResolveOptions{Filter: `^spine:frontend-env$`}, func(api.OnResolveArgs) (api.OnResolveResult, error) {
			return api.OnResolveResult{Path: frontendEnvModule, Namespace: "spine-frontend-env"}, nil
		})
		build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "spine-frontend-env"}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
			// 재빌드마다 읽어 같은 빌드 컨텍스트에서도 .env 변경을 반영합니다.
			values, err := frontendEnvironment(projectRoot)
			if err != nil {
				return api.OnLoadResult{}, err
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				return api.OnLoadResult{}, fmt.Errorf("could not encode frontend environment")
			}
			quoted, _ := json.Marshal(string(encoded))
			contents := "export const __spine_frontend_env = JSON.parse(" + string(quoted) + ");\n"
			return api.OnLoadResult{Contents: &contents, Loader: api.LoaderJS}, nil
		})
	}}
}
