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
    cases = {"http": [], "websocket": ["--websocket-platform", "gorilla"],
             "all": ["--websocket-platform", "gorilla", "--event-platform", "kafka",
                     "--event-platform", "rabbitmq", "--validator", "playground"],
             "worker": ["--platform", "none", "--event-platform", "kafka"]}
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
            for path, version in required.items():
                if recipe["modules"].get(path) != version:
                    raise ReleaseError("기본 생성 의존성이 정책과 다릅니다: " + path)
            execute(["go", "mod", "tidy"], app, env)
            execute(["go", "mod", "verify"], app, env)
            graph = module_graph(execute(["go", "list", "-m", "-json", "all"], app, env))
            expected = dict(required)
            for item in graph:
                if item["Path"] in recipe["modules"]:
                    expected[item["Path"]] = recipe["modules"][item["Path"]]
            assert_modules(graph, expected)
            for command in (["context"], ["contract", "check"], ["verify"]):
                envelope(cli, [*command, "--root", str(app)], work, env)
            composition = app / "internal/application/application.go"
            original = hashlib.sha256(composition.read_bytes()).hexdigest()
            update = envelope(cli, ["update", "--root", str(app), "--dry-run"], work, env)
            envelope(cli, ["update", "--root", str(app), "--if-match", update["base_hash"]], work, env)
            if hashlib.sha256(composition.read_bytes()).hexdigest() != original:
                raise ReleaseError("update가 앱 소스를 변경했습니다")
            if case == "http":
                binary = app / ("server" + suffix)
                execute(["go", "build", "-o", str(binary), "."], app, env)
                health(binary, app, env)
    return {"result": "passed", "source_sha": plan["source_sha"], "query": query,
            "resolved_cli_version": resolved, "components": plan["components"],
            "os": {"Darwin": "darwin", "Windows": "windows", "Linux": "linux"}[platform.system()],
            "go_version": execute(["go", "version"], work, env), "entrypoints": list(ENTRYPOINTS),
            "checks": ["public installation and build provenance", "two layouts and four platform combinations",
                       "default recipe and selected public modules", "dry-run/if-match and source-preserving update",
                       "static contracts and generated tests", "HTTP health and listener release"]}


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
