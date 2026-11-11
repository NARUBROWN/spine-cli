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
import zipfile


TAG = re.compile(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-(preview|rc)\.([1-9]\d*))?\Z")
SHA = re.compile(r"[0-9a-f]{40}\Z")
WORKFLOWS = {"preview": "preview.yml", "rc": "rc.yml", "stable": "release.yml"}
CORE_MODULE = "github.com/NARUBROWN/spine"
UPSTREAM_MODULES = tuple("github.com/NARUBROWN/" + name for name in (
    "spine", "spine-echo-platform", "spine-nethttp-platform", "spine-gorilla-platform",
    "spine-kafka-platform", "spine-rabbitmq-platform", "spine-nats-platform",
    "spine-validator-playground",
))


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
        raise ReleaseError("공개 시각이나 검증 기록 시각이 올바르지 않습니다") from exc


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
    for path, version in recipe["modules"].items():
        if version == "preview" and recipe["id"] == "platform-contracts-preview" and path.startswith("github.com/NARUBROWN/spine"):
            continue
        if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
            raise ReleaseError("Preview 레시피 외에는 고정 모듈 버전을 지정해야 합니다")
    if policy.get("notes_path") != "release/notes/" + policy["next_version"] + ".md":
        raise ReleaseError("대상 버전의 릴리스 초안 경로가 필요합니다")


class GitHub:
    def __init__(self, repository, *, token=None):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
            raise ReleaseError("저장소 이름이 올바르지 않습니다")
        self.repository = repository
        self.env = dict(os.environ, GH_TOKEN=token) if token else None

    def api(self, path, *, missing=False, method="GET", data=None):
        endpoint = f"repos/{self.repository}" + ("/" + path if path else "")
        args = ["gh", "api", "--method", method, endpoint]
        if data is not None:
            args += ["--input", "-"]
        result = subprocess.run(args, input=json.dumps(data) if data is not None else None,
                                text=True, capture_output=True, encoding="utf-8", env=self.env)
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

    def candidate_evidence(self, tag, prefix="release-manifest-", *, allow_absent=False):
        parse_tag(tag)
        name = prefix + tag
        artifacts = []
        for page in range(1, 101):
            items = self.api(f"actions/artifacts?name={name}&per_page=100&page={page}")["artifacts"]
            artifacts.extend(item for item in items if item["name"] == name)
            if len(items) < 100:
                break
        else:
            raise ReleaseError("후보 검증 기록 목록을 모두 확인하지 못했습니다")
        if allow_absent:
            artifacts = [item for item in artifacts if not item.get("expired", True)]
            if not artifacts:
                return None
        if len(artifacts) != 1 or artifacts[0].get("expired", True):
            raise ReleaseError("태그에 대응하는 검증 artifact가 없거나 중복, 만료됐습니다. 새 Preview가 필요합니다")
        artifact = artifacts[0]
        workflow_run = self.api(f"actions/runs/{int(artifact['workflow_run']['id'])}")
        with tempfile.TemporaryDirectory(prefix="spine-cli-release-manifest-") as directory:
            archive = Path(directory) / "manifest.zip"
            with archive.open("wb") as output:
                result = subprocess.run(["gh", "api", f"repos/{self.repository}/actions/artifacts/{int(artifact['id'])}/zip"],
                                        stdout=output, stderr=subprocess.PIPE, env=self.env)
            if result.returncode:
                raise ReleaseError("후보 검증 artifact를 다운로드하지 못했습니다: " + result.stderr.decode("utf-8", errors="replace").strip())
            if artifact.get("digest") != "sha256:" + hashlib.sha256(archive.read_bytes()).hexdigest():
                raise ReleaseError("후보 검증 artifact의 SHA-256이 일치하지 않습니다")
            try:
                with zipfile.ZipFile(archive) as package:
                    manifest = json.loads(package.read("release-manifest.json"))
            except (zipfile.BadZipFile, KeyError) as exc:
                raise ReleaseError("후보 검증 artifact에 올바른 manifest가 없습니다") from exc
        return {"artifact": artifact, "workflow_run": workflow_run, "manifest": manifest}


