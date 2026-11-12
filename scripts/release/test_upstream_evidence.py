import copy
import unittest
from unittest.mock import patch

import release


CORE = "v0.7.0-preview.8"
SHA = "a" * 40
TREE = "b" * 40


class Dependency:
    data = {}

    def __init__(self, repository):
        self.repository = repository
        self.module = "github.com/" + repository
        self.info = self.data[self.module]

    def api(self, path):
        if path == "git/ref/heads/preview":
            return {"object": {"sha": self.info.get("head", SHA)}}
        if path == "git/matching-refs/tags/v":
            return [{"ref": "refs/tags/" + self.info["tag"]}]
        if path == "git/commits/" + SHA:
            return {"tree": {"sha": TREE}}
        if path == f"compare/{SHA}...{self.info.get('head')}":
            return self.info["comparison"]
        raise AssertionError(path)

    def tag_sha(self, tag):
        return SHA

    def candidate_evidence(self, tag, prefix, *, allow_absent=False):
        self.info["requested_prefix"] = prefix
        return copy.deepcopy(self.info["proof"])


def fixtures():
    result = {}
    for index, module in enumerate(release.UPSTREAM_MODULES):
        is_core = module == release.CORE_MODULE
        tag = CORE if is_core else "v0.2.0-preview.1"
        scope = "core" if is_core else "platform"
        components = {"core_version": CORE, "modules": {}}
        validation = {"result": "passed", "scope": scope, "version": tag, "source_sha": SHA,
                      "components": components, "selected_modules": {module: tag, release.CORE_MODULE: CORE}}
        manifest = {"schema_version": "spine.release-manifest/v1", "repository": module.removeprefix("github.com/"),
                    "module": module, "kind": scope, "channel": "preview", "branch": "preview", "version": tag,
                    "source_sha": SHA, "source_tree": TREE, "workflow_run_id": "1", "workflow_run_attempt": "1",
                    "components": components, "validation": validation}
        artifact = {"id": index + 1, "digest": "sha256:abc", "workflow_run": {"id": 1, "head_sha": SHA}}
        run = {"id": 1, "run_attempt": 1, "status": "completed", "conclusion": "success",
               "event": "workflow_dispatch", "head_branch": "preview", "head_sha": SHA,
               "path": ".github/workflows/preview.yml"}
        result[module] = {"tag": tag, "proof": {"artifact": artifact, "workflow_run": run, "manifest": manifest}}
    return result


