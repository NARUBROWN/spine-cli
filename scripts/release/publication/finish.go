package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasegithub"
	"github.com/NARUBROWN/spine-cli/scripts/release/internal/releasenotes"
	"github.com/NARUBROWN/spine-cli/scripts/release/internal/reports"
)

func read(path string) (doc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return reports.Decode(raw)
}

func finish(plan doc, rows []doc, attempt string, tagSHA func(string) (string, error), upstream func(doc) (doc, error)) (doc, error) {
	version := field(plan, "version")
	if err := reports.Validate(rows, plan, version); err != nil {
		return nil, err
	}
	tagged, err := tagSHA(version)
	if err != nil {
		return nil, err
	}
	if tagged == "" || tagged != field(plan, "source_sha") {
		return nil, errors.New("공개 태그가 발행 계획과 다릅니다")
	}
	if old, ok := plan["upstream_evidence"]; ok {
		fresh, err := upstream(object(object(plan, "components"), "recipe"))
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(fresh, old) {
			return nil, errors.New("태그 생성 후 선행 발행 근거가 바뀌었습니다")
		}
	}
	manifest := make(doc, len(plan)+2)
	for key, value := range plan {
		manifest[key] = value
	}
	manifest["workflow_run_attempt"] = attempt
	manifest["validation"] = doc{"result": "passed", "version": version, "source_sha": tagged, "components": plan["components"], "reports": rows}
	return manifest, nil
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("publication command is required")
	}
	switch args[0] {
	case "finish", "check-promotion-ci", "plan", "tag", "validate-policy", "check-environment", "verified-source":
	default:
		return fmt.Errorf("unknown publication command %q", args[0])
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	policyPath := flags.String("policy", ".github/release-policy.json", "release policy")
	planPath := flags.String("plan", "release-plan.json", "release plan")
	reportsPath := flags.String("reports", "public-reports", "public verification reports")
	output := flags.String("manifest", "release-manifest.json", "manifest output")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if args[0] == "verified-source" {
		return verifiedSource()
	}
	policy, err := read(*policyPath)
	if err != nil {
		return err
	}
	if err := validatePolicy(policy); err != nil {
		return err
	}
	if args[0] == "validate-policy" {
		return checkPolicyFiles(".", policy)
	}
	repo, err := releasegithub.New(os.Getenv("GITHUB_REPOSITORY"), "")
	if err != nil {
		return err
	}
	if args[0] == "check-environment" {
		return checkApprovalEnvironments(repo, policy)
	}
	if args[0] == "plan" {
		return preparePlan(repo, repo.Repository, policy, *planPath)
	}
	if args[0] == "check-promotion-ci" {
		return runPromotionCheck(repo, repo.Repository, policy)
	}
	plan, err := read(*planPath)
	if err != nil {
		return err
	}
	if args[0] == "tag" {
		return publish(repo, repo.Repository, policy, plan, *reportsPath)
	}
	rows, err := reports.Collect(*reportsPath, plan, field(plan, "version"))
	if err != nil {
		return err
	}
	attempt, ok := os.LookupEnv("GITHUB_RUN_ATTEMPT")
	if !ok {
		attempt = "1"
	}
	manifest, err := finish(plan, rows, attempt, repo.TagSHA, func(recipe doc) (doc, error) {
		return upstreamEvidence(recipe, os.Getenv("RELEASE_EVIDENCE_TOKEN"))
	})
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "spine-cli-release-publish-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	if err := releasenotes.WriteReview(plan, field(policy, "notes_path"), directory); err != nil {
		return err
	}
	path, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if err := writeDocument(path, manifest); err != nil {
		return err
	}
	if field(plan, "channel") != "stable" {
		return nil
	}
	version := field(plan, "version")
	cmd := exec.Command("gh", "release", "create", version, path, "--repo", repo.Repository, "--verify-tag", "--title", version, "--notes-file", filepath.Join(directory, "release-notes.md"), "--latest")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("명령 실패: gh release\n%s", strings.TrimSpace(string(output)))
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "릴리스 중단:", err)
		os.Exit(1)
	}
}

func writeDocument(path string, manifest doc) error {
	dest, err := os.Create(path)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(dest)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return errors.Join(encoder.Encode(manifest), dest.Close())
}
