#!/usr/bin/env python3
"""Preview, RC, Stable의 출처와 관찰 기간을 검사하고 승인된 계획만 발행합니다."""

import argparse
import base64
import datetime as dt
import json
import hashlib
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


TAG = re.compile(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-(preview|rc)\.([1-9]\d*))?\Z")
SHA = re.compile(r"[0-9a-f]{40}\Z")
WORKFLOWS = {"preview": "preview.yml", "rc": "rc.yml", "stable": "release.yml"}


class ReleaseError(Exception):
    pass


def run(*args, **kwargs):
    result = subprocess.run(args, text=True, capture_output=True, **kwargs, encoding="utf-8")
    if result.returncode:
        raise ReleaseError(f"명령 실패: {args[0]} {args[1]}\n{result.stderr.strip()}")
    return result.stdout.strip()


def parse_tag(value):
    match = TAG.fullmatch(value)
    if not match:
        raise ReleaseError("버전은 vX.Y.Z-preview.N, vX.Y.Z-rc.N 또는 vX.Y.Z 형식이어야 합니다")
    major, minor, patch, channel, number = match.groups()
    return f"v{major}.{minor}.{patch}", channel or "stable", int(number or 0)


def timestamp(value):
    try:
        return dt.datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(dt.timezone.utc)
    except (AttributeError, ValueError) as exc:
        raise ReleaseError("공개 시각이 없는 릴리스는 승격할 수 없습니다") from exc


def require_age(published_at, hours, now, reason):
    age = (now - timestamp(published_at)).total_seconds() / 3600
    if age < 0:
        raise ReleaseError("공개 시각이 현재보다 뒤인 릴리스는 사용할 수 없습니다")
    if age < hours and not reason:
        raise ReleaseError(f"관찰 기간 부족: {age:.1f}시간 경과, {hours}시간 필요")


def validate_policy(policy):
    if policy.get("schema_version") != "spine-cli.release-policy/v1":
        raise ReleaseError("지원하지 않는 릴리스 정책입니다")
    if parse_tag(policy["next_version"])[1] != "stable":
        raise ReleaseError("next_version에는 정식 버전을 지정해야 합니다")
    for name in ("preview_hours", "rc_hours"):
        if type(policy.get(name)) is not int or policy[name] <= 0:
            raise ReleaseError(f"{name}에는 양의 시간을 지정해야 합니다")
    if type(policy.get("stable_interval_hours")) is not int or policy["stable_interval_hours"] < 0:
        raise ReleaseError("정식 출시 간격은 0 이상의 시간이어야 합니다")
    if policy.get("approval_environment") != "release-stable" or not policy.get("reviewer_login"):
        raise ReleaseError("정식 승인 환경과 지정 리뷰어가 필요합니다")
    if policy.get("branches") != {"preview": "preview", "rc": "rc", "stable": "main"}:
        raise ReleaseError("출시 브랜치는 preview → rc → main으로 지정해야 합니다")
    if policy.get("required_public_os") != ["linux", "darwin", "windows"]:
        raise ReleaseError("공개 설치는 Linux, macOS, Windows에서 검증해야 합니다")
    recipe = policy.get("recipe", {})
    if not recipe.get("id") or "github.com/NARUBROWN/spine" not in recipe.get("modules", {}):
        raise ReleaseError("기본 생성 레시피와 모듈 버전을 고정해야 합니다")
    for version in recipe["modules"].values():
        if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
            raise ReleaseError("레시피에는 변경 가능한 브랜치 대신 모듈 버전을 지정해야 합니다")
    if policy.get("notes_path") != "release/notes/" + policy["next_version"] + ".md":
        raise ReleaseError("대상 버전의 릴리스 초안 경로가 필요합니다")


