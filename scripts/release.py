#!/usr/bin/env python3
"""Reserve exact-source release identities and publish native bragibooks outputs."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


class ReleaseError(Exception):
    pass


VERSION = re.compile(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
OID = re.compile(r"[0-9a-f]{40}\Z")
REPOSITORY = re.compile(r"(?:git@github\.com:|https://github\.com/)([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?/?\Z")


def semver(value):
    match = VERSION.fullmatch(value)
    if not match:
        raise ReleaseError(f"Invalid release version: {value!r}; expected X.Y.Z")
    return tuple(map(int, match.groups()))


def require(condition, message):
    if not condition:
        raise ReleaseError(message)


def isolated_git_environment():
    return {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}


class Repository:
    def __init__(self, root="."):
        self.root = Path(root).resolve()

    @staticmethod
    def proc(args, cwd):
        return subprocess.run(args, cwd=cwd, env=isolated_git_environment(), text=True,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def run(self, *args, ok=(0,)):
        result = self.proc(args, self.root)
        require(result.returncode in ok, f"{' '.join(args)}: {result.stderr.strip() or result.stdout.strip()}")
        return result.stdout.strip()

    def git(self, *args):
        return self.run("git", *args)

    def repository(self):
        match = REPOSITORY.fullmatch(self.git("remote", "get-url", "origin"))
        push = REPOSITORY.fullmatch(self.git("remote", "get-url", "--push", "origin"))
        require(match and push and match.group(1).lower() == push.group(1).lower(),
                "origin fetch/push must identify the same GitHub repository")
        return match.group(1)

    def api(self, endpoint, method="GET", fields=None):
        args = ["gh", "api", "--hostname", "github.com", endpoint]
        if method != "GET":
            args += ["--method", method]
        for key, value in (fields or {}).items():
            args += ["-f", f"{key}={value}"]
        value = self.run(*args)
        return json.loads(value) if value else None

    def oid(self, ref):
        require(isinstance(ref, str) and not ref.startswith("-"), "Invalid git reference")
        value = self.git("rev-parse", "--verify", f"{ref}^{{commit}}")
        require(OID.fullmatch(value), "Expected a full commit object ID")
        return value

    def exact_oid(self, value):
        require(isinstance(value, str) and OID.fullmatch(value), "Expected a full commit object ID")
        require(self.oid(value) == value, "Commit object ID did not resolve exactly")
        return value

    def ancestor(self, older, newer):
        result = self.proc(["git", "merge-base", "--is-ancestor", older, newer], self.root)
        require(result.returncode in (0, 1), result.stderr.strip() or "Cannot inspect ancestry")
        return result.returncode == 0

    def fetch(self):
        self.repository()
        self.git("fetch", "--prune", "--no-prune-tags", "--no-tags", "origin",
                 "+refs/heads/main:refs/remotes/origin/main",
                 "+refs/heads/develop:refs/remotes/origin/develop",
                 "+refs/tags/*:refs/bragibooks-release/tags/*")

    def tags(self, ref):
        tags = []
        for tag in self.git("for-each-ref", f"--merged={ref}", "--format=%(refname:strip=3)",
                            "refs/bragibooks-release/tags/v*").splitlines():
            if VERSION.fullmatch(tag[1:]):
                tags.append((semver(tag[1:]), tag))
        return sorted(tags)

    def tag_target(self, tag):
        ref = f"refs/bragibooks-release/tags/{tag}"
        result = self.proc(["git", "show-ref", "--verify", "--quiet", ref], self.root)
        require(result.returncode in (0, 1), "Cannot inspect release tag")
        return self.oid(ref) if result.returncode == 0 else None

    def api_tag_target(self, tag):
        """Resolve a remote tag to its commit, peeling annotated tag objects."""
        repository = self.repository()
        try:
            target = self.api(f"repos/{repository}/git/ref/tags/{tag}")["object"]
        except ReleaseError as error:
            require("HTTP 404" in str(error), str(error))
            return None
        for _ in range(10):
            if target["type"] != "tag":
                break
            target = self.api(f"repos/{repository}/git/tags/{target['sha']}")["object"]
        require(target["type"] == "commit", f"Tag {tag} does not resolve to a commit")
        return target["sha"]

    def release_records(self, repository):
        """Complete authenticated release listing; drafts included, errors propagate.

        GET releases/tags/{tag} is a published-release lookup only, so every
        draft lifecycle caller must use the paginated listing instead."""
        records, page = [], 1
        while True:
            batch = self.api(f"repos/{repository}/releases?per_page=100&page={page}")
            require(isinstance(batch, list), f"Unexpected release listing response on page {page}")
            records.extend(batch)
            if len(batch) < 100:
                return records
            page += 1

    def release_record(self, tag):
        """Draft-aware exact-tag release lookup; duplicate records fail closed."""
        records = [record for record in self.release_records(self.repository())
                   if record.get("tag_name") == tag]
        require(len(records) <= 1, f"Multiple release records share tag {tag}; failing closed")
        return records[0] if records else None

    def release_assets(self, tag):
        """Asset of the draft-aware release record; a missing record is true absence."""
        target = self.release_record(tag)
        if target is None:
            raise ReleaseError(f"Release {tag} does not exist")
        return target["assets"]

    def select(self, sha, version=None, bump="auto", automatic=False):
        sha = self.exact_oid(sha)
        main = self.oid("origin/main")
        require(self.ancestor(sha, main), "Publication source must belong to main history")
        tags = self.tags(sha)
        exact = [tag for _, tag in tags if self.tag_target(tag) == sha]
        require(len(exact) <= 1, "Ambiguous exact-source release reservations")
        if version:
            version = "v" + version.removeprefix("v")
            semver(version[1:])
            require(not exact or exact == [version], "Source already reserved as a different version")
            target = self.tag_target(version)
            require(target is None or target == sha, "Version is reserved by another source")
            if target:
                return version
        elif exact:
            return exact[0]
        if automatic:
            for _, tag in self.tags(main):
                target = self.tag_target(tag)
                if target != sha and self.ancestor(sha, target):
                    return None  # Superseded automatic candidate; no reservation yet.
        baseline, previous = tags[-1] if tags else ((0, 0, 0), None)
        if version:
            require(semver(version[1:]) > baseline, "New version must exceed the reachable reservation baseline")
            return version
        if not previous:
            return "v0.1.0"
        if bump == "auto":
            messages = self.git("log", "--format=%B", f"refs/bragibooks-release/tags/{previous}..{sha}")
            if re.search(r"(?m)^BREAKING[ -]CHANGE:|^[a-zA-Z][\w-]*(?:\([^\n)]*\))?!:", messages):
                bump = "major"
            elif re.search(r"(?m)^feat(?:\([^\n)]*\))?:", messages):
                bump = "minor"
            else:
                bump = "patch"
        major, minor, patch = baseline
        require(bump in ("major", "minor", "patch"), "Invalid bump level")
        parts = {"major": (major + 1, 0, 0), "minor": (major, minor + 1, 0), "patch": (major, minor, patch + 1)}[bump]
        selected = "v" + ".".join(map(str, parts))
        require(self.tag_target(selected) in (None, sha), "Version is reserved by another source")
        return selected

    def reserve(self, sha, version=None, bump="auto", automatic=False):
        self.fetch()
        tag = self.select(sha, version, bump, automatic)
        if tag is None:
            return None
        repository = self.repository()
        try:
            self.api(f"repos/{repository}/git/refs", "POST", {"ref": f"refs/tags/{tag}", "sha": sha})
        except ReleaseError:
            # Create-only REST ref writes cannot move a concurrent reservation.
            target = self.api(f"repos/{repository}/git/ref/tags/{tag}")["object"]
            while target["type"] == "tag":
                target = self.api(f"repos/{repository}/git/tags/{target['sha']}")["object"]
            require(target["type"] == "commit" and target["sha"] == sha,
                    "Concurrent reservation points at a different source")
        return tag

    def release(self, tag, source, publish=False):
        semver(tag.removeprefix("v"))
        require(isinstance(source, str) and OID.fullmatch(source), "Expected a full commit object ID")
        require(self.api_tag_target(tag) == source, f"Tag {tag} does not point at the frozen source")
        target = self.release_record(tag)
        if target is None:
            require(not publish, "Cannot finalize a missing draft release")
            self.run("gh", "release", "create", tag, "--repo", self.repository(),
                     "--verify-tag", "--draft", "--title", f"Release {tag}", "--notes", "")
            return
        if publish and target.get("draft"):
            current = semver(tag.removeprefix("v"))
            higher_published = any(
                not record.get("draft") and not record.get("prerelease")
                and VERSION.fullmatch(str(record.get("tag_name", "")).removeprefix("v"))
                and semver(str(record.get("tag_name", "")).removeprefix("v")) > current
                for record in self.release_records(self.repository()))
            # GitHub Latest is independent of Docker aliases: a new highest
            # stable release advances it; a delayed lower release must not
            # roll it back. Tags outside the numeric X.Y.Z grammar never claim Latest.
            self.run("gh", "release", "edit", tag, "--repo", self.repository(),
                     "--draft=false", f"--latest={'true' if not higher_published else 'false'}")


DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")


class RegistryCommandError(ReleaseError):
    """Failing registry command; captured stdout and stderr reach the CLI error output."""

    def __init__(self, error):
        self.streams = " ".join(part for part in (
            (error.stderr or b"").decode(errors="replace").strip(),
            (error.stdout or b"").decode(errors="replace").strip()) if part)
        super().__init__(f"registry command failed (exit {error.returncode}): {self.streams}")


def registry_run(*args):
    try:
        return subprocess.run(args, check=True, capture_output=True).stdout
    except subprocess.CalledProcessError as error:
        raise RegistryCommandError(error) from None


class Registry:
    """Deterministic native-registry boundary; every command routes through registry_run so
    behavior tests substitute one fake that records and verifies the consumer-visible store."""

    def __init__(self, run=registry_run):
        self.run = run

    def inspect(self, ref, field="Manifest", missing=False):
        try:
            return json.loads(self.run("docker", "buildx", "imagetools", "inspect", ref,
                                       "--format", "{{json ." + field + "}}"))
        except RegistryCommandError as error:
            message = error.streams.lower()
            # Only an explicit registry manifest-not-found response permits creation.
            # Authentication, transport, throttling and malformed responses fail closed.
            absent = "manifest unknown" in message or f"{ref.lower()}: not found" in message
            if missing and absent and not any(word in message for word in
                                              ("unauthorized", "denied", "timeout", "forbidden")):
                return None
            raise

    def config(self, image_name, ref, field="Image"):
        return json.loads(self.run("docker", "buildx", "imagetools", "inspect", ref,
                                   "--format", "{{json ." + field + "}}"))

    def build(self, staging, source, version):
        self.run("docker", "buildx", "build", "--platform", "linux/amd64",
                 "--provenance=false", "--sbom=false", "--file", "docker/Dockerfile",
                 "--build-arg", f"VERSION={version}", "--build-arg", f"GIT_COMMIT={source}",
                 "--tag", staging, "--push", ".")

    def copy(self, origin, expected, ref):
        # Native registry copy preserves manifests AND layers across registries.
        self.run("skopeo", "copy", "--preserve-digests",
                 f"docker://{origin}@{expected}", f"docker://{ref}")


def digest(value):
    if not isinstance(value, str) or not DIGEST.fullmatch(value):
        raise ValueError(f"Invalid registry digest: {value!r}")
    return value


def image(registry, image_name, ref, sha, version, arch, missing=False):
    manifest = registry.inspect(ref, missing=missing)
    if manifest is None:
        return None
    if "manifests" in manifest:
        raise ValueError(f"Expected one native image at {ref}")
    value = digest(manifest["digest"])
    config = registry.config(image_name, f"{image_name}@{value}")
    labels = config.get("config", {}).get("Labels", {})
    if (config.get("os"), config.get("architecture"),
        labels.get("org.opencontainers.image.revision"),
        labels.get("org.opencontainers.image.version")) != ("linux", arch, sha, version):
        raise ValueError(f"Image provenance mismatch at {ref}")
    return value


def publish_image(repository, source, version, registry=None):
    require(OID.fullmatch(source), "Image source must be a full SHA")
    semver(version.removeprefix("v"))
    registry = registry or Registry()
    names = ("ghcr.io/" + repository.lower(), "docker.io/djdembeck/bragibooks")
    targets = [(name, f"{name}:{tag}") for name in names
               for tag in (version, version.removeprefix("v"))]
    existing = [(name, ref, image(registry, name, ref, source, version, "amd64", missing=True))
                for name, ref in targets]
    staging = f"{names[0]}:sha-{source}-amd64"
    staged = image(registry, names[0], staging, source, version, "amd64", missing=True)
    present = [(name, value) for name, _, value in existing if value is not None]
    if staged is not None:
        present.append((names[0], staged))
    require(len({value for _, value in present}) <= 1,
            "Existing registry images have conflicting bytes")
    if not present:
        registry.build(staging, source, version)
        present.append((names[0], image(registry, names[0], staging, source, version, "amd64")))
    origin, expected = present[0]
    for name, ref, value in existing:
        if value is None:
            registry.copy(origin, expected, ref)
        require(image(registry, name, ref, source, version, "amd64") == expected,
                f"Version image read-back differs at {ref}")
    for name in names:
        print(f"Published immutable image: {name}@{expected}")


def asset_label(sha, tag, value):
    return f"bragibooks-publish-v1;sha={sha};tag={tag};sha256={value.removeprefix('sha256:')}"


def asset_digest(asset, repository, run=registry_run):
    actual = asset.get("digest")
    if actual is None:
        data = run("gh", "api", "--hostname", "github.com", "-H",
                   "Accept: application/octet-stream",
                   f"repos/{repository}/releases/assets/{asset['id']}")
        actual = "sha256:" + hashlib.sha256(data).hexdigest()
    return digest(actual)


def checksum_lines(names):
    return "".join(f"{value.removeprefix('sha256:')}  {name}\n"
                   for name, value in sorted(names.items())).encode()


def checksum_entries(data):
    entries = {}
    for line in data.decode().splitlines():
        hexdigest, name = line.split("  ", 1)
        require(len(hexdigest) == 64 and re.fullmatch(r"[0-9a-f]{64}", hexdigest) and name,
                f"Malformed checksum line: {line!r}")
        entries[name] = "sha256:" + hexdigest
    require(len(entries) == 4, "Expected exactly four checksum entries")
    return entries


def publish_assets(repository, source, version, fetcher=None):
    require(OID.fullmatch(source), "Asset source must be a full SHA")
    semver(version.removeprefix("v"))
    checksum_name = f"bragibooks_{version.removeprefix('v')}_checksums.txt"
    store = {}
    for asset in fetcher():
        require(asset["name"] not in store, "Duplicate release asset")
        store[asset["name"]] = asset
    metadata = json.loads(Path("dist/artifacts.json").read_text())
    archives = [item for item in metadata if item["type"] == "Archive"]
    checksums = [item for item in metadata if item["type"] == "Checksum"]
    require(len(archives) == 4 and len(checksums) == 1,
            "Expected the four native archives and their checksum file")
    require({(item.get("goos"), item.get("goarch")) for item in archives}
            == {(system, arch) for system in ("linux", "darwin") for arch in ("amd64", "arm64")},
            "Native archive platform coverage differs")
    require(checksums[0]["name"] == checksum_name,
            "Checksum file has the wrong release identity")
    outputs = archives + checksums
    require(len({item["name"] for item in outputs}) == 5, "Duplicate built asset names")
    # Plan the ENTIRE set before entering the upload loop: every preserved
    # store asset plus every pending build must form one verifiable collection,
    # because a checksum manifest that cannot describe the store is never uploaded.
    planned = {}
    pending = []
    for item in outputs:
        name = item["name"]
        path = Path(item["path"])
        require(path.parent == Path("dist") and path.name == name
                and path.is_file() and not path.is_symlink(), f"Invalid built release asset: {name}")
        built = path.read_bytes()
        expected = "sha256:" + hashlib.sha256(built).hexdigest()
        if item["type"] == "Archive":
            require(expected == item["extra"]["Checksum"], f"Built archive checksum differs: {name}")
        asset = store.get(name)
        if asset is None:
            # The checksum manifest is planned below, where it is either an
            # immutable store asset or a generated pending upload.
            if item["type"] == "Archive":
                planned[name] = expected
                pending.append((path, asset_label(source, version, expected)))
            continue
        actual = asset_digest(asset, repository, registry_run)
        label = asset.get("label") or ""
        if label:
            require(label == asset_label(source, version, actual),
                    f"Existing asset provenance differs: {name}")
        elif item["type"] == "Archive":
            # Only archive bytes are build-verifiable; the checksum manifest is
            # derived content, verified against the planned set below.
            require(actual == expected, f"Existing asset differs from tested build: {name}")
        if item["type"] == "Archive":
            planned[name] = actual
    manifest = checksum_lines(planned)
    existing_checksum = store.get(checksum_name)
    if existing_checksum is not None:
        # The checksum manifest is immutable: it must already describe the
        # preserved and pending archive bytes before any upload.
        if existing_checksum.get("digest") is not None:
            require("sha256:" + hashlib.sha256(manifest).hexdigest() == existing_checksum["digest"],
                    "Existing checksum manifest does not describe the planned archive set")
        else:
            data = registry_run("gh", "api", "--hostname", "github.com", "-H",
                                "Accept: application/octet-stream",
                                f"repos/{repository}/releases/assets/{existing_checksum['id']}")
            require(checksum_entries(data) == planned,
                    "Existing checksum manifest does not describe the planned archive set")
    else:
        # No immutable manifest exists: generate one from the combined verified
        # archive digests so a valid partial retry can complete even when the
        # rebuild bytes nondeterministically differ.
        checksum_path = Path(checksums[0]["path"])
        checksum_path.write_bytes(manifest)
        pending.append((checksum_path,
                        asset_label(source, version,
                                    "sha256:" + hashlib.sha256(manifest).hexdigest())))
    for path, label in pending:
        registry_run("gh", "release", "upload", version, f"{path}#{label}", "--repo", repository)

def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    reserve = commands.add_parser("reserve")
    reserve.add_argument("--sha", required=True)
    reserve.add_argument("--version")
    reserve.add_argument("--bump", choices=("auto", "patch", "minor", "major"), default="auto")
    reserve.add_argument("--automatic", action="store_true")
    for name in ("image", "assets"):
        action = commands.add_parser(name)
        action.add_argument("--source", required=True)
        action.add_argument("--version", required=True)
    finish = commands.add_parser("release")
    finish.add_argument("--tag", required=True)
    finish.add_argument("--source", required=True)
    finish.add_argument("--publish", action="store_true")
    args = parser.parse_args(argv)
    repo = Repository()
    try:
        repository = repo.repository()
        if args.command == "reserve":
            print(repo.reserve(args.sha, args.version, args.bump, args.automatic) or "")
        elif args.command == "release":
            repo.release(args.tag, args.source, args.publish)
        elif args.command == "image":
            publish_image(repository, args.source, args.version)
        elif args.command == "assets":
            publish_assets(repository, args.source, args.version,
                           lambda: repo.release_assets(args.version))
        return 0
    except (ReleaseError, OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"release: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