def verify_source_candidate(github, source, expected_channel, policy):
    _, channel, _ = parse_tag(source)
    if channel != expected_channel:
        raise ReleaseError(f"이 단계의 출처는 {expected_channel} 태그여야 합니다")
    evidence = github.candidate_evidence(source)
    artifact, workflow_run, manifest = evidence["artifact"], evidence["workflow_run"], evidence["manifest"]
    sha = github.tag_sha(source)
    if (manifest.get("schema_version") != "spine-cli.release-manifest/v1" or
            manifest.get("version") != source or manifest.get("channel") != channel or
            manifest.get("source_sha") != sha or not SHA.fullmatch(sha or "") or
            manifest.get("repository") != github.repository or
            manifest.get("branch") != policy["branches"][channel] or
            manifest.get("source_tree") != github.api(f"git/commits/{sha}")["tree"]["sha"] or
            manifest.get("validation", {}).get("result") != "passed" or
            manifest["validation"].get("source_sha") != sha or
            manifest["validation"].get("version") != source or
            manifest["validation"].get("components") != manifest.get("components")):
        raise ReleaseError("출처 태그와 검증 manifest가 일치하지 않습니다")
    artifact_run = artifact.get("workflow_run", {})
    if (workflow_run.get("conclusion") != "success" or workflow_run.get("status") != "completed" or
            workflow_run.get("event") != "workflow_dispatch" or
            workflow_run.get("head_branch") != policy["branches"][channel] or
            workflow_run.get("head_sha") != sha or
            str(workflow_run.get("id")) != manifest.get("workflow_run_id") or
            str(workflow_run.get("run_attempt")) != manifest.get("workflow_run_attempt") or
            artifact.get("name") != "release-manifest-" + source or artifact.get("expired", True) or
            artifact_run.get("id") != workflow_run.get("id") or
            artifact_run.get("head_sha") != sha or artifact_run.get("head_branch") != policy["branches"][channel] or
            workflow_run.get("path", "").split("@")[0] != f".github/workflows/{WORKFLOWS[channel]}"):
        raise ReleaseError("출처 태그의 검증 workflow와 artifact가 일치하지 않거나 성공하지 않았습니다")
    if manifest.get("required_public_os") != policy["required_public_os"]:
        raise ReleaseError("출처 후보의 공개 설치 OS가 현재 정책과 다릅니다")
    validate_reports(manifest["validation"].get("reports", []), manifest, source)
    return artifact, manifest


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


def matches_preview_ci(github, workflow_run, source_sha):
    return (workflow_run.get("status") == "completed" and workflow_run.get("conclusion") == "success" and
            workflow_run.get("event") == "push" and workflow_run.get("head_branch") == "preview" and
            workflow_run.get("head_sha") == source_sha and
            (workflow_run.get("head_repository") or {}).get("full_name") == github.repository and
            workflow_run.get("path") == ".github/workflows/ci.yml")


def promotion_ci_candidate(github, policy, event_name, branch, source_sha, pull=None):
    upstream_branch = {"rc": "preview", "main": "rc"}.get(branch)
    if not upstream_branch or event_name not in ("pull_request", "push"):
        return None
    if event_name == "pull_request" and (
            pull["base"]["ref"] != branch or pull["head"]["ref"] != upstream_branch or
            (pull["head"].get("repo") or {}).get("full_name") != github.repository):
        return None

    commit = github.api(f"git/commits/{source_sha}")
    parents = commit.get("parents", [])
    if len(parents) != 2:
        return None
    upstream_sha = parents[1]["sha"]
    if pull is not None and upstream_sha != pull["head"]["sha"]:
        return None
    if commit["tree"]["sha"] != github.api(f"git/commits/{upstream_sha}")["tree"]["sha"]:
        return None

    prefix = f"{policy['next_version']}-{upstream_branch}."
    refs = github.api(f"git/matching-refs/tags/{prefix}")
    tags = [ref["ref"].removeprefix("refs/tags/") for ref in refs]
    for tag in sorted((tag for tag in tags if TAG.fullmatch(tag)), key=parse_tag, reverse=True):
        if github.tag_sha(tag) != upstream_sha:
            continue
        artifact, manifest = verify_source_candidate(github, tag, upstream_branch, policy)
        if manifest["components"] != components(policy):
            return None
        if event_name == "push":
            promotion(github, source_sha, upstream_sha, branch, upstream_branch)
        return tag, artifact
    return None


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


def latest_preview_tag(github):
    refs = github.api("git/matching-refs/tags/v")
    versions = []
    for ref in refs:
        tag = ref["ref"].removeprefix("refs/tags/")
        if TAG.fullmatch(tag) and parse_tag(tag)[1] == "preview":
            base, _, number = parse_tag(tag)
            versions.append((tuple(map(int, base[1:].split("."))), number, tag))
    if not versions:
        raise ReleaseError("Preview 태그가 없는 선행 저장소입니다: " + github.repository)
    return max(versions)[2]