class GitHub:
    def __init__(self, repository):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
            raise ReleaseError("저장소 이름이 올바르지 않습니다")
        self.repository = repository

    def api(self, path, *, missing=False, method="GET", data=None):
        endpoint = f"repos/{self.repository}" + ("/" + path if path else "")
        args = ["gh", "api", "--method", method, endpoint]
        if data is not None:
            args += ["--input", "-"]
        result = subprocess.run(args, input=json.dumps(data) if data is not None else None,
                                text=True, capture_output=True, encoding="utf-8")
        if result.returncode:
            # 인증, 통신 오류를 '태그 없음'으로 해석하지 않습니다.
            try:
                status = json.loads(result.stdout).get("status")
            except (ValueError, AttributeError):
                status = None
            if missing and str(status) == "404":
                return None
            raise ReleaseError(f"GitHub API 실패 ({path}): {result.stderr.strip()}")
        return json.loads(result.stdout) if result.stdout.strip() else None

    def releases(self):
        result = []
        for page in range(1, 101):
            items = self.api(f"releases?per_page=100&page={page}")
            result.extend(items)
            if len(items) < 100:
                return result
        raise ReleaseError("릴리스 목록이 너무 많아 전체 확인을 마치지 못했습니다")

    def tag_sha(self, tag):
        parse_tag(tag)
        ref = self.api(f"git/ref/tags/{tag}", missing=True)
        if ref is None:
            return None
        obj = ref["object"]
        for _ in range(5):
            if obj["type"] == "commit":
                return obj["sha"]
            if obj["type"] != "tag":
                break
            obj = self.api(f"git/tags/{obj['sha']}")["object"]
        raise ReleaseError("태그가 단일 커밋을 가리키지 않습니다")

    def manifest(self, tag):
        with tempfile.TemporaryDirectory(prefix="spine-cli-release-manifest-") as directory:
            run("gh", "release", "download", tag, "--repo", self.repository,
                "--pattern", "release-manifest.json", "--dir", directory)
            return json.loads((Path(directory) / "release-manifest.json").read_text(encoding="utf-8"))


def verify_source_release(github, source, expected_channel, policy):
    _, channel, _ = parse_tag(source)
    if channel != expected_channel:
        raise ReleaseError(f"이 단계의 출처는 {expected_channel} 태그여야 합니다")
    release = github.api(f"releases/tags/{source}")
    if release.get("draft") or not release.get("prerelease"):
        raise ReleaseError("공개된 사전 릴리스만 승격할 수 있습니다")
    manifest = github.manifest(source)
    sha = github.tag_sha(source)
    if (manifest.get("schema_version") != "spine-cli.release-manifest/v1" or
            manifest.get("version") != source or manifest.get("channel") != channel or
            manifest.get("source_sha") != sha or not SHA.fullmatch(sha or "") or
            manifest.get("branch") != policy["branches"][channel] or
            manifest.get("validation", {}).get("result") != "passed" or
            manifest["validation"].get("source_sha") != sha or
            manifest["validation"].get("version") != source or
            manifest["validation"].get("components") != manifest.get("components")):
        raise ReleaseError("출처 태그와 릴리스 manifest가 일치하지 않습니다")
    workflow_run = github.api(f"actions/runs/{int(manifest['workflow_run_id'])}")
    if (workflow_run.get("conclusion") != "success" or workflow_run.get("status") != "completed" or
            workflow_run.get("event") != "workflow_dispatch" or
            workflow_run.get("head_branch") != policy["branches"][channel] or
            workflow_run.get("head_sha") != sha or
            workflow_run.get("path", "").split("@")[0] != f".github/workflows/{WORKFLOWS[channel]}"):
        raise ReleaseError("출처 릴리스의 발행 workflow가 성공하지 않았습니다")
    return release, manifest


def promotion(github, source_sha, upstream_sha, branch, upstream_branch):
    commit = github.api(f"git/commits/{source_sha}")
    upstream = github.api(f"git/commits/{upstream_sha}")
    parents = commit.get("parents", [])
    if len(parents) != 2 or parents[1]["sha"] != upstream_sha:
        raise ReleaseError("채널 승격은 검증한 후보를 두 번째 부모로 갖는 merge commit이어야 합니다")
    if commit["tree"]["sha"] != upstream["tree"]["sha"]:
        raise ReleaseError("승격 merge가 후보 소스를 변경했습니다. 새 Preview에서 다시 검증해야 합니다")
    pulls = github.api(f"commits/{source_sha}/pulls?per_page=100")
    matches = [pr for pr in pulls if pr.get("merged_at") and pr.get("merge_commit_sha") == source_sha
               and pr["base"]["ref"] == branch and pr["head"]["ref"] == upstream_branch
               and (pr["head"].get("repo") or {}).get("full_name") == github.repository]
    if not matches:
        raise ReleaseError(f"{upstream_branch} → {branch} 방향의 병합된 PR이 필요합니다")
    return {"number": matches[0]["number"], "head": upstream_branch, "base": branch,
            "merge_sha": source_sha, "head_sha": upstream_sha}


