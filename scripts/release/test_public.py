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