def readme_only_preview_head(github, tagged_sha, selected_version):
    suffix = re.search(r"[.-]\d{14}-([0-9a-f]{12})\Z", selected_version)
    if not suffix:
        return None
    head = github.api("git/ref/heads/preview")["object"]["sha"]
    if not SHA.fullmatch(head) or not head.startswith(suffix[1]):
        return None
    comparison = github.api(f"compare/{tagged_sha}...{head}")
    readmes = {"README.md", "README.en.md", "README.ja.md", "README.zh-Hans.md", "README.zh-Hant.md"}
    files = comparison.get("files") or []
    # 검증된 태그 이후 기존 채널별 README만 수정됐는지 확인합니다.
    if (comparison.get("status") == "ahead" and comparison.get("merge_base_commit", {}).get("sha") == tagged_sha
            and 0 < len(files) <= len(readmes)
            and all(file.get("status") == "modified" and file.get("filename") in readmes for file in files)):
        return head
    return None


def upstream_evidence(recipe, github_factory=None):
    """코어 후보와 모든 플랫폼이 같은 코어를 공개 검증했는지 확인합니다."""
    if github_factory is None:
        token = os.environ.get("RELEASE_EVIDENCE_TOKEN", "")
        if not token.strip():
            raise ReleaseError("선행 저장소의 Actions 검증 기록을 읽을 RELEASE_EVIDENCE_TOKEN이 필요합니다")
        github_factory = lambda repository: GitHub(repository, token=token)
    snapshots = {}
    core_version = None
    for module in UPSTREAM_MODULES:
        repository = module.removeprefix("github.com/")
        dependency = github_factory(repository)
        tag = latest_preview_tag(dependency)
        sha = dependency.tag_sha(tag)
        if not SHA.fullmatch(sha or ""):
            raise ReleaseError("선행 저장소의 Preview 태그가 올바른 커밋을 가리키지 않습니다: " + repository)
        is_core = module == CORE_MODULE
        proof = dependency.candidate_evidence(tag, "core-candidate-" if is_core else "release-manifest-", allow_absent=not is_core)
        if proof is None:
            if is_core:
                raise ReleaseError("코어 후보의 발행 검증 기록이 없습니다")
            snapshots[module] = {"version": tag, "source_sha": sha, "artifact_id": None, "digest": None}
            continue
        artifact, run_info, manifest = proof["artifact"], proof["workflow_run"], proof["manifest"]
        validation = manifest.get("validation", {})
        components = manifest.get("components", {})
        scope = "core" if is_core else "platform"
        if (manifest.get("schema_version") != "spine.release-manifest/v1" or
                manifest.get("repository") != repository or manifest.get("module") != module or
                manifest.get("kind") != scope or manifest.get("channel") != "preview" or
                manifest.get("branch") != "preview" or manifest.get("version") != tag or
                manifest.get("source_sha") != sha or
                manifest.get("source_tree") != dependency.api("git/commits/" + sha)["tree"]["sha"] or
                validation.get("result") != "passed" or validation.get("scope") != scope or
                validation.get("version") != tag or validation.get("source_sha") != sha or
                validation.get("components") != components or
                validation.get("selected_modules", {}).get(module) != tag or
                run_info.get("status") != "completed" or run_info.get("conclusion") != "success" or
                run_info.get("event") != "workflow_dispatch" or run_info.get("head_branch") != "preview" or
                run_info.get("head_sha") != sha or run_info.get("path", "").split("@")[0] != ".github/workflows/preview.yml" or
                str(run_info.get("id")) != manifest.get("workflow_run_id") or
                str(run_info.get("run_attempt")) != manifest.get("workflow_run_attempt") or
                artifact.get("workflow_run", {}).get("id") != run_info.get("id") or
                artifact.get("workflow_run", {}).get("head_sha") != sha):
            raise ReleaseError("선행 발행의 태그와 공개 검증 기록이 다릅니다: " + repository)
        if is_core:
            if components.get("core_version") != tag:
                raise ReleaseError("코어 후보의 구성 버전이 태그와 다릅니다: " + repository)
            core_version = tag
        elif (not validation.get("selected_modules", {}).get(CORE_MODULE) or
              (components.get("core_version") and
               validation.get("selected_modules", {}).get(CORE_MODULE) != components["core_version"])):
            raise ReleaseError("선행 플랫폼의 코어 검증 기록이 일치하지 않습니다: " + repository)
        snapshots[module] = {"version": tag, "source_sha": sha, "artifact_id": artifact["id"], "digest": artifact["digest"]}
    for module, snapshot in snapshots.items():
        snapshot["selected_version"] = resolve_module_query(module, "preview")
    for module, version in recipe["modules"].items():
        if module not in snapshots:
            continue
        snapshot = snapshots[module]
        if snapshot["selected_version"] != version:
            raise ReleaseError("CLI 생성 레시피와 선행 태그가 다릅니다: " + module)
        if snapshot["version"] == version:
            continue
        dependency = github_factory(module.removeprefix("github.com/"))
        selected_sha = readme_only_preview_head(dependency, snapshot["source_sha"], version)
        if selected_sha is None:
            raise ReleaseError("CLI 생성 레시피와 선행 태그가 다릅니다: " + module)
        snapshot["selected_source_sha"] = selected_sha
    return snapshots