def approval_environment(github, policy, channel="stable"):
    name = "release-" + channel
    environment = github.api(f"environments/{name}")
    rules = environment.get("protection_rules", [])
    reviewers = [entry["reviewer"]["login"] for rule in rules if rule["type"] == "required_reviewers"
                 for entry in rule.get("reviewers", []) if entry["type"] == "User"]
    branches = environment.get("deployment_branch_policy") or {}
    if (policy["reviewer_login"] not in reviewers or environment.get("can_admins_bypass", True) or
            not branches.get("custom_branch_policies")):
        raise ReleaseError("정식 승인 환경에 지정 리뷰어, 우회 금지, 브랜치 제한이 필요합니다")
    branch_rules = github.api(f"environments/{name}/deployment-branch-policies")
    if len(branch_rules.get("branch_policies", [])) != 1 or not any(rule["name"] == policy["branches"][channel] and rule.get("type", "branch") == "branch"
               for rule in branch_rules.get("branch_policies", [])):
        raise ReleaseError("승인 환경은 해당 채널 브랜치에서만 실행할 수 있어야 합니다")


def make_plan(github, policy, channel, version, source, reason="", now=None):
    validate_policy(policy)
    now = now or dt.datetime.now(dt.timezone.utc)
    base, actual_channel, _ = parse_tag(version)
    if base != policy["next_version"] or channel != actual_channel:
        raise ReleaseError("대상 버전, 채널이 정책의 next_version과 일치하지 않습니다")
    if channel == "stable" and any("-" in v for v in policy["recipe"]["modules"].values()):
        raise ReleaseError("정식 CLI의 새 기본 레시피에는 정식 모듈만 사용할 수 있습니다. 레시피 변경 후 새 Preview가 필요합니다")
    reason = reason.strip()
    if reason and len(reason) < 20:
        raise ReleaseError("긴급 단축 사유를 20자 이상 기록해야 합니다")
    if github.tag_sha(version) is not None:
        raise ReleaseError("이미 존재하는 태그는 이동하거나 재사용하지 않습니다")
    if channel != "stable" and github.tag_sha(base) is not None:
        raise ReleaseError("이미 정식 발행한 버전에는 사전 릴리스를 추가할 수 없습니다")
    if channel != "stable":
        siblings = github.api(f"git/matching-refs/tags/{base}-{channel}.")
        numbers = [parse_tag(item["ref"].removeprefix("refs/tags/"))[2] for item in siblings
                   if TAG.fullmatch(item["ref"].removeprefix("refs/tags/"))]
        if numbers and parse_tag(version)[2] <= max(numbers):
            raise ReleaseError("같은 채널의 새 번호는 기존 번호보다 커야 합니다")
    branch = policy["branches"][channel]
    source_sha = github.api(f"git/ref/heads/{branch}")["object"]["sha"]
    if not SHA.fullmatch(source_sha):
        raise ReleaseError("채널 브랜치가 유효한 커밋을 가리키지 않습니다")
    if os.environ.get("GITHUB_SHA") and os.environ["GITHUB_SHA"] != source_sha:
        raise ReleaseError("workflow 실행 뒤 채널 HEAD가 바뀌었습니다")
    source_tag, upstream_sha, promotion_pr = "", "", None
    if channel == "preview":
        if not SHA.fullmatch(source) or source != source_sha:
            raise ReleaseError("Preview 출처에는 preview 브랜치 HEAD의 전체 커밋 SHA가 필요합니다")
    else:
        source_tag = source
        source_base, _, _ = parse_tag(source)
        if source_base != base:
            raise ReleaseError("다른 버전의 후보를 승격할 수 없습니다")
        expected = "preview" if channel == "rc" else "rc"
        release, manifest = verify_source_release(github, source, expected, policy)
        upstream_sha = manifest["source_sha"]
        promotion_pr = promotion(github, source_sha, upstream_sha, branch, policy["branches"][expected])
        # 코드와 호환 버전 조합은 승인된 후보에서 그대로 이어받습니다.
        if manifest.get("components") != components(policy):
            raise ReleaseError("후보 검증 뒤 호환 버전 조합이 바뀌었습니다. 새 Preview가 필요합니다")
        require_age(release["published_at"], policy[f"{expected}_hours"], now, reason)
    tree = github.api(f"git/commits/{source_sha}")["tree"]["sha"]
    source_policy = github.api(f"contents/.github/release-policy.json?ref={source_sha}")
    if json.loads(base64.b64decode(source_policy["content"])) != policy:
        raise ReleaseError("후보 커밋의 릴리스 정책이 현재 계획과 다릅니다. 대상 SHA를 확인하세요")
    approval_environment(github, policy, channel)
    if channel == "stable":
        stable = [r for r in github.releases() if not r.get("draft") and not r.get("prerelease")
                  and TAG.fullmatch(r["tag_name"]) and parse_tag(r["tag_name"])[1] == "stable"]
        if stable:
            highest = max(tuple(map(int, parse_tag(r["tag_name"])[0][1:].split("."))) for r in stable)
            if tuple(map(int, base[1:].split("."))) <= highest:
                raise ReleaseError("기존 정식 버전보다 높은 버전만 발행할 수 있습니다")
            last = max(stable, key=lambda r: timestamp(r["published_at"]))
            require_age(last["published_at"], policy["stable_interval_hours"], now, reason)
    plan = {"schema_version": "spine-cli.release-manifest/v1", "channel": channel, "version": version,
            "source_sha": source_sha, "source_tag": source_tag, "branch": branch, "source_tree": tree,
            "upstream_source_sha": upstream_sha, "promotion_pr": promotion_pr, "components": components(policy),
            "observation_override_reason": reason, "planned_at": now.isoformat(),
            "workflow_run_id": os.environ.get("GITHUB_RUN_ID", ""),
            "repository": github.repository}
    plan["required_public_os"] = policy["required_public_os"]
    plan["notes_sha256"] = hashlib.sha256(notes(plan, policy).encode()).hexdigest()
    return plan


