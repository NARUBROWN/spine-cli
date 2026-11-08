import copy
import unittest

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
        raise AssertionError(path)

    def tag_sha(self, tag):
        return SHA

    def candidate_evidence(self, tag, prefix):
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
        self.recipe = {"modules": {release.CORE_MODULE: CORE,
                                    "github.com/NARUBROWN/spine-echo-platform": "v0.2.0-preview.1"}}

    def check(self):
        return release.upstream_evidence(self.recipe, Dependency)

    def test_all_platforms_must_have_matching_public_evidence(self):
        result = self.check()
        self.assertEqual(set(result), set(release.UPSTREAM_MODULES))
        self.assertEqual(Dependency.data[release.CORE_MODULE]["requested_prefix"], "core-candidate-")
        self.assertIn("github.com/NARUBROWN/spine-nats-platform", result)

    def test_rejects_platform_built_with_old_core(self):
        proof = Dependency.data["github.com/NARUBROWN/spine-nats-platform"]["proof"]
        proof["manifest"]["components"]["core_version"] = "v0.7.0-preview.2"
        proof["manifest"]["validation"]["components"]["core_version"] = "v0.7.0-preview.2"
        proof["manifest"]["validation"]["selected_modules"][release.CORE_MODULE] = "v0.7.0-preview.2"
        with self.assertRaisesRegex(release.ReleaseError, "현재 코어"):
            self.check()

    def test_rejects_unpublished_head_and_failed_workflow(self):
        for condition in ("head", "workflow"):
            with self.subTest(condition=condition):
                Dependency.data = fixtures()
                item = Dependency.data["github.com/NARUBROWN/spine-echo-platform"]
                if condition == "head":
                    item["head"] = "c" * 40
                else:
                    item["proof"]["workflow_run"]["conclusion"] = "failure"
                with self.assertRaises(release.ReleaseError):
                    self.check()

    def test_rejects_resolved_recipe_drift(self):
        self.recipe["modules"][release.CORE_MODULE] = "v0.7.0-preview.7"
        with self.assertRaisesRegex(release.ReleaseError, "레시피"):
            self.check()


if __name__ == "__main__":
    unittest.main()
