package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const runtimeOpenAPIMarker = "SPINE_RUNTIME_OPENAPI="

// executeRuntimeOpenAPI는 리스너 없이 앱의 설명 API를 호출합니다.
// BuildApplication과 Go 도구는 실행되므로 정적 검사와 별도인 실행 증거입니다.
func executeRuntimeOpenAPI() ([]byte, error) {
	return executeRuntimeOpenAPIWithPreparation(false)
}

func executeRuntimeOpenAPIWithPreparation(prepare bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return executeRuntimeOpenAPIProcess(ctx, prepare)
}

func executeRuntimeOpenAPIContext(ctx context.Context) ([]byte, error) {
	return executeRuntimeOpenAPIProcess(ctx, false)
}

func executeRuntimeOpenAPIProcess(ctx context.Context, prepare bool) ([]byte, error) {
	importPath, err := compositionImportPath()
	if err != nil {
		return nil, err
	}
	imports := ""
	describe := "description, err := spine.DescribeHTTP(app, options)"
	if prepare {
		imports = "\"context\"\n\"errors\"\n\"time\""
		describe = preparedOpenAPIDescription
	}
	source, err := format.Source([]byte(fmt.Sprintf(runtimeOpenAPISource, imports, importPath, describe)))
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(".", ".spine-openapi-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	if err := os.WriteFile(filepath.Join(directory, "main.go"), source, 0o600); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "go", "run", "./"+filepath.Base(directory))
	command.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		if strings.Contains(message, "controller routes require a prepared application") {
			return nil, fmt.Errorf("controller routes are not collected: runtime export did not call Prepare; use --source static or --source runtime --prepare to execute DI constructors and collect controller routes")
		}
		return nil, fmt.Errorf("runtime OpenAPI export failed: %s\nSpine v0.7.0-preview.1 or compatible DescribeHTTP and pkg/openapi APIs are required. Use --source static for offline source export", message)
	}
	marker := bytes.LastIndex(stdout.Bytes(), []byte("\n"+runtimeOpenAPIMarker))
	if marker < 0 {
		return nil, fmt.Errorf("runtime OpenAPI process did not emit a document")
	}
	line := bytes.SplitN(stdout.Bytes()[marker+1+len(runtimeOpenAPIMarker):], []byte{'\n'}, 2)[0]
	document, err := base64.StdEncoding.DecodeString(string(line))
	if err != nil || !json.Valid(document) {
		return nil, fmt.Errorf("runtime OpenAPI process emitted an invalid document")
	}
	return document, nil
}

const runtimeOpenAPISource = `package main

import (
 %s
 "encoding/base64"
 "fmt"
 "os"
 application %q
 "github.com/NARUBROWN/spine"
 "github.com/NARUBROWN/spine/pkg/openapi"
)

func main() {
 if err := export(); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}
func export() error {
 app, options, err := application.BuildApplication()
 if err != nil { return err }
 if options.HTTP == nil { return fmt.Errorf("HTTP is disabled") }
 %s
 if err != nil { return err }
 document, err := openapi.Generate(description, options.HTTP.OpenAPI)
 if err != nil { return err }
 fmt.Print("\nSPINE_RUNTIME_OPENAPI=" + base64.StdEncoding.EncodeToString(document) + "\n")
 return nil
}
`

const preparedOpenAPIDescription = `
 prepared, err := app.Prepare(context.Background(), options)
 if err != nil { return err }
 description, err := spine.DescribePreparedHTTP(prepared)
 closeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
 defer cancel()
 if closeErr := prepared.Close(closeContext); closeErr != nil { return errors.Join(err, closeErr) }
`