def components(policy):
    recipe = policy["recipe"]
    digest = hashlib.sha256(json.dumps(recipe, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return {"recipe": recipe, "recipe_sha256": digest}


def notes(plan, policy):
    content = Path(policy["notes_path"]).read_text(encoding="utf-8")
    if "\u00b7" in content or not content.strip():
        raise ReleaseError("릴리스 초안의 문장과 내용을 확인하세요")
    return (content.rstrip() + f"\n\n버전: `{plan['version']}`\n\n채널: `{plan['channel']}`\n\n"
            f"소스: `{plan['source_sha']}`\n\n이전 단계: `{plan['source_tag'] or '없음'}`\n\n"
            f"레시피 SHA-256: `{plan['components']['recipe_sha256']}`\n\n"
            f"관찰 기간 단축 사유: {plan['observation_override_reason'] or '없음'}\n\n"
            "상세 검증 결과는 첨부 release-manifest.json을 확인하세요.\n")


def review_files(plan, policy, directory):
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    body = notes(plan, policy)
    if hashlib.sha256(body.encode()).hexdigest() != plan["notes_sha256"]:
        raise ReleaseError("검토한 발행 본문이 바뀌었습니다")
    (directory / "release-title.txt").write_text(plan["version"] + "\n", encoding="utf-8")
    (directory / "release-notes.md").write_text(body, encoding="utf-8")


def collect_reports(directory, plan, query):
    reports = [json.loads(p.read_text(encoding="utf-8")) for p in Path(directory).glob("**/release-validation.json")]
    required = plan["required_public_os"]
    if sorted(r.get("os") for r in reports) != sorted(required):
        raise ReleaseError("공개 설치 결과의 OS가 누락되거나 중복됐습니다")
    for report in reports:
        if (report.get("result") != "passed" or report.get("source_sha") != plan["source_sha"] or
                report.get("query") != query or report.get("components") != plan["components"] or
                set(report.get("entrypoints", [])) != {"spine", "spine-cli"}):
            raise ReleaseError("공개 설치 결과가 발행 계획과 다릅니다")
    return reports



def publish(github, policy, plan, verified_sha, reports_directory):
    collect_reports(reports_directory, plan, plan["source_sha"])
    if verified_sha != plan["source_sha"]:
        raise ReleaseError("검증한 커밋과 발행 대상이 다릅니다")
    if os.environ.get("GITHUB_REF") != "refs/heads/" + plan["branch"]:
        raise ReleaseError("릴리스 workflow의 실행 브랜치가 채널과 다릅니다")
    current = github.api("contents/.github/release-policy.json?ref=" + plan["branch"])
    if json.loads(base64.b64decode(current["content"])) != policy:
        raise ReleaseError("계획을 만든 뒤 채널 브랜치의 릴리스 정책이 바뀌었습니다")
    # 승인 대기 중 태그, 정책, 관찰 기간이 바뀌었는지 쓰기 직전에 다시 검사합니다.
    fresh = make_plan(github, policy, plan["channel"], plan["version"],
                      plan["source_tag"] or plan["source_sha"], plan["observation_override_reason"])
    for key in ("source_sha", "source_tree", "branch", "promotion_pr", "channel", "version", "components", "notes_sha256", "required_public_os"):
        if fresh[key] != plan[key]:
            raise ReleaseError("승인한 계획이 바뀌었습니다. 새 실행이 필요합니다")
    github.api("git/refs", method="POST", data={"ref": f"refs/tags/{plan['version']}", "sha": verified_sha})


def finish(github, policy, plan, reports_directory):
    reports = collect_reports(reports_directory, plan, plan["version"])
    if github.tag_sha(plan["version"]) != plan["source_sha"]:
        raise ReleaseError("공개 태그가 발행 계획과 다릅니다")
    manifest = dict(plan, validation={"result": "passed", "version": plan["version"],
                                    "source_sha": plan["source_sha"], "components": plan["components"],
                                    "reports": reports})
    with tempfile.TemporaryDirectory(prefix="spine-cli-release-publish-") as directory:
        path = Path(directory) / "release-manifest.json"
        path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        review_files(plan, policy, directory)
        args = ["gh", "release", "create", plan["version"], str(path), "--repo", github.repository,
                "--verify-tag", "--title", plan["version"], "--notes-file", str(Path(directory)/"release-notes.md")]
        args += ["--latest"] if plan["channel"] == "stable" else ["--prerelease", "--latest=false"]
        run(*args)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["plan", "tag", "finish", "validate-policy", "check-environment"])
    parser.add_argument("--policy", default=".github/release-policy.json")
    parser.add_argument("--plan", default="release-plan.json")
    parser.add_argument("--reports", default="public-reports")
    args = parser.parse_args()
    policy = json.loads(Path(args.policy).read_text(encoding="utf-8"))
    validate_policy(policy)
    if args.command == "validate-policy":
        expected = policy["next_version"].removeprefix("v") + "-dev"
        if f'developmentVersion = "{expected}"' not in Path("internal/cli/version.go").read_text(encoding="utf-8"):
            raise ReleaseError("개발 버전과 next_version이 다릅니다")
        if not Path(policy["notes_path"]).is_file():
            raise ReleaseError("대상 버전의 릴리스 초안이 필요합니다")
        print("릴리스 정책 형식 확인 완료")
        return
    github = GitHub(os.environ["GITHUB_REPOSITORY"])
    if args.command == "check-environment":
        for channel in WORKFLOWS:
            approval_environment(github, policy, channel)
        print("정식 릴리스 승인 환경 확인 완료")
        return
    if args.command == "plan":
        channel = os.environ["RELEASE_CHANNEL"]
        if os.environ.get("GITHUB_REF") != "refs/heads/" + policy["branches"][channel]:
            raise ReleaseError("채널에 맞는 preview, rc, main 브랜치에서 workflow를 실행해야 합니다")
        source = os.environ["RELEASE_SOURCE"]
        version = parse_tag(source)[0] if channel == "stable" else os.environ["RELEASE_VERSION"]
        plan = make_plan(github, policy, channel, version, source, os.environ.get("RELEASE_OVERRIDE_REASON", ""))
        review_files(plan, policy, ".")
        Path(args.plan).write_text(json.dumps(plan, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        if output := os.environ.get("GITHUB_OUTPUT"):
            with open(output, "a") as stream:
                stream.write(f"sha={plan['source_sha']}\nversion={plan['version']}\n")
        if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
            with open(summary, "a") as stream:
                stream.write(f"출시 계획: `{plan['version']}` / `{plan['source_sha']}`\n\n"
                             f"관찰 기간 단축 사유: {plan['observation_override_reason'] or '없음'}\n")
    else:
        plan = json.loads(Path(args.plan).read_text(encoding="utf-8"))
        if args.command == "tag":
            publish(github, policy, plan, os.environ["VERIFIED_SHA"], args.reports)
        else:
            finish(github, policy, plan, args.reports)


if __name__ == "__main__":
    try:
        main()
    except (ReleaseError, KeyError, ValueError) as exc:
        print(f"릴리스 중단: {exc}", file=sys.stderr)
        sys.exit(1)
