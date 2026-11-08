import base64
import json
import unittest
from unittest.mock import patch

import release
import release_train as train

SHA = "a" * 40


class FakeGitHub:
    def __init__(self, repository):
        self.repository = repository

    def tag_sha(self, tag):
        return None

    def api(self, path):
        if path == "git/ref/heads/preview":
            return {"object": {"sha": SHA}}
        if path.startswith("contents/"):
            if self.repository.endswith("/spine"):
                next_version = "v0.7.0"
            elif self.repository.endswith("/spine-cli"):
                next_version = "v0.4.3"
            else:
                next_version = "v0.2.0"
            policy = {"next_version": next_version, "ci_core": SHA,
                      "channels": {"preview": {"core_version": "v0.7.0-preview.8"}}}
            return {"content": base64.b64encode(json.dumps(policy).encode()).decode()}
        raise AssertionError(path)


def sample():
    plan = {module: {"version": "v0.2.0-preview.1", "source_sha": SHA} for module in train.ORDER}
    plan[release.CORE_MODULE]["version"] = "v0.7.0-preview.8"
    plan[train.CLI]["version"] = "v0.4.3-preview.12"
    return plan


class ReleaseTrainTests(unittest.TestCase):
    def test_dispatches_core_then_all_platforms_then_cli(self):
        seen = []
        def dispatch(module, item, **kwargs):
            seen.append(module)
        train.run_train(sample(), execute=True, github_factory=FakeGitHub, dispatch=dispatch)
        self.assertEqual(seen, list(train.ORDER))
        self.assertEqual(seen[0], release.CORE_MODULE)
        self.assertEqual(seen[-1], train.CLI)

    def test_missing_adapter_stops_before_any_dispatch(self):
        plan = sample()
        del plan["github.com/NARUBROWN/spine-nats-platform"]
        with patch.object(train, "dispatch_and_wait") as dispatch, self.assertRaises(release.ReleaseError):
            train.run_train(plan, execute=True, github_factory=FakeGitHub, dispatch=dispatch)
        dispatch.assert_not_called()

    def test_wrong_core_or_changed_head_stops_before_dispatch(self):
        for condition in ("core", "head"):
            with self.subTest(condition=condition):
                plan = sample()
                if condition == "core":
                    plan[release.CORE_MODULE]["version"] = "v0.7.0-preview.9"
                else:
                    plan[train.CLI]["source_sha"] = "b" * 40
                seen = []
                with self.assertRaises(release.ReleaseError):
                    train.run_train(plan, execute=True, github_factory=FakeGitHub,
                                    dispatch=lambda module, item, **kwargs: seen.append(module))
                self.assertEqual(seen, [])

    def test_failure_stops_before_cli(self):
        seen = []
        def dispatch(module, item, **kwargs):
            seen.append(module)
            if module.endswith("spine-nats-platform"):
                raise release.ReleaseError("failed")
        with self.assertRaises(release.ReleaseError):
            train.run_train(sample(), execute=True, github_factory=FakeGitHub, dispatch=dispatch)
        self.assertNotIn(train.CLI, seen)

    def test_existing_tag_requires_matching_successful_provenance(self):
        module = release.CORE_MODULE
        item = sample()[module]
        manifest = {"schema_version": "spine.release-manifest/v1", "repository": "NARUBROWN/spine",
                    "version": item["version"], "source_sha": SHA, "channel": "preview", "branch": "preview",
                    "components": {}, "workflow_run_id": "10", "workflow_run_attempt": "1",
                    "validation": {"result": "passed", "version": item["version"], "source_sha": SHA, "components": {}}}
        run = {"id": 10, "run_attempt": 1, "status": "completed", "conclusion": "success",
               "event": "workflow_dispatch", "head_sha": SHA, "head_branch": "preview",
               "path": ".github/workflows/preview.yml"}
        artifact = {"workflow_run": {"id": 10, "head_sha": SHA}}
        github = FakeGitHub("NARUBROWN/spine")
        github.candidate_evidence = lambda tag, prefix: {"manifest": manifest, "workflow_run": run, "artifact": artifact}
        train.check_existing(github, module, item)
        run["conclusion"] = "failure"
        with self.assertRaises(release.ReleaseError):
            train.check_existing(github, module, item)

    def test_existing_publication_in_progress_prevents_dispatch(self):
        item = sample()[release.CORE_MODULE]
        github = FakeGitHub("NARUBROWN/spine")
        original_api = github.api
        def api(path, **kwargs):
            if path.startswith("actions/workflows/") and kwargs.get("method") != "POST":
                return {"workflow_runs": [{"id": 11, "status": "in_progress"}]}
            if kwargs.get("method") == "POST":
                self.fail("진행 중인 발행이 있는데 새 실행을 요청했습니다")
            return original_api(path)
        github.api = api
        with self.assertRaisesRegex(release.ReleaseError, "진행 중인"):
            train.dispatch_and_wait(release.CORE_MODULE, item, github_factory=lambda _: github)


if __name__ == "__main__":
    unittest.main()
