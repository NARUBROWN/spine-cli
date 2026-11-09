"""선택형 공개 어댑터 E2E를 제외한 CLI 회귀를 실패 게이트로 실행합니다."""

import re
import subprocess
import sys

OPTIONAL = {
    "TestGeneratedAssemblyPublicMatrix", "TestGeneratedAssemblyBrokerE2E",
    "TestGeneratedAssemblyMinimumToolchain", "TestGeneratedNATSAssemblyE2E",
    "TestGeneratedDynamicPlatformE2E", "TestGeneratedWebSocketPublicAssembly",
    "TestResolvePreviewModulesIncludesNATS", "TestLayeredGeneratedProjectEndToEnd",
    "TestRuntimeOpenAPIGeneratedApplications", "TestGeneratedProjectsHandleHTTPInputErrors",
    "TestDynamicHTTPAndWebSocketComposition",
}


def selection(output):
    names = [line for line in output.splitlines() if re.fullmatch(r"Test[A-Za-z0-9_]+", line)]
    missing = OPTIONAL - set(names)
    if missing or not names:
        raise ValueError("선택형 검사 목록과 테스트 목록이 다릅니다: " + ", ".join(sorted(missing)))
    return "^(" + "|".join(name for name in names if name not in OPTIONAL) + ")$"


def main():
    listing = subprocess.check_output(["go", "test", "./internal/cli", "-list", "^Test"], text=True)
    pattern = selection(listing)
    with open("core-regression.jsonl", "w", encoding="utf-8") as log:
        result = subprocess.run(["go", "test", "-race", "-json", "./...", "-run", pattern,
                                 "-count=1", "-timeout=30m"], stdout=log)
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())
