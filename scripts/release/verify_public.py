#!/usr/bin/env python3
"""격리한 공개 설치에서 실제 배포 CLI와 기본 생성 레시피를 검사합니다."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import socket
import subprocess
import tempfile
import time
import tomllib
import urllib.request

from release import ReleaseError


MODULE = "github.com/NARUBROWN/spine-cli"
ENTRYPOINTS = {"spine": MODULE + "/cmd/spine", "spine-cli": MODULE}


def execute(args, directory, env, timeout=600):
    print("[public] " + " ".join(map(str, args)), flush=True)
    result = subprocess.run(args, cwd=directory, env=env, capture_output=True, text=True, timeout=timeout, encoding="utf-8")
    if result.returncode:
        raise ReleaseError(result.stdout + result.stderr)
    return result.stdout.strip()


def envelope(cli, args, directory, env):
    result = json.loads(execute([cli, *args, "--json"], directory, env))
    if result.get("schema_version") != "spine.cli-result/v1" or not result.get("ok"):
        raise ReleaseError("CLI 실행 계약을 확인할 수 없습니다")
    return result["result"]


def module_graph(contents):
    result = []
    decoder = json.JSONDecoder()
    while contents.strip():
        contents = contents.lstrip()
        item, end = decoder.raw_decode(contents)
        result.append(item)
        contents = contents[end:]
    return result


def assert_modules(graph, required):
    selected = {m["Path"]: m for m in graph}
    if any(m.get("Replace") for m in graph):
        raise ReleaseError("공개 설치 검증에는 replace를 사용할 수 없습니다")
    for name, version in required.items():
        if selected.get(name, {}).get("Version") != version:
            raise ReleaseError("생성 레시피의 실제 모듈 버전이 다릅니다: " + name)


def composition_cases(plan):
    cases = {"http": []}
    if plan.get("upstream_evidence") is not None:
        extra = ["--validator", "playground", "--websocket-platform", "gorilla",
                 "--event-platform", "kafka", "--event-platform", "rabbitmq", "--event-platform", "nats"]
        cases.update({"echo-full": ["--platform", "echo", *extra],
                      "nethttp-full": ["--platform", "nethttp", *extra]})
    return cases


def direct_dependencies(module):
    return {item["Path"]: item["Version"] for item in module["Require"] if not item.get("Indirect", False)}


def expected_dependencies(required, recipe, upstream, case):
    allowed = dict(recipe["modules"])
    allowed.update({module: value.get("selected_version", value["version"]) for module, value in upstream.items()})
    for path, version in required.items():
        if allowed.get(path) != version:
            raise ReleaseError("기본 생성 의존성이 계획과 다릅니다: " + path)
    if case in ("echo-full", "nethttp-full"):
        selected = set(upstream)
        selected.remove("github.com/NARUBROWN/spine-nethttp-platform" if case == "echo-full"
                        else "github.com/NARUBROWN/spine-echo-platform")
        if not selected.issubset(required):
            raise ReleaseError("전체 플랫폼 조합에 필요한 모듈이 누락됐습니다")
    return allowed


def health(binary, app, env):
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    with tempfile.TemporaryFile() as output:
        process = subprocess.Popen([str(binary)], cwd=app, env=dict(env, SPINE_PORT=str(port)),
                                   stdout=output, stderr=output)
        try:
            for _ in range(100):
                if process.poll() is not None:
                    break
                try:
                    with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=1) as response:
                        if response.status == 200:
                            return
                except OSError:
                    time.sleep(.1)
            raise ReleaseError("공개 설치 CLI가 생성한 앱의 health 확인 실패")
        finally:
            if process.poll() is None:
                if os.name == "nt":
                    process.kill()
                else:
                    process.send_signal(signal.SIGTERM)
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    raise ReleaseError("생성 앱이 종료 제한 시간을 초과했습니다")
            with socket.socket() as probe:
                probe.settimeout(1)
                if probe.connect_ex(("127.0.0.1", port)) == 0:
                    raise ReleaseError("생성 앱의 리스너가 계속 연결을 받습니다")
            with socket.socket() as listener:
                # 정상 종료 뒤 TCP TIME_WAIT를 살아 있는 리스너로 오인하지 않습니다.
                listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                try:
                    listener.bind(("127.0.0.1", port))
                except OSError as exc:
                    raise ReleaseError("생성 앱의 리스너가 해제되지 않았습니다") from exc


def verify(plan, query, work, env):
    module = json.loads(execute(["go", "list", "-m", "-json", MODULE + "@" + query], work, env))
    resolved = module["Version"]
    suffix = ".exe" if os.name == "nt" else ""
    for name, path in ENTRYPOINTS.items():
        execute(["go", "install", path + "@" + query], work, env)
        binary = str(Path(env["GOBIN"]) / (name + suffix))
        if execute([binary, "version"], work, env) != "spine-cli " + resolved.removeprefix("v"):
            raise ReleaseError("CLI 표시 버전이 실제 설치 버전과 다릅니다")
        build = execute(["go", "version", "-m", binary], work, env)
        if not any(line.split()[:3] == ["mod", MODULE, resolved] for line in build.splitlines()):
            raise ReleaseError("CLI 빌드 정보가 공개 모듈 버전과 다릅니다")
        for command in ("schema", "capabilities"):
            envelope(binary, [command], work, env)
    cli = str(Path(env["GOBIN"]) / ("spine" + suffix))
    recipe = plan["components"]["recipe"]
    cases = composition_cases(plan)
    upstream = plan.get("upstream_evidence") or {}
    for layout in ("legacy", "layered-v1"):
        for case, options in cases.items():
            name = layout + "-" + case
            args = ["new", name, "--module", "example.com/" + name, "--layout", layout, "--skip-install", *options]
            generated = envelope(cli, [*args, "--dry-run"], work, env)
            if not generated.get("base_hash") or not generated.get("diff"):
                raise ReleaseError("생성 계획에 검토할 diff와 base_hash가 없습니다")
            envelope(cli, [*args, "--if-match", generated["base_hash"]], work, env)
            app = work / name
            config = tomllib.loads((app / "spine.toml").read_text(encoding="utf-8"))
            if config.get("recipe") != recipe["id"]:
                raise ReleaseError("기본 생성 레시피가 정책과 다릅니다")
            mod = json.loads(execute(["go", "mod", "edit", "-json"], app, env))
            required = {m["Path"]: m["Version"] for m in mod["Require"]}
            allowed = expected_dependencies(required, recipe, upstream, case)
            execute(["go", "mod", "tidy"], app, env)
            execute(["go", "mod", "verify"], app, env)
            graph = module_graph(execute(["go", "list", "-m", "-json", "all"], app, env))
            expected = dict(required)
            for item in graph:
                if item["Path"] in allowed:
                    expected[item["Path"]] = allowed[item["Path"]]
            assert_modules(graph, expected)
            for command in (["context"], ["contract", "check"], ["verify"]):
                envelope(cli, [*command, "--root", str(app)], work, env)
            composition = app / "internal/application/application.go"
            original = hashlib.sha256(composition.read_bytes()).hexdigest()
            update = envelope(cli, ["update", "--root", str(app), "--dry-run"], work, env)
            envelope(cli, ["update", "--root", str(app), "--if-match", update["base_hash"]], work, env)
            if hashlib.sha256(composition.read_bytes()).hexdigest() != original:
                raise ReleaseError("update가 앱 소스를 변경했습니다")
            updated = json.loads(execute(["go", "mod", "edit", "-json"], app, env))
            updated_required = direct_dependencies(updated)
            expected_dependencies(updated_required, recipe, upstream, case)
            updated_graph = module_graph(execute(["go", "list", "-m", "-json", "all"], app, env))
            assert_modules(updated_graph, expected)
            binary = app / ("server" + suffix)
            execute(["go", "build", "-o", str(binary), "."], app, env)
            if case == "http":
                health(binary, app, env)
    rc_cohort = plan.get("rc_cohort")
    if rc_cohort:
        extra = ["--validator", "playground", "--websocket-platform", "gorilla",
                 "--event-platform", "kafka", "--event-platform", "rabbitmq", "--event-platform", "nats"]
        for layout in ("legacy", "layered-v1"):
            for provider in ("echo", "nethttp"):
                name = "rc-" + layout + "-" + provider
                args = ["new", name, "--module", "example.com/" + name, "--layout", layout,
                        "--channel", "rc", "--platform", provider, "--skip-install", *extra]
                generated = envelope(cli, [*args, "--dry-run"], work, env)
                envelope(cli, [*args, "--if-match", generated["base_hash"]], work, env)
                app = work / name
                config = tomllib.loads((app / "spine.toml").read_text(encoding="utf-8"))
                if config.get("dependency_channel") != "rc":
                    raise ReleaseError("생성 프로젝트가 RC 채널을 사용하지 않습니다")
                expected = {path: version for path, version in rc_cohort.items()
                            if path != "github.com/NARUBROWN/spine-" + ("nethttp" if provider == "echo" else "echo") + "-platform"}
                mod = json.loads(execute(["go", "mod", "edit", "-json"], app, env))
                required = direct_dependencies(mod)
                if any(required.get(path) != version for path, version in expected.items()):
                    raise ReleaseError("생성 프로젝트가 검증된 RC 조합을 선택하지 않았습니다")
                execute(["go", "mod", "tidy"], app, env)
                execute(["go", "mod", "verify"], app, env)
                assert_modules(module_graph(execute(["go", "list", "-m", "-json", "all"], app, env)), expected)
                for command in (["context"], ["contract", "check"], ["verify"]):
                    envelope(cli, [*command, "--root", str(app)], work, env)
                update = envelope(cli, ["update", "--root", str(app), "--dry-run"], work, env)
                envelope(cli, ["update", "--root", str(app), "--if-match", update["base_hash"]], work, env)
                assert_modules(module_graph(execute(["go", "list", "-m", "-json", "all"], app, env)), expected)
    report = {"result": "passed", "source_sha": plan["source_sha"], "query": query,
            "resolved_cli_version": resolved, "components": plan["components"],
            "os": {"Darwin": "darwin", "Windows": "windows", "Linux": "linux"}[platform.system()],
            "go_version": execute(["go", "version"], work, env), "entrypoints": list(ENTRYPOINTS),
            "checks": ["public installation and build provenance", "two layouts and default HTTP composition",
                       "default recipe and selected public modules", "dry-run/if-match and source-preserving update",
                       "static contracts and generated tests", "HTTP health and listener release"]}
    if rc_cohort:
        report["rc_cohort"] = rc_cohort
        report["rc_combination_checks"] = ["echo-full", "nethttp-full"]
    if plan.get("upstream_evidence") is not None:
        report["upstream_evidence"] = plan["upstream_evidence"]
        report["combination_checks"] = ["echo-full", "nethttp-full"]
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", default="release-plan.json")
    parser.add_argument("--query", required=True)
    parser.add_argument("--report", default="release-validation.json")
    args = parser.parse_args()
    plan = json.loads(Path(args.plan).read_text(encoding="utf-8"))
    if args.query not in (plan["source_sha"], plan["version"]):
        raise ReleaseError("발행 계획의 SHA 또는 버전만 설치할 수 있습니다")
    with tempfile.TemporaryDirectory(prefix="spine-cli-public-") as directory:
        work = Path(directory).resolve()
        env = dict(os.environ, GOWORK="off", GOFLAGS=" ", GOTOOLCHAIN="local",
                   GOPROXY="https://proxy.golang.org,direct", GOSUMDB="sum.golang.org",
                   GOBIN=str(work/"bin"), GOCACHE=str(work/"build-cache"), GOMODCACHE=str(work/"mod-cache"))
        Path(env["GOBIN"]).mkdir()
        report = verify(plan, args.query, work, env)
    Path(args.report).write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