def make_plan(github, policy, channel, version, source, reason="", now=None):
    validate_policy(policy)
    now = now or dt.datetime.now(dt.timezone.utc)
    base, actual_channel, _ = parse_tag(version)
    if base != policy["next_version"] or channel != actual_channel:
        raise ReleaseError("대상 버전, 채널이 정책의 next_version과 일치하지 않습니다")
    if channel == "stable" and any(v == "preview" or "-" in v for v in policy["recipe"]["modules"].values()):
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
    release_components = components(policy)
    source_tag, upstream_sha, promotion_pr, source_evidence = "", "", None, None
    if channel == "preview":
        if not SHA.fullmatch(source) or source != source_sha:
            raise ReleaseError("Preview 출처에는 preview 브랜치 HEAD의 전체 커밋 SHA가 필요합니다")
    else:
        source_tag = source
        source_base, _, _ = parse_tag(source)
        if source_base != base:
            raise ReleaseError("다른 버전의 후보를 승격할 수 없습니다")
        expected = "preview" if channel == "rc" else "rc"
        artifact, manifest = verify_source_candidate(github, source, expected, policy)
        source_evidence = {"artifact_id": artifact["id"], "digest": artifact["digest"],
                           "workflow_run_id": manifest["workflow_run_id"], "created_at": artifact["created_at"]}
        upstream_sha = manifest["source_sha"]
        promotion_pr = promotion(github, source_sha, upstream_sha, branch, policy["branches"][expected])
        # 코드와 호환 버전 조합은 승인된 후보에서 그대로 이어받습니다.
        if manifest.get("components") != release_components:
            raise ReleaseError("후보 검증 뒤 호환 버전 조합이 바뀌었습니다. 새 Preview가 필요합니다")
        require_age(artifact["created_at"], policy[f"{expected}_hours"], now, reason)
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
            "upstream_source_sha": upstream_sha, "promotion_pr": promotion_pr, "components": release_components,
            "source_evidence": source_evidence,
            "observation_override_reason": reason, "planned_at": now.isoformat(),
            "workflow_run_id": os.environ.get("GITHUB_RUN_ID", ""),
            "workflow_run_attempt": os.environ.get("GITHUB_RUN_ATTEMPT", "1"),
            "repository": github.repository}
    plan["required_public_os"] = policy["required_public_os"]
    if channel == "rc" and policy.get("rc_cohort"):
        cohort = policy["rc_cohort"]
        if set(cohort) != set(UPSTREAM_MODULES) or any(not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+-rc\.[1-9][0-9]*", v) for v in cohort.values()):
            raise ReleaseError("RC 조합에는 코어와 일곱 플랫폼의 정확한 RC 태그가 필요합니다")
        plan["rc_cohort"] = cohort
    if source_evidence and not plan.get("rc_cohort") and not release_components["recipe"].get("queries"):
        plan["public_validation_scope"] = "tag-install"
    if channel == "preview" and policy["recipe"]["id"] == "platform-contracts-preview":
        plan["upstream_evidence"] = upstream_evidence(plan["components"]["recipe"])
    plan["notes_sha256"] = hashlib.sha256(notes(plan, policy).encode()).hexdigest()
    return plan


def resolve_module_query(path, query):
    with tempfile.TemporaryDirectory(prefix="spine-cli-preview-recipe-") as directory:
        env = dict(os.environ, GOWORK="off", GOFLAGS=" ", GOTOOLCHAIN="local")
        proxy = run("go", "env", "GOPROXY", cwd=directory, env=env)
        if proxy == "https://proxy.golang.org,direct":
            env["GOPROXY"] = "direct"
        try:
            value = json.loads(run("go", "list", "-m", "-json", path + "@" + query, cwd=directory, env=env))
        except ValueError as exc:
            raise ReleaseError("Preview 모듈 조회 결과가 올바른 JSON이 아닙니다") from exc
    version = value.get("Version", "")
    if value.get("Path") != path or value.get("Replace") or not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
        raise ReleaseError("Preview 모듈을 고정 버전으로 해석하지 못했습니다: " + path)
    return version


def components(policy):
    recipe = json.loads(json.dumps(policy["recipe"]))
    queries = {path: version for path, version in recipe["modules"].items() if version == "preview"}
    if queries:
        recipe["queries"] = queries
        for path, query in queries.items():
            recipe["modules"][path] = resolve_module_query(path, query)
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
            "상세 검증 결과는 release-manifest.json을 확인하세요.\n")


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
    return validate_reports(reports, plan, query)