class UpstreamEvidenceTests(unittest.TestCase):
    def setUp(self):
        Dependency.data = fixtures()
        resolver = patch.object(release, "resolve_module_query", side_effect=lambda module, query: Dependency.data[module].get("selected_version", Dependency.data[module]["tag"]))
        resolver.start()
        self.addCleanup(resolver.stop)
        self.recipe = {"modules": {release.CORE_MODULE: CORE,
                                    "github.com/NARUBROWN/spine-echo-platform": "v0.2.0-preview.1"}}

    def check(self):
        return release.upstream_evidence(self.recipe, Dependency)

    def test_all_platforms_must_have_matching_public_evidence(self):
        result = self.check()
        self.assertEqual(set(result), set(release.UPSTREAM_MODULES))
        self.assertEqual(Dependency.data[release.CORE_MODULE]["requested_prefix"], "core-candidate-")
        self.assertIn("github.com/NARUBROWN/spine-nats-platform", result)

    def test_records_preview_commit_separately_from_tag_evidence(self):
        module = "github.com/NARUBROWN/spine-kafka-platform"
        selected = "v0.2.0-preview.1.0.20261108124442-0628cf02bee1"
        Dependency.data[module]["selected_version"] = selected
        result = self.check()
        self.assertEqual(result[module]["selected_version"], selected)
        self.assertEqual(result[module]["version"], "v0.2.0-preview.1")

    def test_accepts_existing_platform_built_with_old_core(self):
        proof = Dependency.data["github.com/NARUBROWN/spine-nats-platform"]["proof"]
        proof["manifest"]["components"]["core_version"] = "v0.7.0-preview.2"
        proof["manifest"]["validation"]["components"]["core_version"] = "v0.7.0-preview.2"
        proof["manifest"]["validation"]["selected_modules"][release.CORE_MODULE] = "v0.7.0-preview.2"
        self.assertIn("github.com/NARUBROWN/spine-nats-platform", self.check())

    def test_independent_platform_manifest_records_actual_contract_dependency(self):
        proof = Dependency.data["github.com/NARUBROWN/spine-nats-platform"]["proof"]
        proof["manifest"]["components"].pop("core_version")
        proof["manifest"]["validation"]["components"].pop("core_version", None)
        self.assertIn("github.com/NARUBROWN/spine-nats-platform", self.check())
        proof["manifest"]["validation"]["selected_modules"].pop(release.CORE_MODULE)
        with self.assertRaises(release.ReleaseError):
            self.check()

    def test_rejects_failed_workflow_or_inconsistent_previous_validation(self):
        for condition in ("workflow", "core"):
            with self.subTest(condition=condition):
                Dependency.data = fixtures()
                item = Dependency.data["github.com/NARUBROWN/spine-echo-platform"]
                if condition == "workflow":
                    item["proof"]["workflow_run"]["conclusion"] = "failure"
                else:
                    item["proof"]["manifest"]["validation"]["selected_modules"][release.CORE_MODULE] = "v0.7.0-preview.2"
                with self.assertRaises(release.ReleaseError):
                    self.check()

    def test_rejects_resolved_recipe_drift(self):
        self.recipe["modules"][release.CORE_MODULE] = "v0.7.0-preview.7"
        with self.assertRaisesRegex(release.ReleaseError, "레시피"):
            self.check()

    def set_readme_only_head(self, module=release.CORE_MODULE):
        item = Dependency.data[module]
        item["head"] = "c" * 40
        item["selected_version"] = item["tag"] + ".0.20261108124442-" + item["head"][:12]
        self.recipe["modules"][module] = item["selected_version"]
        item["comparison"] = {"status": "ahead", "merge_base_commit": {"sha": SHA},
                              "files": [{"status": "modified", "filename": "README.zh-Hans.md"}]}
        return item

    def test_accepts_readme_only_head_and_records_both_commits(self):
        item = self.set_readme_only_head()
        snapshot = self.check()[release.CORE_MODULE]
        self.assertEqual(snapshot["source_sha"], SHA)
        self.assertEqual(snapshot["selected_source_sha"], item["head"])
        self.assertEqual(snapshot["selected_version"], item["selected_version"])

    def test_accepts_only_listed_core_release_script_modifications(self):
        item = self.set_readme_only_head()
        files = item["comparison"]["files"]
        files[:] = [{"status": "modified", "filename": "scripts/release/" + name}
                    for name in ("release.py", "test_release.py", "verify_public.py")]
        snapshot = self.check()[release.CORE_MODULE]
        self.assertEqual(snapshot["selected_source_sha"], item["head"])
        files.append({"status": "modified", "filename": "scripts/release/other.py"})
        with self.assertRaisesRegex(release.ReleaseError, "레시피"):
            self.check()

    def test_accepts_only_listed_echo_release_script_modifications(self):
        module = "github.com/NARUBROWN/spine-echo-platform"
        item = self.set_readme_only_head(module)
        files = item["comparison"]["files"]
        files[:] = [{"status": "modified", "filename": path} for path in (
            "scripts/release/release.py",
            "scripts/release/verify_public.py",
            "test/release/test_release.py",
        )]
        self.assertEqual(self.check()[module]["selected_source_sha"], item["head"])
        files.append({"status": "modified", "filename": "scripts/release/test_release.py"})
        with self.assertRaisesRegex(release.ReleaseError, "레시피"):
            self.check()

    def test_rejects_a_source_change_even_with_readme_changes(self):
        item = self.set_readme_only_head()
        item["comparison"]["files"].append({"status": "modified", "filename": "go.mod"})
        with self.assertRaisesRegex(release.ReleaseError, "레시피"):
            self.check()

    def test_rejects_a_head_that_no_longer_matches_the_selected_version(self):
        item = self.set_readme_only_head()
        item["head"] = "d" * 40
        with self.assertRaisesRegex(release.ReleaseError, "레시피"):
            self.check()

    def test_rejects_readme_changes_on_an_unrelated_history(self):
        item = self.set_readme_only_head()
        item["comparison"]["status"] = "diverged"
        item["comparison"]["merge_base_commit"]["sha"] = "d" * 40
        with self.assertRaisesRegex(release.ReleaseError, "레시피"):
            self.check()

    def test_missing_old_adapter_proof_does_not_force_republication(self):
        Dependency.data["github.com/NARUBROWN/spine-nats-platform"]["proof"] = None
        result = self.check()
        self.assertIsNone(result["github.com/NARUBROWN/spine-nats-platform"]["artifact_id"])
        self.assertEqual(result["github.com/NARUBROWN/spine-nats-platform"]["version"], "v0.2.0-preview.1")


if __name__ == "__main__":
    unittest.main()
