"""설치 증거와 초기 전환이 실패를 숨기지 않는지 검사합니다."""

import copy
import json
from pathlib import Path
import unittest
from unittest.mock import patch

import check_tests
import core_regression
import configure_branches
import release
import verify_public


class PublicEvidenceTests(unittest.TestCase):
    def test_reused_platforms_are_checked_with_current_core(self):
        upstream = {name: {"version": "v0.2.0-preview.1"} for name in release.UPSTREAM_MODULES}
        upstream[release.CORE_MODULE]["version"] = "v0.7.0-preview.8"
        recipe = {"modules": {release.CORE_MODULE: "v0.7.0-preview.8"}}
        cases = verify_public.composition_cases({"upstream_evidence": upstream})
        self.assertEqual(list(cases), ["http", "echo-full", "nethttp-full"])
        required = {module: info["version"] for module, info in upstream.items()
                    if module != "github.com/NARUBROWN/spine-nethttp-platform"}
        allowed = verify_public.expected_dependencies(required, recipe, upstream, "echo-full")
        self.assertEqual(allowed[release.CORE_MODULE], "v0.7.0-preview.8")
        bad = dict(required, **{release.CORE_MODULE: "v0.7.0-preview.7"})
        with self.assertRaises(release.ReleaseError):
            verify_public.expected_dependencies(bad, recipe, upstream, "echo-full")
        del required["github.com/NARUBROWN/spine-nats-platform"]
        with self.assertRaises(release.ReleaseError):
            verify_public.expected_dependencies(required, recipe, upstream, "echo-full")

    def test_updated_module_distinguishes_indirect_dependencies(self):
        module = {"Require": [{"Path": "example.com/core", "Version": "v1.0.0"},
                              {"Path": "example.com/transitive", "Version": "v2.0.0", "Indirect": True}]}
        self.assertEqual(verify_public.direct_dependencies(module), {"example.com/core": "v1.0.0"})

    def test_expired_adapter_artifact_can_be_replaced_by_new_combination_check(self):
        github = release.GitHub("NARUBROWN/spine-nats-platform")
        tag = "v0.1.0-preview.1"
        for artifacts in ([], [{"name": "release-manifest-" + tag, "expired": True}]):
            with self.subTest(artifacts=artifacts), patch.object(github, "api", return_value={"artifacts": artifacts}):
                self.assertIsNone(github.candidate_evidence(tag, allow_absent=True))
                with self.assertRaises(release.ReleaseError):
                    github.candidate_evidence(tag)
        with patch.object(github, "api", return_value={"artifacts": [
                {"name": "release-manifest-" + tag, "expired": False},
                {"name": "release-manifest-" + tag, "expired": False}]}):
            with self.assertRaises(release.ReleaseError):
                github.candidate_evidence(tag, allow_absent=True)

    def test_release_rejects_missing_or_mismatched_combination_evidence(self):
        plan = {"required_public_os": ["linux", "darwin", "windows"], "source_sha": "a" * 40,
                "components": {"recipe": "pinned"}, "upstream_evidence": {"platform": {"version": "v1.0.0"}}}
        reports = [{"os": os, "result": "passed", "source_sha": plan["source_sha"], "query": "v1.0.0",
                    "components": plan["components"], "entrypoints": ["spine", "spine-cli"],
                    "upstream_evidence": plan["upstream_evidence"], "combination_checks": ["echo-full", "nethttp-full"]}
                   for os in plan["required_public_os"]]
        release.validate_reports(reports, plan, "v1.0.0")
        for key, value in (("combination_checks", ["echo-full"]), ("upstream_evidence", {})):
            bad = copy.deepcopy(reports)
            bad[0][key] = value
            with self.assertRaises(release.ReleaseError):
                release.validate_reports(bad, plan, "v1.0.0")

    def test_core_regression_excludes_only_declared_optional_tests(self):
        names = sorted(core_regression.OPTIONAL) + ["TestCoreBehavior"]
        pattern = core_regression.selection("\n".join(names))
        self.assertIn("TestCoreBehavior", pattern)
        self.assertNotIn("TestGeneratedNATSAssemblyE2E", pattern)
        with self.assertRaises(ValueError):
            core_regression.selection("TestCoreBehavior")
        events = [{"Action": "pass", "Test": name} for name in check_tests.REQUIRED
                  if name.split("/")[0] not in core_regression.OPTIONAL]
        check_tests.check(events, core=True)
        with self.assertRaises(ValueError):
            check_tests.check(events, core=False)

    def test_module_replacements_and_wrong_versions_are_rejected(self):
        graph = verify_public.module_graph('{"Path":"example.com/a","Version":"v1.0.0"}\n{"Path":"example.com/b","Version":"v2.0.0"}')
        verify_public.assert_modules(graph, {"example.com/a": "v1.0.0"})
        with self.assertRaises(release.ReleaseError):
            verify_public.assert_modules(graph, {"example.com/a": "v1.0.1"})
        graph[1]["Replace"] = {"Dir": "../local"}
        with self.assertRaises(release.ReleaseError):
            verify_public.assert_modules(graph, {"example.com/a": "v1.0.0"})

    def test_required_test_skip_or_absence_is_failure(self):
        events = [{"Action": "pass", "Test": name} for name in check_tests.REQUIRED]
        check_tests.check(events)
        check_tests.check(events + [{"Action": "skip", "Package": "example.com/empty"}])
        with self.assertRaises(ValueError):
            check_tests.check(events + [{"Action": "fail", "Package": "example.com/broken"}])
        with self.assertRaises(ValueError):
            check_tests.check(events[:-1])
        with self.assertRaises(ValueError):
            check_tests.check(events + [{"Action": "skip", "Test": "TestGeneratedAssemblyBrokerE2E/legacy"}])

    def test_bootstrap_never_moves_existing_channel(self):
        class GitHub:
            def __init__(self): self.writes = []
            def api(self, path, method="GET", data=None, missing=False):
                if method != "GET": self.writes.append(data); return {}
                if path.startswith("contents/"): return {}
                return {"object": {"sha": "a"*40 if path.endswith("main") else "b"*40}}
        github = GitHub()
        with self.assertRaises(release.ReleaseError):
            configure_branches.bootstrap(github, "a"*40)
        self.assertEqual(github.writes, [])

    def test_bootstrap_creates_only_missing_refs_after_all_prechecks(self):
        class GitHub:
            def __init__(self): self.writes = []
            def api(self, path, method="GET", data=None, missing=False):
                if method != "GET": self.writes.append(data); return {}
                if path.startswith("contents/"): return {}
                if path.endswith("main"): return {"object": {"sha": "a"*40}}
                return None
        github = GitHub()
        configure_branches.bootstrap(github, "a"*40)
        self.assertEqual(github.writes, [{"ref": "refs/heads/preview", "sha": "a"*40}, {"ref": "refs/heads/rc", "sha": "a"*40}])

    def test_tag_protection_does_not_block_initial_creation(self):
        types = {rule["type"] for rule in configure_branches.tag_specification()["rules"]}
        self.assertEqual(types, {"update", "deletion"})
        actual = copy.deepcopy(configure_branches.specification("main"))
        actual["bypass_actors"] = [{"actor_type": "RepositoryRole", "actor_id": 5, "bypass_mode": "always"}]
        with self.assertRaises(release.ReleaseError):
            configure_branches.verify_rule(actual, configure_branches.specification("main"))

    def test_cli_protocol_failure_is_not_a_successful_smoke_test(self):
        for data in [{"ok": False, "schema_version": "spine.cli-result/v1"}, {"ok": True, "schema_version": "wrong"}]:
            with patch.object(verify_public, "execute", return_value=json.dumps(data)):
                with self.assertRaises(release.ReleaseError):
                    verify_public.envelope("spine", ["schema"], Path("."), {})


if __name__ == "__main__":
    unittest.main()