def validate_reports(reports, plan, query):
    required = plan["required_public_os"]
    scope = plan.get("public_validation_scope")
    if scope == "tag-install" and (query != plan["version"] or not plan.get("source_evidence")):
        raise ReleaseError("태그 설치 검증에는 승격 근거와 정확한 새 태그가 필요합니다")
    if sorted(r.get("os") for r in reports) != sorted(required):
        raise ReleaseError("공개 설치 결과의 OS가 누락되거나 중복됐습니다")
    for report in reports:
        if (report.get("result") != "passed" or report.get("source_sha") != plan["source_sha"] or
                report.get("query") != query or report.get("components") != plan["components"] or
                report.get("scope") != scope or
                set(report.get("entrypoints", [])) != {"spine", "spine-cli"}):
            raise ReleaseError("공개 설치 결과가 발행 계획과 다릅니다")
        if scope == "tag-install":
            if report.get("resolved_cli_version") != query:
                raise ReleaseError("공개 설치가 발행한 태그를 확인하지 못했습니다")
            continue
        if plan.get("upstream_evidence") is not None and (
                report.get("upstream_evidence") != plan["upstream_evidence"] or
                report.get("combination_checks") != ["echo-full", "nethttp-full"]):
            raise ReleaseError("공개 설치 결과에 코어와 플랫폼의 조합 검증이 없습니다")
        if plan.get("rc_cohort") is not None and (
                report.get("rc_cohort") != plan["rc_cohort"] or
                report.get("rc_combination_checks") != ["echo-full", "nethttp-full"]):
            raise ReleaseError("RC 공개 설치 결과에 전체 RC 조합 검증이 없습니다")
    return reports



def publish(github, policy, plan, verified_sha, reports_directory):
    if plan.get("public_validation_scope") != "tag-install":
        collect_reports(reports_directory, plan, plan["source_sha"])
    if evidence := plan.get("preview_ci_evidence"):
        workflow_run = github.api(f"actions/runs/{int(evidence['id'])}")
        if (plan["channel"] != "preview" or not matches_preview_ci(github, workflow_run, plan["source_sha"]) or
                workflow_run.get("id") != evidence["id"] or workflow_run.get("run_attempt") != evidence["run_attempt"]):
            raise ReleaseError("Preview CI의 실행 기록이 발행 계획과 다릅니다")
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
    for key in ("source_sha", "source_tree", "branch", "promotion_pr", "source_evidence", "channel", "version", "components", "notes_sha256", "required_public_os", "upstream_evidence", "rc_cohort", "public_validation_scope"):
        if fresh.get(key) != plan.get(key):
            raise ReleaseError("승인한 계획이 바뀌었습니다. 새 실행이 필요합니다")
    github.api("git/refs", method="POST", data={"ref": f"refs/tags/{plan['version']}", "sha": verified_sha})


