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
        if path.startswith("git/matching-refs/tags/"):
            return []
        if path.startswith("actions/workflows/preview.yml/runs?"):
            return {"workflow_runs": []}
        if path == "git/commits/" + SHA:
            return {"tree": {"sha": "b" * 40}}
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
        manifest = {"schema_version": "spine.release-manifest/v1", "repository": "NARUBROWN/spine", "module": module, "kind": "core", "source_tree": "b" * 40,
                    "version": item["version"], "source_sha": SHA, "channel": "preview", "branch": "preview",
                    "components": {"core_version": item["version"]}, "workflow_run_id": "10", "workflow_run_attempt": "1",
                    "validation": {"result": "passed", "scope": "core", "version": item["version"], "source_sha": SHA,
                                   "components": {"core_version": item["version"]}, "selected_modules": {module: item["version"]}}}
        run = {"id": 10, "run_attempt": 1, "status": "completed", "conclusion": "success",
               "event": "workflow_dispatch", "head_sha": SHA, "head_branch": "preview",
               "path": ".github/workflows/preview.yml"}
        artifact = {"workflow_run": {"id": 10, "head_sha": SHA}}
        github = FakeGitHub("NARUBROWN/spine")
        github.candidate_evidence = lambda tag, prefix, **kwargs: {"manifest": manifest, "workflow_run": run, "artifact": artifact}
        train.check_existing(github, module, item, core_version=item["version"])
        run["conclusion"] = "failure"
        with self.assertRaises(release.ReleaseError):
            train.check_existing(github, module, item, core_version=item["version"])

    def test_existing_adapter_can_use_older_core_without_republication(self):
        module = "github.com/NARUBROWN/spine-nats-platform"
        item = sample()[module]
        old_core = "v0.7.0-preview.2"
        components = {"core_version": old_core}
        manifest = {"schema_version": "spine.release-manifest/v1", "repository": "NARUBROWN/spine-nats-platform",
                    "module": module, "kind": "platform", "source_tree": "b" * 40,
                    "version": item["version"], "source_sha": SHA, "channel": "preview", "branch": "preview",
                    "components": components, "workflow_run_id": "10", "workflow_run_attempt": "1",
                    "validation": {"result": "passed", "scope": "platform", "version": item["version"],
                                   "source_sha": SHA, "components": components,
                                   "selected_modules": {module: item["version"], release.CORE_MODULE: old_core}}}
        run = {"id": 10, "run_attempt": 1, "status": "completed", "conclusion": "success",
               "event": "workflow_dispatch", "head_sha": SHA, "head_branch": "preview",
               "path": ".github/workflows/preview.yml"}
        github = FakeGitHub("NARUBROWN/spine-nats-platform")
        github.candidate_evidence = lambda tag, prefix, **kwargs: {"manifest": manifest, "workflow_run": run,
                                                         "artifact": {"workflow_run": {"id": 10, "head_sha": SHA}}}
        train.check_existing(github, module, item, core_version="v0.7.0-preview.8")

    def test_existing_adapter_without_old_artifact_can_be_retested(self):
        module = "github.com/NARUBROWN/spine-nats-platform"
        github = FakeGitHub("NARUBROWN/spine-nats-platform")
        github.candidate_evidence = lambda tag, prefix, **kwargs: None
        train.check_existing(github, module, sample()[module], core_version="v0.7.0-preview.8")

    def test_reused_adapter_does_not_require_matching_head_or_new_policy(self):
        class ReusedGitHub(FakeGitHub):
            def tag_sha(self, tag):
                return SHA if self.repository.endswith("/spine-nats-platform") else None
            def api(self, path):
                if self.repository.endswith("/spine-nats-platform"):
                    if path == "git/matching-refs/tags/v":
                        return [{"ref": "refs/tags/v0.2.0-preview.1"}]
                    raise AssertionError("기존 태그 재사용에 현재 브랜치 정책은 필요하지 않습니다")
                return super().api(path)
        with patch.object(train, "check_existing") as check:
            train.validate_plan(sample(), ReusedGitHub)
        self.assertEqual(check.call_count, 1)
        self.assertEqual(check.call_args.args[1], "github.com/NARUBROWN/spine-nats-platform")
        with patch.object(train, "latest_preview_tag", return_value="v0.2.0-preview.2"), self.assertRaisesRegex(release.ReleaseError, "최신 Preview"):
            train.validate_plan(sample(), ReusedGitHub)

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
            train.dispatch_and_wait(release.CORE_MODULE, item, core_version=item["version"], github_factory=lambda _: github)

    def test_reused_sequence_or_queued_release_stops_before_dispatch(self):
        for condition in ("sequence", "queued"):
            with self.subTest(condition=condition):
                class BusyGitHub(FakeGitHub):
                    def api(self, path):
                        if self.repository.endswith("/spine-nats-platform"):
                            if condition == "sequence" and path.startswith("git/matching-refs/tags/"):
                                return [{"ref": "refs/tags/v0.2.0-preview.3"}]
                            if condition == "queued" and path.startswith("actions/workflows/preview.yml/runs?"):
                                return {"workflow_runs": [{"status": "queued"}]}
                        return super().api(path)
                seen = []
                with self.assertRaises(release.ReleaseError):
                    train.run_train(sample(), execute=True, github_factory=BusyGitHub,
                                    dispatch=lambda module, item, **kwargs: seen.append(module))
                self.assertEqual(seen, [])

    def test_successful_dispatch_waits_for_run_tag_and_proof(self):
        module = "github.com/NARUBROWN/spine-nats-platform"
        item = sample()[module]
        class RunGitHub(FakeGitHub):
            def __init__(self, repository):
                super().__init__(repository)
                self.dispatched = False
                self.queries = 0
            def tag_sha(self, tag):
                return SHA if self.dispatched and self.queries >= 2 else None
            def api(self, path, **kwargs):
                if kwargs.get("method") == "POST":
                    self.dispatched = True
                    self.sent = kwargs["data"]
                    return None
                if path.startswith("actions/workflows/preview.yml/runs?"):
                    self.queries += 1
                    return {"workflow_runs": [{"id": 42, "head_sha": SHA, "status": "in_progress"}] if self.dispatched else []}
                if path == "actions/runs/42":
                    return {"status": "completed", "conclusion": "success"}
                return super().api(path)
        github = RunGitHub(module.removeprefix("github.com/"))
        with patch.object(train, "check_existing") as proof:
            train.dispatch_and_wait(module, item, core_version="v0.7.0-preview.8", github_factory=lambda _: github,
                                    sleep=lambda _: None)
        self.assertEqual(github.sent, {"ref": "preview", "inputs": item})
        proof.assert_called_once_with(github, module, item, core_version="v0.7.0-preview.8")


if __name__ == "__main__":
    unittest.main()
