"""필수 외부 검증이 실제 실행돼 통과했는지 검사합니다."""

import json
from pathlib import Path
import sys

REQUIRED = {"TestGeneratedAssemblyBrokerE2E", "TestGeneratedAssemblyMinimumToolchain",
            "TestGeneratedNATSAssemblyE2E",
            "TestGeneratedNATSAssemblyE2E/layered-v1/echo", "TestGeneratedNATSAssemblyE2E/layered-v1/none",
            "TestGeneratedNATSAssemblyE2E/legacy/echo", "TestGeneratedNATSAssemblyE2E/legacy/none",
            "TestGeneratedNATSAssemblyE2E/minimum-go",
            "TestGeneratedDynamicPlatformE2E",
            "TestGeneratedDynamicPlatformE2E/layered-v1/echo", "TestGeneratedDynamicPlatformE2E/layered-v1/none",
            "TestGeneratedDynamicPlatformE2E/legacy/echo", "TestGeneratedDynamicPlatformE2E/legacy/none",
            "TestGeneratedAssemblyPublicMatrix", "TestGeneratedWebSocketPublicAssembly",
            "TestStructuredHTTPInputContracts", "TestRuntimeOpenAPIGeneratedApplications",
            "TestGeneratedFirstAPIGuideRunsAndDetectsRawReturn",
            "TestRunAndDevGracefulShutdown", "TestGeneratedHTTPShutdownDrainsRequests"}


def check(events):
    passed = {e.get("Test") for e in events if e.get("Action") == "pass"}
    skipped = {e["Test"] for e in events if e.get("Action") == "skip" and e.get("Test")}
    if (any(e.get("Action") == "fail" for e in events) or not REQUIRED <= passed or
            any(t == r or t.startswith(r + "/") for t in skipped for r in REQUIRED)):
        raise ValueError("필수 브로커, 최소 Go, 생성 앱 또는 종료 검사가 누락되거나 skip됐습니다")


if __name__ == "__main__":
    check([json.loads(line) for line in Path(sys.argv[1]).read_text(encoding="utf-8").splitlines() if line.strip()])