def finish(github, policy, plan, reports_directory, manifest_path="release-manifest.json"):
    reports = collect_reports(reports_directory, plan, plan["version"])
    if github.tag_sha(plan["version"]) != plan["source_sha"]:
        raise ReleaseError("공개 태그가 발행 계획과 다릅니다")
    if "upstream_evidence" in plan and upstream_evidence(plan["components"]["recipe"]) != plan["upstream_evidence"]:
        raise ReleaseError("태그 생성 후 선행 발행 근거가 바뀌었습니다")
    manifest = dict(plan, workflow_run_attempt=os.environ.get("GITHUB_RUN_ATTEMPT", "1"),
                    validation={"result": "passed", "version": plan["version"],
                                    "source_sha": plan["source_sha"], "components": plan["components"],
                                    "reports": reports})
    with tempfile.TemporaryDirectory(prefix="spine-cli-release-publish-") as directory:
        review_files(plan, policy, directory)
        path = Path(manifest_path).resolve()
        path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        # 후보는 태그와 Actions artifact로 검증 기록을 남기고 정식 버전만 GitHub Release를 공개합니다.
        if plan["channel"] != "stable":
            return
        args = ["gh", "release", "create", plan["version"], str(path), "--repo", github.repository,
                "--verify-tag", "--title", plan["version"], "--notes-file", str(Path(directory)/"release-notes.md"), "--latest"]
        run(*args)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["plan", "tag", "finish", "validate-policy", "check-environment", "check-promotion-ci"])
    parser.add_argument("--policy", default=".github/release-policy.json")
    parser.add_argument("--plan", default="release-plan.json")
    parser.add_argument("--reports", default="public-reports")
    parser.add_argument("--manifest", default="release-manifest.json")
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
    if args.command == "check-promotion-ci":
        event = os.environ["GITHUB_EVENT_NAME"]
        pull = None
        branch = os.environ["GITHUB_REF_NAME"]
        if event == "pull_request":
            payload = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text(encoding="utf-8"))
            pull = payload["pull_request"]
            branch = pull["base"]["ref"]
        try:
            evidence = promotion_ci_candidate(github, policy, event, branch, os.environ["GITHUB_SHA"], pull)
        except ReleaseError as exc:
            print(f"이전 검증을 재사용할 수 없어 전체 CI를 실행합니다. {exc}")
            return
        if evidence:
            candidate, artifact = evidence
            with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as stream:
                stream.write(f"candidate={candidate}\n")
            print(f"{candidate}의 검증 artifact {artifact['id']}를 재사용합니다.")
        return
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
        if channel == "preview":
            sha = plan["source_sha"]
            try:
                runs = github.api(f"actions/workflows/ci.yml/runs?branch=preview&head_sha={sha}&event=push&status=success")
            except ReleaseError as exc:
                print(f"이전 Preview CI를 조회하지 못해 전체 CI를 실행합니다. {exc}")
                runs = {"workflow_runs": []}
            for workflow_run in runs["workflow_runs"]:
                if matches_preview_ci(github, workflow_run, sha):
                    plan["preview_ci_evidence"] = {key: workflow_run[key] for key in ("id", "run_attempt")}
                    break
        review_files(plan, policy, ".")
        Path(args.plan).write_text(json.dumps(plan, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        if output := os.environ.get("GITHUB_OUTPUT"):
            with open(output, "a") as stream:
                stream.write(f"sha={plan['source_sha']}\nversion={plan['version']}\n")
                tag_only = "true" if plan.get("public_validation_scope") == "tag-install" else "false"
                reuse_ci = "true" if plan.get("preview_ci_evidence") else "false"
                reuse_common = "true" if plan["source_evidence"] else "false"
                stream.write(f"tag_only={tag_only}\nreuse_ci={reuse_ci}\nreuse_common={reuse_common}\n")
        if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
            with open(summary, "a") as stream:
                stream.write(f"출시 계획: `{plan['version']}` / `{plan['source_sha']}`\n\n"
                             f"관찰 기간 단축 사유: {plan['observation_override_reason'] or '없음'}\n")
                if evidence := plan.get("preview_ci_evidence"):
                    stream.write(f"같은 커밋의 Preview CI 실행 `{evidence['id']}`를 재사용합니다.\n")
                if evidence := plan["source_evidence"]:
                    stream.write(f"공통 CI 재사용 근거는 `{plan['source_tag']}`의 Actions 실행 "
                                 f"`{evidence['workflow_run_id']}`입니다.\n")
    else:
        plan = json.loads(Path(args.plan).read_text(encoding="utf-8"))
        if args.command == "tag":
            publish(github, policy, plan, os.environ["VERIFIED_SHA"], args.reports)
        else:
            finish(github, policy, plan, args.reports, args.manifest)


if __name__ == "__main__":
    try:
        main()
    except (ReleaseError, KeyError, ValueError) as exc:
        print(f"릴리스 중단: {exc}", file=sys.stderr)
        sys.exit(1)
