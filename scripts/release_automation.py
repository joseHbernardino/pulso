#!/usr/bin/env python3
"""Plan automatic versions and publish completed releases; no third-party modules."""
import argparse
import dataclasses
import json
import os
import pathlib
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request


@dataclasses.dataclass(frozen=True)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: str = ""

    @classmethod
    def parse(cls, value):
        match = re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?", value)
        if not match:
            raise ValueError(f"Invalid release version: {value}")
        prerelease = match[4] or ""
        if prerelease and any(not part or (part.isdigit() and len(part) > 1 and part[0] == "0") for part in prerelease.split(".")):
            raise ValueError(f"Invalid prerelease: {value}")
        return cls(int(match[1]), int(match[2]), int(match[3]), prerelease)

    def __str__(self):
        value = f"v{self.major}.{self.minor}.{self.patch}"
        return value + ("-" + self.prerelease if self.prerelease else "")

    @property
    def numbers(self):
        return self.major, self.minor, self.patch

    def bump(self, part):
        if part == "major":
            return Version(self.major + 1, 0, 0)
        if part == "minor":
            return Version(self.major, self.minor + 1, 0)
        if part == "patch":
            return Version(self.major, self.minor, self.patch + 1)
        raise ValueError("bump must be patch, minor or major")


class Git:
    def __init__(self, directory="."):
        self.directory = directory

    def run(self, *args, check=True):
        result = subprocess.run(["git", *args], cwd=self.directory, text=True, capture_output=True)
        if check and result.returncode:
            raise RuntimeError(f"git {args[0]} failed: {result.stderr.strip()}")
        return result

    def resolve(self, ref):
        return self.run("rev-parse", "--verify", "--end-of-options", ref + "^{commit}").stdout.strip()

    def ancestor(self, first, second):
        result = self.run("merge-base", "--is-ancestor", first, second, check=False)
        if result.returncode not in (0, 1):
            raise RuntimeError("Could not verify release ancestry")
        return result.returncode == 0

    def versions(self):
        versions = []
        for tag in self.run("tag", "--list", "v*").stdout.splitlines():
            try:
                version = Version.parse(tag)
            except ValueError:
                continue
            if not version.prerelease:
                versions.append(version)
        return sorted(versions, key=lambda version: version.numbers)

    def tag_intent(self, version):
        ref = "refs/tags/" + str(version)
        if self.run("cat-file", "-t", ref).stdout.strip() != "tag":
            return ""
        message = self.run("for-each-ref", "--format=%(contents)", ref).stdout
        match = re.search(r"^Pulso release intent: ([0-9]+)$", message, re.MULTILINE)
        return match[1] if match else ""

    def create_tag(self, version, sha, intent=""):
        message = f"Pulso {version}" + (f"\n\nPulso release intent: {intent}" if intent else "")
        self.run("-c", "user.name=github-actions[bot]", "-c",
                 "user.email=41898282+github-actions[bot]@users.noreply.github.com",
                 "tag", "-a", str(version), sha, "-m", message)
        self.run("push", "origin", "refs/tags/" + str(version))


def plan_release(git, ref, sha, bump="patch", published=lambda _version: False, intent=""):
    if not re.fullmatch(r"[0-9a-f]{40,64}", sha) or git.resolve("HEAD") != sha:
        raise ValueError("Checkout must match the exact event commit")
    if intent and not re.fullmatch(r"[0-9]+", intent):
        raise ValueError("Release intent must be a numeric workflow run ID")
    create = False
    if ref.startswith("refs/tags/"):
        version = Version.parse(ref.removeprefix("refs/tags/"))
        if git.resolve(ref) != sha:
            raise ValueError("Release tag does not point to the event commit")
    elif ref == "refs/heads/main":
        versions = git.versions()
        if versions:
            latest_sha = git.resolve("refs/tags/" + str(versions[-1]))
            if not git.ancestor(latest_sha, sha):
                if git.ancestor(sha, latest_sha):
                    return {"version": "", "revision": sha, "publish": False,
                            "create_tag": False, "reason": "A newer commit already has a stable tag"}
                raise ValueError("main diverges from the latest stable tag; reconcile the history before releasing")
        existing = [version for version in versions if git.resolve("refs/tags/" + str(version)) == sha
                    and (not intent or git.tag_intent(version) == intent)]
        if existing:
            version = existing[-1]
        else:
            version = (versions[-1] if versions else Version(0, 0, 0)).bump(bump)
            create = True
    else:
        raise ValueError("Automatic releases and manual version bumps must run on main")
    complete = published(str(version))
    return {"version": str(version), "revision": sha, "publish": not complete,
            "create_tag": create and not complete,
            "reason": "Already published" if complete else "Resume or publish this commit"}


