package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func runGo(args []string, production bool, out, errOut io.Writer) int {
	cmd := exec.Command("go", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, errOut
	if production {
		cmd.Env = append(os.Environ(), "ENV=production")
	}
	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(errOut, "could not run go: %v\n", err)
		return 1
	}
	return 0
}

func runProject(development bool, out, errOut io.Writer) int {
	config, err := loadProjectConfig()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if _, err := applicationShutdownTimeout(); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		fmt.Fprintf(errOut, "could not resolve project directory: %v\n", err)
		return 1
	}
	frontend, err := buildFrontend(projectRoot, config, true, errOut)
	if err != nil {
		fmt.Fprintf(errOut, "frontend build failed: %v\n", err)
		return 1
	}
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	// Go의 빌드 캐시는 로컬 replace와 워크스페이스 입력도 검증합니다.
	binary, directory, err := buildDevBinaryContext(ctx, config, errOut, frontend)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintf(errOut, "application build failed: %v\n", err)
		return 1
	}
	defer os.RemoveAll(directory)
	environment := "production"
	if development {
		environment = "development"
	}
	process, err := startApplicationProcess(binary, directory, config, environment, out, errOut, frontend)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	select {
	case err := <-process.done:
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	case <-ctx.Done():
		if stopDevProcess(process, false) {
			fmt.Fprintln(errOut, forcedTerminationMessage("Application", runtime.GOOS))
			return 1
		}
		return 0
	}
}

func runCheck(out, errOut io.Writer) int {
	unformatted, err := unformattedFiles(".")
	if err != nil {
		fmt.Fprintf(errOut, "could not check formatting: %v\n", err)
		return 1
	}
	if len(unformatted) > 0 {
		fmt.Fprintf(errOut, "files need gofmt:\n%s\n", strings.Join(unformatted, "\n"))
		return 1
	}
	if code := runGo([]string{"vet", "./..."}, false, out, errOut); code != 0 {
		return code
	}
	return runGo([]string{"test", "./..."}, false, out, errOut)
}

func runVerify(args []string, out, errOut io.Writer) int {
	warningsAsErrors, jsonOutput := false, false
	for _, argument := range args {
		if argument == "--warnings-as-errors" {
			warningsAsErrors = true
		} else if argument == "--json" {
			jsonOutput = true
		} else {
			fmt.Fprintln(errOut, "usage: spine verify [--warnings-as-errors] [--json]")
			return 1
		}
	}
	contract, err := inspectApplication()
	if err != nil {
		fmt.Fprintf(errOut, "contract inspection failed: %v\n", err)
		return 1
	}
	report := buildContractCheckReport(contract, warningsAsErrors)
	if jsonOutput {
		verification := verificationReport{
			SchemaVersion:    "spine.verification/v1alpha1",
			Valid:            report.Valid,
			Stages:           []verificationStage{{Name: "contract", OK: report.Valid, Diagnostics: report.Diagnostics}},
			EvidenceBoundary: report.EvidenceBoundary,
		}
		if !report.Valid {
			_ = encodeIndentedJSON(verification, out, errOut)
			fmt.Fprintln(errOut, "static execution contract validation failed")
			return 1
		}
		var checkOut, checkErr bytes.Buffer
		code := runCheck(&checkOut, &checkErr)
		verification.Valid = code == 0
		verification.Stages = append(verification.Stages, verificationStage{Name: "gofmt-vet-test", OK: code == 0, Output: strings.TrimSpace(checkOut.String()), Error: strings.TrimSpace(checkErr.String())})
		if encodeCode := encodeIndentedJSON(verification, out, errOut); encodeCode != 0 {
			return encodeCode
		}
		if code != 0 {
			fmt.Fprintln(errOut, "local Go verification failed")
		}
		return code
	}
	for _, diagnostic := range report.Diagnostics {
		writer := out
		if diagnostic.Severity == "error" || (warningsAsErrors && diagnostic.Severity == "warning") {
			writer = errOut
		}
		printContractDiagnostic(writer, diagnostic, "")
	}
	if !report.Valid {
		return 1
	}
	if code := runCheck(out, errOut); code != 0 {
		return code
	}
	fmt.Fprintln(out, "Local Spine contract verification passed (source contract, gofmt, go vet, go test).")
	fmt.Fprintln(out, "External databases, brokers, listeners, and deployed runtime behavior were not checked.")
	return 0
}

type verificationReport struct {
	SchemaVersion    string              `json:"schema_version"`
	Valid            bool                `json:"valid"`
	Stages           []verificationStage `json:"stages"`
	EvidenceBoundary []string            `json:"evidence_boundary"`
}

type verificationStage struct {
	Name        string               `json:"name"`
	OK          bool                 `json:"ok"`
	Diagnostics []contractDiagnostic `json:"diagnostics,omitempty"`
	Output      string               `json:"output,omitempty"`
	Error       string               `json:"error,omitempty"`
}

func unformattedFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == ".gocache") {
			return filepath.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		formatted, err := format.Source(contents)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !bytes.Equal(contents, formatted) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func runBuild(out, errOut io.Writer) int {
	config, err := loadProjectConfig()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	name, err := projectName()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := os.MkdirAll("bin", 0o755); err != nil {
		fmt.Fprintf(errOut, "could not create build output directory: %v\n", err)
		return 1
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		fmt.Fprintf(errOut, "could not resolve project directory: %v\n", err)
		return 1
	}
	frontend, err := buildFrontend(projectRoot, config, true, errOut)
	if err != nil {
		fmt.Fprintf(errOut, "frontend build failed: %v\n", err)
		return 1
	}
	overlay, err := createFrontendOverlay(projectRoot, config, frontend)
	if err != nil {
		fmt.Fprintf(errOut, "frontend serving integration failed: %v\n", err)
		return 1
	}
	if overlay != nil {
		defer overlay.cleanup()
	}
	return runGo(applicationBuildArgs(config, filepath.Join("bin", name), overlay), false, out, errOut)
}

func projectName() (string, error) {
	f, err := os.Open("go.mod")
	if err != nil {
		return "", fmt.Errorf("spine build must run from a Go module: %w", err)
	}
	defer f.Close()
	for scanner := bufio.NewScanner(f); scanner.Scan(); {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "module" {
			return filepath.Base(fields[1]), nil
		}
	}
	return "", fmt.Errorf("could not find module name in go.mod")
}

func runCompletion(args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, "usage: spine completion <powershell|bash|zsh>")
		return 1
	}
	commands := "new update generate g dev run test check inspect explain verify contract doctor capabilities schema context build licenses completion help version"
	switch args[0] {
	case "powershell":
		fmt.Fprintf(out, "Register-ArgumentCompleter -Native -CommandName spine -ScriptBlock { param($wordToComplete) '%s'.Split(' ') | Where-Object { $_ -like \"$wordToComplete*\" } }\n", commands)
	case "bash":
		fmt.Fprintf(out, "complete -W '%s' spine\n", commands)
	case "zsh":
		fmt.Fprintf(out, "#compdef spine\n_arguments '1: :(%s)'\n", commands)
	default:
		fmt.Fprintf(errOut, "unsupported shell: %s\n", args[0])
		return 1
	}
	return 0
}
