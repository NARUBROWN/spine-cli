#!/usr/bin/env python3
"""고정된 Preview 후보를 코어, 플랫폼, CLI 순서로 발행합니다."""

import argparse
import base64
import json
from pathlib import Path
import time

from release import CORE_MODULE, GitHub, ReleaseError, SHA, UPSTREAM_MODULES, parse_tag

CLI = "github.com/NARUBROWN/spine-cli"
ORDER = (*UPSTREAM_MODULES, CLI)


def validate_plan(plan, github_factory=GitHub):
    if set(plan) != set(ORDER):
        raise ReleaseError("코어, 모든 플랫폼, CLI 후보를 정확히 한 번 지정해야 합니다")
    for module in ORDER:
        item = plan[module]
        if not isinstance(item, dict) or set(item) != {"version", "source_sha"}:
            raise ReleaseError("후보마다 version과 source_sha가 필요합니다: " + module)
        if parse_tag(item["version"])[1] != "preview" or not SHA.fullmatch(item["source_sha"]):
            raise ReleaseError("Preview 버전과 전체 소스 SHA가 필요합니다: " + module)
        github = github_factory(module.removeprefix("github.com/"))
        head = github.api("git/ref/heads/preview")["object"]["sha"]
        tagged = github.tag_sha(item["version"])
        if head != item["source_sha"] or (tagged is not None and tagged != head):
            raise ReleaseError("Preview HEAD 또는 기존 태그가 계획과 다릅니다: " + module)
        raw = github.api("contents/.github/release-policy.json?ref=" + head)
        policy = json.loads(base64.b64decode(raw["content"]))
        if parse_tag(item["version"])[0] != policy["next_version"]:
            raise ReleaseError("후보 버전이 저장소의 다음 버전과 다릅니다: " + module)
        if module not in (CORE_MODULE, CLI):
            selected = policy["channels"]["preview"]["core_version"]
            if selected != plan[CORE_MODULE]["version"] or policy["ci_core"] != plan[CORE_MODULE]["source_sha"]:
                raise ReleaseError("플랫폼의 코어 버전 또는 CI 소스가 계획과 다릅니다: " + module)
        if tagged is not None:
            check_existing(github, module, item)
    return plan


def workflow_runs(github):
    return github.api("actions/workflows/preview.yml/runs?event=workflow_dispatch&branch=preview&per_page=100")["workflow_runs"]


def check_existing(github, module, item):
    prefix = "core-candidate-" if module == CORE_MODULE else "release-manifest-"
    proof = github.candidate_evidence(item["version"], prefix)
    manifest, run, artifact = proof["manifest"], proof["workflow_run"], proof["artifact"]
    report = manifest.get("validation", {})
    schema = "spine-cli.release-manifest/v1" if module == CLI else "spine.release-manifest/v1"
    if (manifest.get("schema_version") != schema or manifest.get("repository") != github.repository or
            manifest.get("version") != item["version"] or
            manifest.get("source_sha") != item["source_sha"] or manifest.get("channel") != "preview" or
            manifest.get("branch") != "preview" or
            report.get("result") != "passed" or report.get("version") != item["version"] or
            report.get("source_sha") != item["source_sha"] or report.get("components") != manifest.get("components") or
            run.get("status") != "completed" or
            run.get("conclusion") != "success" or run.get("event") != "workflow_dispatch" or
            run.get("head_sha") != item["source_sha"] or run.get("head_branch") != "preview" or
            run.get("path", "").split("@")[0] != ".github/workflows/preview.yml" or
            artifact.get("workflow_run", {}).get("id") != run.get("id") or
            artifact.get("workflow_run", {}).get("head_sha") != item["source_sha"] or
            str(run.get("id")) != manifest.get("workflow_run_id") or
            str(run.get("run_attempt")) != manifest.get("workflow_run_attempt")):
        raise ReleaseError("기존 태그의 발행 검증이 완료되지 않았습니다: " + module)


def dispatch_and_wait(module, item, *, timeout=21600, github_factory=GitHub, sleep=time.sleep, now=time.monotonic):
    github = github_factory(module.removeprefix("github.com/"))
    current = github.tag_sha(item["version"])
    if current:
        if current != item["source_sha"]:
            raise ReleaseError("기존 태그가 요청한 커밋과 다릅니다: " + module)
        check_existing(github, module, item)
        return
    if github.api("git/ref/heads/preview")["object"]["sha"] != item["source_sha"]:
        raise ReleaseError("발행 전에 Preview HEAD가 바뀌었습니다: " + module)
    previous = workflow_runs(github)
    if any(run["status"] != "completed" for run in previous):
        raise ReleaseError("이미 진행 중인 Preview 발행이 있습니다: " + module)
    known = {run["id"] for run in previous}
    github.api("actions/workflows/preview.yml/dispatches", method="POST", data={"ref": "preview", "inputs": item})
    deadline = now() + timeout
    run_id = None
    while now() < deadline:
        if run_id is None:
            matches = [run for run in workflow_runs(github) if run["id"] not in known and run["head_sha"] == item["source_sha"]]
            if len(matches) > 1:
                raise ReleaseError("새 발행 실행을 유일하게 식별할 수 없습니다: " + module)
            if matches:
                run_id = matches[0]["id"]
                print(f"{module}: 발행 실행 {run_id}", flush=True)
        else:
            run = github.api("actions/runs/" + str(run_id))
            if run["status"] == "completed":
                if run["conclusion"] != "success" or github.tag_sha(item["version"]) != item["source_sha"]:
                    raise ReleaseError("발행 실행 또는 태그 검증에 실패했습니다: " + module)
                check_existing(github, module, item)
                return
        sleep(15)
    raise ReleaseError("발행 완료 대기 시간이 초과됐습니다. 승인과 실행 상태를 확인하세요: " + module)


def run_train(plan, *, execute=False, github_factory=GitHub, dispatch=dispatch_and_wait):
    validate_plan(plan, github_factory)
    if not execute:
        for module in ORDER:
            print(f"{module}: {plan[module]['version']} {plan[module]['source_sha']}")
        return
    for module in ORDER:
        dispatch(module, plan[module], github_factory=github_factory)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", required=True, help="저장소별 Preview 버전과 소스 SHA를 담은 JSON")
    parser.add_argument("--execute", action="store_true", help="검토한 계획의 발행 workflow를 순서대로 실행")
    args = parser.parse_args()
    run_train(json.loads(Path(args.plan).read_text(encoding="utf-8")), execute=args.execute)


if __name__ == "__main__":
    try:
        main()
    except (ReleaseError, KeyError, ValueError) as exc:
        raise SystemExit("릴리스 중단: " + str(exc))
