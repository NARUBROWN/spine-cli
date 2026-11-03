#!/usr/bin/env python3
"""채널 브랜치의 GitHub 보호 규칙을 확인하거나 명시적으로 적용합니다."""

import argparse
import json
import os
from pathlib import Path

from release import GitHub, ReleaseError, approval_environment, run, SHA


BRANCHES = ("preview", "rc", "main")


def specification(branch):
    return {
        "name": "Spine CLI channel / " + branch,
        "target": "branch",
        "enforcement": "active",
        "bypass_actors": [],
        "conditions": {"ref_name": {"include": ["refs/heads/" + branch], "exclude": []}},
        "rules": [
            {"type": "deletion"},
            {"type": "non_fast_forward"},
            {"type": "pull_request", "parameters": {
                "allowed_merge_methods": ["merge", "squash", "rebase"] if branch == "preview" else ["merge"],
                "dismiss_stale_reviews_on_push": True,
                "require_code_owner_review": False,
                "require_last_push_approval": False,
                "required_approving_review_count": 0,
                "required_review_thread_resolution": True,
            }},
            {"type": "required_status_checks", "parameters": {
                "strict_required_status_checks_policy": False,
                "required_status_checks": [
                    {"context": "Verified Source / " + branch, "integration_id": 15368},
                    {"context": "Branch Flow / " + branch, "integration_id": 15368},
                ],
            }},
        ],
    }


def verify_rule(actual, expected):
    for key in ("name", "target", "enforcement", "bypass_actors", "conditions"):
        if actual.get(key) != expected[key]:
            raise ReleaseError(f"브랜치 보호 규칙 불일치: {expected['name']} / {key}")
    rules = {rule["type"]: rule for rule in actual["rules"]}
    for rule in expected["rules"]:
        if rule["type"] not in rules:
            raise ReleaseError("필수 브랜치 보호 규칙이 없습니다: " + rule["type"])
        params = rules[rule["type"]].get("parameters", {})
        for key, value in rule.get("parameters", {}).items():
            if params.get(key) != value:
                raise ReleaseError("브랜치 보호 매개변수가 다릅니다: " + key)


def bootstrap(github, sha):
    if not SHA.fullmatch(sha) or github.api("git/ref/heads/main")["object"]["sha"] != sha:
        raise ReleaseError("초기 전환은 현재 main의 전체 SHA를 지정해야 합니다")
    for path in (".github/release-policy.json", ".github/workflows/ci.yml", ".github/workflows/branch-flow.yml"):
        github.api("contents/" + path + "?ref=" + sha)
    missing = []
    for branch in ("preview", "rc"):
        ref = github.api("git/ref/heads/" + branch, missing=True)
        if ref and ref["object"]["sha"] != sha:
            raise ReleaseError("다른 커밋을 가진 기존 채널 브랜치를 변경하지 않습니다: " + branch)
        if not ref:
            missing.append(branch)
    for branch in missing:
        github.api("git/refs", method="POST", data={"ref": "refs/heads/" + branch, "sha": sha})


def tag_specification():
    return {"name": "Spine CLI immutable release tags", "target": "tag", "enforcement": "active",
            "bypass_actors": [], "conditions": {"ref_name": {"include": ["refs/tags/v*"], "exclude": []}},
            "rules": [{"type": "deletion"}, {"type": "update"}]}


def configure(github, apply=False):
    if github.api("branches/main/protection", missing=True) is not None:
        raise ReleaseError("기존 main 보호를 자동 변경하지 않습니다. 전환 전 호환성을 검토하세요")
    # 기본 브랜치를 바꾸기 전에 신뢰할 PR 방향 검사 코드가 preview에 있어야 합니다.
    github.api("contents/.github/workflows/branch-flow.yml?ref=preview")
    for branch in BRANCHES:
        github.api("git/ref/heads/"+branch)
    listed = {rule["name"]: rule for rule in github.api("rulesets?per_page=100")}
    for branch in BRANCHES:
        expected = specification(branch)
        previous = listed.get(expected["name"])
        if apply:
            if previous:
                github.api(f"rulesets/{previous['id']}", method="PUT", data=expected)
            else:
                previous = github.api("rulesets", method="POST", data=expected)
        if not previous:
            raise ReleaseError("브랜치 보호 규칙을 먼저 적용해야 합니다: " + branch)
        verify_rule(github.api(f"rulesets/{previous['id']}"), expected)
        print("채널 보호 확인:", branch)
    expected = tag_specification()
    previous = listed.get(expected["name"])
    if apply:
        if previous:
            github.api(f"rulesets/{previous['id']}", method="PUT", data=expected)
        else:
            previous = github.api("rulesets", method="POST", data=expected)
    if not previous:
        raise ReleaseError("발행 태그의 변경과 삭제 방지 규칙이 필요합니다")
    verify_rule(github.api(f"rulesets/{previous['id']}"), expected)
    if apply:
        github.api("", method="PATCH", data={"default_branch": "preview"})
        policy = json.loads(Path(".github/release-policy.json").read_text(encoding="utf-8"))
        # 리뷰어 ID는 저장소 외 API에서 읽으며 토큰이나 비밀 값은 출력하지 않습니다.
        user = json.loads(run("gh", "api", "users/" + policy["reviewer_login"]))
        for channel, branch in (("preview", "preview"), ("rc", "rc"), ("stable", "main")):
            environment = "release-" + channel
            github.api("environments/" + environment, method="PUT", data={
                "can_admins_bypass": False,
                "prevent_self_review": False,
                "reviewers": [{"type": "User", "id": user["id"]}],
                "deployment_branch_policy": {"protected_branches": False, "custom_branch_policies": True},
            })
            policies = github.api(f"environments/{environment}/deployment-branch-policies")["branch_policies"]
            for existing in policies:
                if existing["name"] != branch or existing.get("type", "branch") != "branch":
                    raise ReleaseError("기존 승인 환경의 다른 브랜치 규칙을 먼저 검토하세요")
            if not policies:
                github.api(f"environments/{environment}/deployment-branch-policies", method="POST",
                           data={"name": branch, "type": "branch"})
    policy = json.loads(Path(".github/release-policy.json").read_text(encoding="utf-8"))
    for channel in ("preview", "rc", "stable"):
        approval_environment(github, policy, channel)
    repo = github.api("")
    if repo["default_branch"] != "preview":
        raise ReleaseError("저장소 기본 브랜치는 preview여야 합니다")
    if github.api("branches/main/protection", missing=True) is not None:
        raise ReleaseError("기존 main 보호와 새 승격 규칙의 호환성을 수동 검토하세요")
    print("기본 브랜치와 preview → rc → main 보호 확인 완료")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--bootstrap-sha")
    args = parser.parse_args()
    github = GitHub(os.environ.get("GITHUB_REPOSITORY", "NARUBROWN/spine-cli"))
    if args.bootstrap_sha:
        if not args.apply:
            parser.error("--bootstrap-sha는 --apply와 함께 사용합니다")
        bootstrap(github, args.bootstrap_sha)
    configure(github, args.apply)