class GitHub:
    def __init__(self, repository, token):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository) or any(part in (".", "..") for part in repository.split("/")):
            raise ValueError("Set GITHUB_REPOSITORY to owner/repository")
        if not token:
            raise ValueError("Set GH_TOKEN to the workflow's GITHUB_TOKEN")
        self.repository, self.token = repository, token
        self.base = os.environ.get("GITHUB_API_URL", "https://api.github.com").rstrip("/")
        if urllib.parse.urlparse(self.base).scheme != "https":
            raise ValueError("GitHub API must use HTTPS")

    def request(self, method, path, data=None):
        request = urllib.request.Request(self.base + "/repos/" + self.repository + path, method=method,
            data=json.dumps(data).encode() if data is not None else None,
            headers={"Accept": "application/vnd.github+json", "Authorization": "Bearer " + self.token,
                     "Content-Type": "application/json", "X-GitHub-Api-Version": "2022-11-28"})
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            if method == "GET" and error.code == 404:
                return None
            raise RuntimeError(f"GitHub {method} {path}: HTTP {error.code}") from None

    def release(self, version):
        return self.request("GET", "/releases/tags/" + urllib.parse.quote(version, safe=""))

    def published(self, version):
        release = self.release(version)
        return bool(release and release.get("tag_name") == version and not release.get("draft", True))


def required_assets(version):
    names = {"checksums.txt"}
    for platform in ("linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64", "windows_amd64"):
        windows = platform.startswith("windows")
        names.add("pulso_" + platform + (".exe" if windows else ""))
        names.add(f"pulso_{version}_{platform}" + (".zip" if windows else ".tar.gz"))
    return names


def docker(*args):
    result = subprocess.run(["docker", "buildx", "imagetools", *args], text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError("Docker manifest operation failed: " + result.stderr.strip())
    return result.stdout


def publish_release(version, github, registry=docker):
    parsed = Version.parse(version)
    release = github.release(version)
    if not release:
        raise ValueError("Create and upload the draft release before publishing")
    if not release.get("draft", True):
        return "Already published"
    uploaded = {asset["name"] for asset in release.get("assets", []) if asset.get("state") == "uploaded" and asset.get("size", 0) > 0}
    missing = required_assets(version) - uploaded
    if missing:
        raise ValueError("Release assets are missing or unfinished: " + ", ".join(sorted(missing)))
    latest = github.request("GET", "/releases/latest")
    promote = not parsed.prerelease and (latest is None or parsed.numbers > Version.parse(latest["tag_name"]).numbers)
    image = "ghcr.io/" + github.repository.lower()
    manifests = []
    # Verify both variants and both architectures before moving either latest tag.
    for suffix in ("", "-python311"):
        manifest = json.loads(registry("inspect", image + ":" + version + suffix, "--format", "{{json .Manifest}}"))
        digest = manifest.get("digest", "")
        platforms = {(entry.get("platform", {}).get("os"), entry.get("platform", {}).get("architecture")) for entry in manifest.get("manifests", [])}
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest) or not {("linux", "amd64"), ("linux", "arm64")} <= platforms:
            raise ValueError("Both linux/amd64 and linux/arm64 images must be uploaded: " + suffix)
        manifests.append((suffix, digest))
    if promote:
        for suffix, digest in manifests:
            registry("create", "--tag", image + ":latest" + suffix, image + "@" + digest)
    github.request("PATCH", "/releases/" + str(int(release["id"])),
                   {"draft": False, "prerelease": bool(parsed.prerelease), "make_latest": "true" if promote else "false"})
    return "Published " + version + (" and Docker latest tags" if promote else " without changing latest")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    plan = sub.add_parser("plan")
    plan.add_argument("--ref", required=True)
    plan.add_argument("--sha", required=True)
    plan.add_argument("--bump", choices=("patch", "minor", "major"), default="patch")
    plan.add_argument("--create-tag", action="store_true")
    plan.add_argument("--check-published", action="store_true")
    plan.add_argument("--intent", default="", help="Manual dispatch run ID; reruns reuse its tag")
    plan.add_argument("--output", type=pathlib.Path)
    publish = sub.add_parser("publish")
    publish.add_argument("--version", required=True)
    args = parser.parse_args()
    if args.command == "publish" or args.check_published:
        github = GitHub(os.environ.get("GITHUB_REPOSITORY", ""), os.environ.get("GH_TOKEN", ""))
    if args.command == "publish":
        print(publish_release(args.version, github))
        return
    git = Git()
    # Refresh tags created by a previous queued execution, without rewriting any.
    git.run("fetch", "--tags", "origin")
    result = plan_release(git, args.ref, args.sha, args.bump,
                          github.published if args.check_published else lambda _version: False, args.intent)
    if result["create_tag"] and args.create_tag:
        git.create_tag(result["version"], result["revision"], args.intent)
    if args.output:
        with args.output.open("a") as output:
            for key in ("version", "revision", "publish"):
                value = str(result[key]).lower() if isinstance(result[key], bool) else result[key]
                output.write(f"{key}={value}\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
