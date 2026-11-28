import json
import pathlib
import subprocess
import tempfile
import unittest

import release_automation as release


class VersionTests(unittest.TestCase):
    def test_strict_versions(self):
        for value in ("1.0.0", "v01.0.0", "v1.0", "v1.0.0\nINJECT=x", "v1.0.0-rc..1", "v1.0.0-01", "v1.0.0;echo bad"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                release.Version.parse(value)
        self.assertEqual(str(release.Version.parse("v1.2.3-rc.1")), "v1.2.3-rc.1")

    def test_patch_minor_and_major(self):
        current = release.Version.parse("v1.2.9")
        for bump, expected in (("patch", "v1.2.10"), ("minor", "v1.3.0"), ("major", "v2.0.0")):
            self.assertEqual(str(current.bump(bump)), expected)


class GitPlanTests(unittest.TestCase):
    def setUp(self):
        fixture = tempfile.TemporaryDirectory(prefix="pulso-release-test-")
        self.addCleanup(fixture.cleanup)
        root = pathlib.Path(fixture.name)
        self.remote = root / "remote.git"
        self.directory = root / "source"
        for extra in (("--bare", str(self.remote)), (str(self.directory),)):
            subprocess.run(["git", "init", "--initial-branch=main", *extra], check=True, capture_output=True)
        self.git = release.Git(self.directory)
        self.git.run("config", "user.name", "Test")
        self.git.run("config", "user.email", "fixture@example.invalid")
        self.git.run("remote", "add", "origin", str(self.remote))
        self.first = self.commit("initial")
        self.git.run("tag", "v0.2.9", self.first)
        self.sha = self.commit("change")

    def commit(self, text):
        (self.directory / "fixture").write_text(text)
        self.git.run("add", "fixture")
        self.git.run("commit", "-m", text)
        return self.git.resolve("HEAD")

    def plan(self, ref="refs/heads/main", sha=None, **kwargs):
        return release.plan_release(self.git, ref, sha or self.sha, **kwargs)

    def test_patch_reservation_resume_and_published_rerun(self):
        self.git.run("tag", "v0.2.10-rc.1", self.sha)
        self.git.run("tag", "v9.0.0-rc.1", self.sha)
        candidate = self.plan()
        self.assertEqual(candidate["version"], "v0.2.10")
        self.assertTrue(candidate["create_tag"])
        self.git.create_tag(candidate["version"], self.sha)
        remote = self.git.run("ls-remote", "origin", "refs/tags/v0.2.10^{}").stdout
        self.assertIn(self.sha, remote)
        resumed = self.plan()
        self.assertEqual(resumed["version"], candidate["version"])
        self.assertFalse(resumed["create_tag"])
        complete = self.plan(published=lambda version: version == candidate["version"])
        self.assertFalse(complete["publish"])
        self.assertFalse(complete["create_tag"])

    def test_minor_and_major_dispatch(self):
        self.assertEqual(self.plan(bump="minor")["version"], "v0.3.0")
        self.assertEqual(self.plan(bump="major")["version"], "v1.0.0")

    def test_manual_dispatch_can_bump_an_already_tagged_commit_and_resume(self):
        self.git.run("tag", "v0.2.10", self.sha)
        candidate = self.plan(bump="minor", intent="77")
        self.assertEqual(candidate["version"], "v0.3.0")
        self.git.create_tag(candidate["version"], self.sha, intent="77")
        resumed = self.plan(bump="minor", intent="77")
        self.assertEqual(resumed["version"], "v0.3.0")
        self.assertFalse(resumed["create_tag"])
        self.assertFalse(self.plan(bump="minor", intent="77", published=lambda _version: True)["publish"])
        self.assertEqual(self.plan(bump="minor", intent="78")["version"], "v0.4.0")

    def test_cli_creates_only_one_tag_and_writes_workflow_outputs(self):
        script = str(pathlib.Path(release.__file__).resolve())
        for attempt in range(2):
            output = self.directory / f"output-{attempt}"
            result = subprocess.run(
                ["python3", script, "plan", "--ref", "refs/heads/main", "--sha", self.sha,
                 "--create-tag", "--output", str(output)],
                cwd=self.directory, text=True, capture_output=True, check=True)
            plan = json.loads(result.stdout)
            self.assertEqual(plan["version"], "v0.2.10")
            self.assertEqual(plan["create_tag"], attempt == 0)
            self.assertEqual(dict(line.split("=", 1) for line in output.read_text().splitlines()),
                             {"version": "v0.2.10", "revision": self.sha, "publish": "true"})
        self.assertEqual(self.git.run("tag", "--list", "v0.2.10").stdout.strip(), "v0.2.10")

    def test_manual_tag_must_match_checkout(self):
        self.git.run("tag", "v1.0.0-rc.1", self.sha)
        self.assertEqual(self.plan("refs/tags/v1.0.0-rc.1")["version"], "v1.0.0-rc.1")
        with self.assertRaisesRegex(ValueError, "does not point"):
            self.plan("refs/tags/v0.2.9")

    def test_superseded_run_does_not_publish_older_code(self):
        newer = self.commit("newer")
        self.git.run("tag", "v0.2.10", newer)
        self.git.run("checkout", "--detach", self.sha)
        self.assertFalse(self.plan()["publish"])

    def test_divergent_history_and_wrong_checkout(self):
        with self.assertRaisesRegex(ValueError, "exact event commit"):
            self.plan(sha=self.first)
        self.git.run("checkout", "--orphan", "unrelated")
        divergent = self.commit("unrelated source")
        with self.assertRaisesRegex(ValueError, "diverges"):
            self.plan(sha=divergent)

    def test_branch_and_sha_injection(self):
        with self.assertRaises(ValueError):
            self.plan("refs/heads/feature")
        with self.assertRaises(ValueError):
            self.plan(sha="--help")
        with self.assertRaises(ValueError):
            self.plan(intent="77\nINJECT=x")

    def test_first_version(self):
        self.git.run("tag", "-d", "v0.2.9")
        self.assertEqual(self.plan()["version"], "v0.0.1")


class FakeGitHub:
    repository = "Example/Pulso"

    def __init__(self, version="v0.2.10", latest="v0.2.9"):
        self.current = {"id": 42, "tag_name": version, "draft": True,
                        "assets": [{"name": name, "state": "uploaded", "size": 12} for name in release.required_assets(version)]}
        self.latest = {"tag_name": latest} if latest else None
        self.patches = []

    def release(self, version):
        return self.current if self.current and self.current["tag_name"] == version else None

    def request(self, method, path, data=None):
        if method == "GET" and path == "/releases/latest":
            return self.latest
        if method == "PATCH" and path == "/releases/42":
            self.patches.append(data)
            self.current.update(data)
            return self.current
        raise AssertionError((method, path))


class Registry:
    def __init__(self):
        self.calls = []
        self.bad_variant = ""
        self.fail_promotion = False

    def __call__(self, *args):
        self.calls.append(args)
        if args[0] == "create":
            if self.fail_promotion:
                raise RuntimeError("registry unavailable")
            return ""
        manifest = {"digest": "sha256:" + "a" * 64,
                    "manifests": [{"platform": {"os": "linux", "architecture": arch}} for arch in ("amd64", "arm64")]}
        if self.bad_variant and args[1].endswith(self.bad_variant):
            manifest["manifests"].pop()
        return json.dumps(manifest)


class PublishTests(unittest.TestCase):
    def test_publish_after_both_variants_are_verified_and_promoted(self):
        github, registry = FakeGitHub(), Registry()
        release.publish_release("v0.2.10", github, registry)
        self.assertEqual([call[0] for call in registry.calls], ["inspect", "inspect", "create", "create"])
        self.assertIn("ghcr.io/example/pulso:latest", registry.calls[2])
        self.assertIn("ghcr.io/example/pulso:latest-python311", registry.calls[3])
        self.assertTrue(registry.calls[2][-1].startswith("ghcr.io/example/pulso@sha256:"))
        self.assertEqual(github.patches, [{"draft": False, "prerelease": False, "make_latest": "true"}])

    def test_missing_or_empty_assets_do_not_publish(self):
        for change in ("missing", "empty", "uploading"):
            with self.subTest(change=change):
                github, registry = FakeGitHub(), Registry()
                if change == "missing":
                    github.current["assets"].pop()
                elif change == "empty":
                    github.current["assets"][0]["size"] = 0
                else:
                    github.current["assets"][0]["state"] = "new"
                with self.assertRaisesRegex(ValueError, "assets"):
                    release.publish_release("v0.2.10", github, registry)
                self.assertFalse(github.patches)
                self.assertFalse(registry.calls)

    def test_missing_architecture_does_not_move_latest(self):
        github, registry = FakeGitHub(), Registry()
        registry.bad_variant = "-python311"
        with self.assertRaisesRegex(ValueError, "linux/arm64"):
            release.publish_release("v0.2.10", github, registry)
        self.assertEqual([call[0] for call in registry.calls], ["inspect", "inspect"])
        self.assertFalse(github.patches)

    def test_registry_failure_keeps_release_in_draft(self):
        github, registry = FakeGitHub(), Registry()
        registry.fail_promotion = True
        with self.assertRaises(RuntimeError):
            release.publish_release("v0.2.10", github, registry)
        self.assertTrue(github.current["draft"])
        self.assertFalse(github.patches)

    def test_prerelease_and_older_release_do_not_replace_latest(self):
        for version in ("v1.0.0-rc.1", "v0.2.8"):
            with self.subTest(version=version):
                github, registry = FakeGitHub(version), Registry()
                release.publish_release(version, github, registry)
                self.assertEqual([call[0] for call in registry.calls], ["inspect", "inspect"])
                self.assertEqual(github.patches[0]["make_latest"], "false")
                self.assertEqual(github.patches[0]["prerelease"], "-" in version)

    def test_repeat_publish_is_a_noop(self):
        github, registry = FakeGitHub(), Registry()
        github.current["draft"] = False
        release.publish_release("v0.2.10", github, registry)
        self.assertFalse(registry.calls)
        self.assertFalse(github.patches)


class ReleaseLookupTests(unittest.TestCase):
    """The real GitHub.release, which every other test replaces with a stub.

    Publishing has to find the draft the workflow uploaded to. GitHub answers
    404 when a draft is requested by tag, even though the reserved tag exists,
    so a lookup by tag alone never finds it and publishing stopped with
    "Create and upload the draft release before publishing".
    """

    def github(self, by_tag=None, pages=()):
        client = release.GitHub("Example/Pulso", "token")
        calls = []

        def request(method, path, data=None):
            calls.append(path)
            if path.startswith("/releases/tags/"):
                return by_tag
            if path.startswith("/releases?"):
                page = int(path.rsplit("page=", 1)[1])
                return list(pages[page - 1]) if page <= len(pages) else []
            raise AssertionError(path)

        client.request = request
        return client, calls

    def test_draft_is_found_by_listing(self):
        draft = {"id": 7, "tag_name": "v0.2.3", "draft": True}
        client, calls = self.github(pages=[[{"tag_name": "v0.2.1", "draft": False}, draft]])
        self.assertEqual(client.release("v0.2.3"), draft)
        self.assertEqual(calls, ["/releases/tags/v0.2.3", "/releases?per_page=100&page=1"])
        self.assertFalse(client.published("v0.2.3"), "a draft is not published")

    def test_published_release_needs_no_listing(self):
        published = {"id": 7, "tag_name": "v0.2.1", "draft": False}
        client, calls = self.github(by_tag=published)
        self.assertEqual(client.release("v0.2.1"), published)
        self.assertEqual(calls, ["/releases/tags/v0.2.1"])
        self.assertTrue(client.published("v0.2.1"))

    def test_draft_on_a_later_page_is_found(self):
        draft = {"id": 9, "tag_name": "v0.2.3", "draft": True}
        first = [{"tag_name": f"v0.1.{index}", "draft": False} for index in range(100)]
        client, calls = self.github(pages=[first, [draft]])
        self.assertEqual(client.release("v0.2.3"), draft)
        self.assertEqual(len(calls), 3)

    def test_unknown_version_is_absent(self):
        client, _ = self.github(pages=[[{"tag_name": "v0.2.1", "draft": False}]])
        self.assertIsNone(client.release("v9.9.9"))
        self.assertFalse(client.published("v9.9.9"))

    def test_publishing_reaches_a_draft_found_by_listing(self):
        draft = {"id": 42, "tag_name": "v0.2.3", "draft": True,
                 "assets": [{"name": name, "state": "uploaded", "size": 12}
                            for name in release.required_assets("v0.2.3")]}
        client, _ = self.github(pages=[[draft]])
        patches = []
        listing = client.request

        def request(method, path, data=None):
            if method == "GET" and path == "/releases/latest":
                return {"tag_name": "v0.2.1"}
            if method == "PATCH" and path == "/releases/42":
                patches.append(data)
                return draft
            return listing(method, path, data)

        client.request = request
        self.assertIn("Published v0.2.3", release.publish_release("v0.2.3", client, Registry()))
        self.assertEqual(patches, [{"draft": False, "prerelease": False, "make_latest": "true"}])


if __name__ == "__main__":
    unittest.main()
