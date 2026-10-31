"""Real temporary git-history release lifecycle regressions; no production writes."""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import release


SOURCE = "f" * 40
REPOSITORY = "djdembeck/bragibooks"
VERSION = "v9.9.9"
CHECKSUM_NAME = f"bragibooks_{VERSION.removeprefix('v')}_checksums.txt"
ARCHIVE_NAMES = (
    "bragibooks_Linux_x86_64.tar.gz",
    "bragibooks_Linux_arm64 .tar.gz",
    "bragibooks_Darwin_x86_64.tar.gz",
    "bragibooks_Darwin_arm64 .tar.gz",
)
ARCHIVE_PLATFORM = {
    "bragibooks_Linux_x86_64.tar.gz": ("linux", "amd64"),
    "bragibooks_Linux_arm64 .tar.gz": ("linux", "arm64"),
    "bragibooks_Darwin_x86_64.tar.gz": ("darwin", "amd64"),
    "bragibooks_Darwin_arm64 .tar.gz": ("darwin", "arm64"),
}


def sha256(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def line(name, value):
    return f"{value.removeprefix('sha256:')}  {name}\n"


def manifest(names):
    return "".join(line(name, sha256(value))
                   for name, value in sorted(names.items())).encode()


def run_git_ok(cwd, *args):
    result = subprocess.run(args, cwd=cwd, env=release.isolated_git_environment(), text=True,
                            capture_output=True)
    if result.returncode != 0:
        raise AssertionError(result.stderr)
    return result.stdout.strip()


class LocalGitHub(release.Repository):

    def __init__(self, root, remote):
        super().__init__(root)
        self.remote = remote
        self.releases = []
        self.release_commands = []
        self.latest = None
        self.release_list_pages = []

    def repository(self):
        return "djdembeck/m4b-merge"

    def remote_git(self, *args):
        return self.run("git", "--git-dir", str(self.remote), *args)

    def api(self, endpoint, method="GET", fields=None):
        path = endpoint.removeprefix("repos/djdembeck/m4b-merge/")
        if path == "git/refs" and method == "POST":
            self.remote_git("update-ref", fields["ref"], fields["sha"], "0" * 40)
            return {"ref": fields["ref"]}
        if path.startswith("git/ref/"):
            ref = path.removeprefix("git/ref/")
            return {"object": {"type": "commit", "sha": self.remote_git("rev-parse", f"refs/{ref}^{{commit}}")}}
        if path.startswith("releases/tags/"):
            # GitHub documents releases/tags/{tag} as a published-release lookup.
            tag = path.removeprefix("releases/tags/")
            for record in self.releases:
                if record["tag_name"] == tag and not record["draft"]:
                    return dict(record)
            raise release.ReleaseError("HTTP 404: Not Found")
        if path == "releases/latest":
            record = next((record for record in self.releases
                           if record["tag_name"] == self.latest), None) \
                if self.latest is not None else None
            if record is None:
                raise release.ReleaseError("HTTP 404: Not Found")
            return dict(record)
        if path.startswith("releases?"):
            page = int(endpoint.rsplit("page=", 1)[1])
            self.release_list_pages.append(page)
            start = (page - 1) * 100
            return [dict(record) for record in self.releases[start:start + 100]]
        raise AssertionError((endpoint, method, fields))

    def run(self, *args, ok=(0,)):
        if len(args) >= 4 and args[0] == "gh" and args[1] == "release" and args[2] in ("create", "edit"):
            tag, existing = args[3], [record for record in self.releases
                                      if record["tag_name"] == args[3]]
            if args[2] == "create":
                if existing:
                    raise release.ReleaseError(f"gh release create: release {tag} already exists")
                self.release_commands.append(("create", args))
                self.releases.append({"tag_name": tag, "draft": "--draft" in args, "prerelease": False})
                return ""
            self.release_commands.append((args[2], args))
            record = existing[0]
            if "--draft=false" in args:
                # Newly-published releases default to GitHub Latest unless
                # the edit explicitly opts out.
                if record["draft"] and "--latest=false" not in args:
                    self.latest = record["tag_name"]
                record["draft"] = False
            elif "--latest=true" in args:
                self.latest = record["tag_name"]
            return ""
        return super().run(*args, ok=ok)


def bootstrap(temp):
    """Temp bare remote + main/develop checkout with v1.0.0 and a promoted feat commit."""
    root = Path(temp)
    remote, checkout = root / "remote.git", root / "checkout"
    run_git_ok(root, "git", "init", "--bare", str(remote))
    run_git_ok(root, "git", "init", "-b", "main", str(checkout))
    repository = LocalGitHub(checkout, remote)
    for key, value in (("user.name", "Release Test"), ("user.email", "test@example.invalid"),
                       ("commit.gpgsign", "false"), ("tag.gpgsign", "false")):
        run_git_ok(checkout, "git", "config", key, value)
    run_git_ok(checkout, "git", "remote", "add", "origin", str(remote))
    _commit(checkout, "initial", "one")
    run_git_ok(checkout, "git", "tag", "v1.0.0")
    run_git_ok(checkout, "git", "push", "origin", "main", "--tags")
    run_git_ok(checkout, "git", "switch", "-c", "develop")
    _commit(checkout, "feat: new behavior", "two")
    run_git_ok(checkout, "git", "push", "origin", "develop")
    run_git_ok(checkout, "git", "switch", "main")
    run_git_ok(checkout, "git", "merge", "--no-ff", "develop", "-m", "Promote develop")
    run_git_ok(checkout, "git", "push", "origin", "main")
    return remote, checkout, repository


def _commit(checkout, message, content):
    (checkout / "source.txt").write_text(content)
    run_git_ok(checkout, "git", "add", "source.txt")
    run_git_ok(checkout, "git", "commit", "-m", message)


class LifecycleFixture(unittest.TestCase):
    """Shared git-history fixture; subclasses add the release records under test."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="m4b-reservation-")
        self.addCleanup(self.temp.cleanup)
        self.remote, self.checkout, self.repo = bootstrap(self.temp.name)
        self.source = run_git_ok(self.checkout, "git", "rev-parse", "HEAD")
        self.repo.fetch()

    def git(self, *args):
        return run_git_ok(self.checkout, "git", *args)

    def commit(self, message, content):
        _commit(self.checkout, message, content)

    def next_develop(self):
        self.git("switch", "develop")
        self.commit("fix: next behavior", "three")
        self.git("push", "origin", "develop")
        self.repo.fetch()
        return self.git("rev-parse", "HEAD")


class ReleaseBehavior(LifecycleFixture):

    def test_reservation_reuses_exact_source_after_newer_release(self):
        tag = self.repo.reserve(self.source)
        self.assertEqual(tag, "v1.1.0")
        self.next_develop()
        self.git("switch", "main")
        self.git("merge", "--no-ff", "develop", "-m", "Next promotion")
        self.git("push", "origin", "main")
        newer = self.git("rev-parse", "HEAD")
        self.assertEqual(self.repo.reserve(newer), "v1.1.1")
        self.assertEqual(self.repo.reserve(self.source), tag)
        self.assertEqual(self.repo.remote_git("rev-parse", "refs/tags/v1.1.0"), self.source)

    def test_unfinished_reservation_is_not_reallocated(self):
        self.assertEqual(self.repo.reserve(self.source), "v1.1.0")
        self.assertEqual(self.repo.reserve(self.source, bump="major"), "v1.1.0")
        self.assertEqual(self.repo.select(self.source, "v1.1.0"), "v1.1.0")
        with self.assertRaises(release.ReleaseError):
            self.repo.reserve(self.source, "v2.0.0")

    def test_other_branch_tag_cannot_set_reachable_baseline(self):
        self.git("switch", "-c", "unrelated", "v1.0.0")
        self.commit("unrelated", "elsewhere")
        self.git("tag", "v99.0.0")
        self.git("push", "origin", "v99.0.0")
        self.repo.fetch()
        self.assertEqual(self.repo.select(self.source), "v1.1.0")

    def test_explicit_different_source_collision_never_moves_tag(self):
        with self.assertRaises(release.ReleaseError):
            self.repo.reserve(self.source, "v1.0.0")
        self.assertEqual(self.repo.remote_git("rev-parse", "refs/tags/v1.0.0"), self.git("rev-parse", "v1.0.0"))

    def test_ambiguous_exact_source_reservations_fail(self):
        for tag in ("v1.1.0", "v1.2.0"):
            self.git("tag", tag, self.source)
            self.git("push", "origin", tag)
        self.repo.fetch()
        with self.assertRaises(release.ReleaseError):
            self.repo.select(self.source)

    def test_release_refuses_tag_pointing_at_another_source(self):
        initial = self.git("rev-parse", "v1.0.0")
        self.assertNotEqual(initial, self.source)
        # Release metadata routes raise AssertionError in this fixture, so a
        # ReleaseError proves the frozen-source check failed closed first.
        with self.assertRaisesRegex(release.ReleaseError, "does not point at the frozen source"):
            self.repo.release("v1.0.0", self.source, publish=True)
        self.assertEqual(self.repo.remote_git("rev-parse", "refs/tags/v1.0.0"), initial)

    def test_unreserved_automatic_candidate_is_coalesced_but_manual_is_not(self):
        old = self.source
        self.next_develop()
        self.git("switch", "main")
        self.git("merge", "--no-ff", "develop", "-m", "Next promotion")
        self.git("push", "origin", "main")
        self.repo.reserve(self.git("rev-parse", "HEAD"), "v1.2.0")
        self.repo.fetch()
        self.assertIsNone(self.repo.select(old, automatic=True))
        self.assertEqual(self.repo.select(old), "v1.1.0")


class ReleaseLifecycleBehavior(LifecycleFixture):
    """Draft-aware release lookup and ordered GitHub Latest behavior."""

    def test_release_publish_cycle_advances_latest_for_highest_release(self):
        # v1.10.0 beats the published v1.9.0 numerically, not lexically.
        self.repo.releases.append({"tag_name": "v1.9.0", "draft": False, "prerelease": False})
        self.repo.latest = "v1.9.0"
        self.assertEqual(self.repo.reserve(self.source, version="v1.10.0"), "v1.10.0")
        self.repo.fetch()
        self.repo.release("v1.10.0", self.source)
        self.assertEqual([name for name, _ in self.repo.release_commands], ["create"])
        draft = self.repo.releases[-1]
        self.assertTrue(draft["draft"])
        self.repo.release("v1.10.0", self.source)
        self.assertEqual([name for name, _ in self.repo.release_commands], ["create"])
        self.assertIs(self.repo.releases[-1], draft)
        self.repo.release("v1.10.0", self.source, publish=True)
        self.assertEqual([name for name, _ in self.repo.release_commands], ["create", "edit"])
        self.assertFalse(draft["draft"])
        self.assertEqual(self.repo.api("repos/djdembeck/m4b-merge/releases/latest")["tag_name"],
                         "v1.10.0")
        self.repo.release("v1.10.0", self.source, publish=True)
        self.assertEqual([name for name, _ in self.repo.release_commands], ["create", "edit"])
        self.assertEqual(len(self.repo.releases), 2)
        self.assertEqual(self.repo.remote_git("rev-parse", "refs/tags/v1.10.0"), self.source)

    def test_delayed_lower_publish_does_not_rollback_latest(self):
        self.repo.releases.append({"tag_name": "v1.3.0", "draft": False, "prerelease": False})
        self.repo.releases.append({"tag_name": "v1.3.1-rc.1", "draft": False, "prerelease": True})
        self.repo.releases.append({"tag_name": "v9.9.9", "draft": True, "prerelease": False})
        self.repo.latest = "v1.3.0"
        initial = self.git("rev-parse", "v1.0.0")
        self.assertEqual(self.repo.api("repos/djdembeck/m4b-merge/releases/latest")["tag_name"],
                         "v1.3.0")
        self.repo.release("v1.0.0", initial)
        self.repo.release("v1.0.0", initial, publish=True)
        self.assertIn("--latest=false", self.repo.release_commands[-1][1])
        self.assertEqual(self.repo.api("repos/djdembeck/m4b-merge/releases/latest")["tag_name"],
                         "v1.3.0")

    def test_release_listing_paginates_beyond_one_page(self):
        self.repo.releases.extend({"tag_name": f"v2.{index // 100}.{index % 100}",
                                   "draft": False, "prerelease": False}
                                  for index in range(205))
        self.repo.latest = "v2.2.4"
        initial = self.git("rev-parse", "v1.0.0")
        self.assertEqual(self.repo.api("repos/djdembeck/m4b-merge/releases/latest")["tag_name"],
                         "v2.2.4")
        self.repo.release("v1.0.0", initial)
        self.assertEqual([name for name, _ in self.repo.release_commands], ["create"])
        draft = self.repo.releases[-1]
        self.assertTrue(draft["draft"])
        self.repo.release("v1.0.0", initial)
        self.assertEqual([name for name, _ in self.repo.release_commands], ["create"])
        self.assertIs(self.repo.releases[-1], draft)
        self.repo.release("v1.0.0", initial, publish=True)
        self.assertEqual([name for name, _ in self.repo.release_commands], ["create", "edit"])
        self.assertIn("--latest=false", self.repo.release_commands[-1][1])
        self.assertEqual(self.repo.release_list_pages[:3], [1, 2, 3],
                         "the listing must paginate beyond one page")
        self.assertEqual(set(self.repo.release_list_pages), {1, 2, 3})
        self.assertEqual(self.repo.api("repos/djdembeck/m4b-merge/releases/latest")["tag_name"],
                         "v2.2.4")

    def test_duplicate_release_records_fail_closed_without_writes(self):
        records = [{"tag_name": "v1.0.0", "draft": False, "prerelease": False},
                   {"tag_name": "v1.0.0", "draft": True, "prerelease": False}]
        self.repo.releases.extend(records)
        initial = self.git("rev-parse", "v1.0.0")
        with self.assertRaisesRegex(release.ReleaseError,
                                    r"Multiple release records share tag v1\.0\.0"):
            self.repo.release("v1.0.0", initial, publish=True)
        self.assertEqual(self.repo.release_commands, [])
        self.assertEqual(self.repo.releases, records)

    def test_listing_failure_propagates_without_release_writes(self):
        def fail(repository):
            raise release.ReleaseError("HTTP 403: Resource forbidden by rate limit")

        self.repo.release_records = fail
        initial = self.git("rev-parse", "v1.0.0")
        with self.assertRaisesRegex(release.ReleaseError, "HTTP 403"):
            self.repo.release("v1.0.0", initial, publish=True)
        self.assertEqual(self.repo.release_commands, [])


class FakeRegistry(release.Registry):
    """Deterministic native-registry boundary: consumer-visible ref store plus build/copy records."""

    def __init__(self, existing=None, fail=None, config_overrides=None):
        self.refs = {name: dict(locations) for name, locations in
                     (existing or {}).items()}
        self.provenance = {}
        self.builds = []
        self.copies = []
        self.fail = fail  # (message) raised by the next failing command only
        self.config_overrides = config_overrides or {}
        self.next_digest = "b" * 64
        super().__init__(self._run)

    @staticmethod
    def split(ref):
        registry, _, tail = ref.rpartition(":")
        return registry, tail

    def set(self, registry, ref, digest):
        _, tag = FakeRegistry.split(ref)
        self.refs.setdefault(registry, {})[tag] = digest.removeprefix("sha256:")

    def store_view(self):
        return {ref for registry, locations in self.refs.items() for tag in locations
                for ref in (f"{registry}:{tag}",)}

    def build_digest(self, staging, version, source):
        digest = "sha256:" + ("b" * 64 if self.provenance else self.next_digest)
        self.builds.append((staging, source, version))
        self.provenance[digest] = (source, version)
        self.set(staging.partition(":")[0], staging, digest.removeprefix("sha256:"))
        self.next_digest = "c" * 64
        return digest

    def _run(self, *args):
        if args[0] == "docker" and args[1:3] == ("buildx", "imagetools"):
            ref = args[4]
            if self.fail is not None:
                error = subprocess.CalledProcessError(1, args, output=b"", stderr=self.fail.encode())
                self.fail = None
                raise release.RegistryCommandError(error)
            if "@" in ref:
                return self.config_for(ref.partition("@")[2].removeprefix("sha256:"))
            registry, tag = FakeRegistry.split(ref)
            digest = self.refs.get(registry, {}).get(tag)
            if digest is None:
                raise release.RegistryCommandError(subprocess.CalledProcessError(
                    1, args, output=b"", stderr=f"error: {ref}: not found".encode()))
            return json.dumps({"digest": "sha256:" + digest}).encode()
        if args[0] == "docker" and args[1:3] == ("buildx", "build"):
            tail = list(args)
            staging = tail[tail.index("--tag") + 1]
            build_args = {}
            index = 0
            while index < len(tail) - 1:
                if tail[index] == "--build-arg":
                    key, _, value = tail[index + 1].partition("=")
                    build_args[key] = value
                index += 1
            self.build_digest(staging, build_args["VERSION"], build_args["GIT_COMMIT"])
            return b"build complete"
        if args[0] == "skopeo" and args[1] == "copy":
            origin, target = args[3], args[4]
            origin_hostname, _, source_digest = origin.removeprefix("docker://").rpartition("@")
            target_registry, _, target_ref = target.removeprefix("docker://").rpartition(":")
            self.copies.append((f"{origin_hostname}@{source_digest}", target))
            self.set(target_registry, target_ref, source_digest.removeprefix("sha256:"))
            return b"copied"
        raise AssertionError(args)

    def config_for(self, hexdigest):
        digest = "sha256:" + hexdigest
        override = self.config_overrides.get(hexdigest)
        if override is not None:
            return json.dumps(override).encode()
        source, version = self.provenance.get(digest, ("unknown", "unknown"))
        return json.dumps({"os": "linux", "architecture": "amd64",
                           "config": {"Labels": {"org.opencontainers.image.revision": source,
                                                 "org.opencontainers.image.version": version}}}).encode()


def empty_registry():
    return FakeRegistry()


class RegistryBehavior(unittest.TestCase):
    """publish_image consumer-visible output against a deterministic fake registry."""

    def publish(self, registry, version="v1.2.3"):
        release.publish_image(REPOSITORY, SOURCE, version, registry)

    def test_publish_image_fresh_output_visibly_lands_in_both_registries(self):
        registry = FakeRegistry()
        self.publish(registry)
        ghcr, hub = "ghcr.io/djdembeck/bragibooks", "docker.io/djdembeck/bragibooks"
        digest = registry.builds and registry.provenance and next(iter(registry.provenance))
        for registry_name in (ghcr, hub):
            self.assertEqual(registry.refs[registry_name]["v1.2.3"], digest.removeprefix("sha256:"))
            self.assertEqual(registry.refs[registry_name]["1.2.3"], digest.removeprefix("sha256:"))
        self.assertEqual(registry.refs[ghcr]["sha-" + SOURCE + "-amd64"], digest.removeprefix("sha256:"))
        self.assertEqual(len(registry.builds), 1)
        self.assertEqual(len(registry.copies), 4)

    def test_publish_image_reuses_staged_bytes_without_rebuild(self):
        digest = "sha256:" + "d" * 64
        registry = FakeRegistry(existing={"ghcr.io/djdembeck/bragibooks":
                                          {"sha-" + SOURCE + "-amd64": "d" * 64}})
        registry.provenance[digest] = (SOURCE, "v1.2.3")
        self.publish(registry)
        self.assertEqual(registry.builds, [])
        self.assertEqual(registry.refs["docker.io/djdembeck/bragibooks"]["v1.2.3"], "d" * 64)
        self.assertEqual(registry.refs["ghcr.io/djdembeck/bragibooks"]["1.2.3"], "d" * 64)

    def test_existing_version_tags_with_conflicting_bytes_fail_before_copy(self):
        registry = FakeRegistry(existing={"ghcr.io/djdembeck/bragibooks": {"v1.2.3": "1" * 64},
                                          "docker.io/djdembeck/bragibooks": {"1.2.3": "2" * 64}})
        registry.provenance.update({"sha256:" + "1" * 64: (SOURCE, "v1.2.3"),
                                    "sha256:" + "2" * 64: (SOURCE, "v1.2.3")})
        before = {name: dict(locations) for name, locations in registry.refs.items()}
        with self.assertRaisesRegex(release.ReleaseError, "conflicting bytes"):
            self.publish(registry)
        self.assertEqual(registry.builds, [])
        self.assertEqual(registry.copies, [])
        self.assertEqual({name: dict(locations) for name, locations in registry.refs.items()}, before)

    def test_version_tag_readback_mismatch_fails_closed(self):
        registry = FakeRegistry(existing={"ghcr.io/djdembeck/bragibooks": {"v1.2.3": "e" * 64}})
        registry.provenance["sha256:" + "e" * 64] = (SOURCE, "v1.2.3")
        registry.config_overrides["e" * 64] = {"os": "linux", "architecture": "amd64",
                                               "config": {"Labels": {
                                                   "org.opencontainers.image.revision": "e" * 40,
                                                   "org.opencontainers.image.version": "v1.2.3"}}}
        with self.assertRaisesRegex(ValueError, "provenance mismatch"):
            self.publish(registry)
        self.assertEqual(registry.builds, [])
        self.assertEqual(registry.copies, [])

    def test_auth_failure_inspection_fails_closed_without_writes(self):
        registry = FakeRegistry(fail="unauthorized: authentication required")
        with self.assertRaises(release.RegistryCommandError):
            self.publish(registry)
        self.assertEqual(registry.refs, {})
        self.assertEqual(registry.builds, [])
        self.assertEqual(registry.copies, [])

    def test_transport_failure_is_not_treated_as_absence(self):
        registry = FakeRegistry(fail="registry server timed out")
        with self.assertRaises(release.RegistryCommandError):
            self.publish(registry)
        self.assertEqual(registry.refs, {})
        self.assertEqual(registry.builds, [])
        self.assertEqual(registry.copies, [])


class FakeStore:
    def __init__(self, assets=None, bodies=None, list_error=None, upload_error=None):
        self.assets = [dict(asset) for asset in (assets or [])]
        self.bodies = dict(bodies or {})
        self.list_error = list_error
        self.upload_error = upload_error
        self.uploads = []

    def fetch(self):
        if self.list_error is not None:
            raise self.list_error
        return [dict(asset) for asset in self.assets]

    def names(self):
        return [asset["name"] for asset in self.assets]


def store_upload(store):
    def run_release(*args):
        if args[0] == "gh" and args[1] == "release" and args[2] == "upload":
            tag, spec = args[3], args[4]
            require(tag == VERSION, f"unexpected release upload target {tag}")
            path, label = spec.split("#", 1)
            name = Path(path).name
            require(name not in store.names(), "replaced an existing asset")
            if store.upload_error is not None and len(store.uploads) >= store.upload_error:
                raise release.RegistryCommandError(subprocess.CalledProcessError(
                    1, args, output=b"", stderr=b"502 registry server error"))
            store.uploads.append((name, label, Path(path).read_bytes()))
            return b""
        if args[0] == "gh" and args[1] == "api":
            endpoint = next((arg for arg in args if arg.startswith("repos/")), None)
            if endpoint is not None and "/releases/assets/" in endpoint:
                asset_id = endpoint.rsplit("/", 1)[1]
                for asset in store.assets:
                    if str(asset["id"]) == asset_id:
                        return store.bodies[asset["name"]]
            raise AssertionError(("gh", "api", endpoint, args))
        raise AssertionError(args)
    return run_release


def require(condition, message):
    if not condition:
        raise AssertionError(message)


class AssetsBehavior(unittest.TestCase):
    """publish_assets combined-set checksum behavior against a deterministic fake store."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="m4b-assets-")
        self.addCleanup(self.temp.cleanup)
        self.work = Path(self.temp.name)
        dist = self.work / "dist"
        dist.mkdir()
        self.built = {name: f"archive-{name}-v2".encode() for name in ARCHIVE_NAMES}
        self.store = FakeStore()
        self.old_cwd = os.getcwd()
        os.chdir(self.work)
        self.addCleanup(os.chdir, self.old_cwd)

    def dist_bytes(self, name):
        return self.built[name]

    def write_dist(self, archives=None, checksum_names=None):
        if archives is None:
            archives = dict(self.built)
        if checksum_names is None:
            checksum_names = dict(self.built)
        items = []
        for name in ARCHIVE_NAMES:
            data = archives[name]
            (self.work / "dist" / name).write_bytes(data)
            system, arch = ARCHIVE_PLATFORM[name]
            items.append({"name": name, "path": f"dist/{name}", "goos": system, "goarch": arch,
                          "type": "Archive", "extra": {"Checksum": sha256(data)}})
        checksum = manifest(checksum_names)
        (self.work / "dist" / CHECKSUM_NAME).write_bytes(checksum)
        items.append({"name": CHECKSUM_NAME, "path": f"dist/{CHECKSUM_NAME}",
                      "type": "Checksum", "extra": {}})
        (self.work / "dist" / "artifacts.json").write_text(json.dumps(items))
        self.checksum_bytes = checksum

    def asset(self, index, name, data=None, digest=None, label=True):
        if digest is None:
            digest = sha256(self.built.get(name, b"")) if name in self.built else None
        label_text = (None if not label or digest is None else
                      release.asset_label(SOURCE, VERSION, digest))
        return {"id": index, "name": name, "digest": digest, "label": label_text}

    def run_assets(self):
        with mock.patch.object(release, "registry_run", store_upload(self.store)):
            release.publish_assets(REPOSITORY, SOURCE, VERSION, self.store.fetch)

    def uploaded(self):
        return {name: (label, body) for name, label, body in self.store.uploads}

    def test_fresh_assets_upload_complete_verified_set(self):
        self.write_dist()
        self.run_assets()
        uploaded = self.uploaded()
        self.assertEqual(set(uploaded), set(ARCHIVE_NAMES) | {CHECKSUM_NAME})
        for name in ARCHIVE_NAMES:
            self.assertEqual(uploaded[name][0], release.asset_label(SOURCE, VERSION,
                                                                    sha256(self.built[name])))
        self.assertEqual(uploaded[CHECKSUM_NAME][1], self.checksum_bytes)
        self.assertEqual(uploaded[CHECKSUM_NAME][0],
                         release.asset_label(SOURCE, VERSION, sha256(self.checksum_bytes)))

    def test_partial_retry_generates_manifest_from_preserved_bytes(self):
        preserved = "archive-preserved-v1-not-rebuildable".encode()
        name = ARCHIVE_NAMES[0]  # the rebuild bytes for this archive differ
        self.store.assets.append(self.asset(1, name, digest=sha256(preserved)))
        self.store.bodies[name] = preserved
        self.write_dist()
        self.run_assets()
        uploaded = self.uploaded()
        self.assertNotIn(name, uploaded)  # never replace an existing asset
        self.assertEqual(set(uploaded), set(ARCHIVE_NAMES) - {name} | {CHECKSUM_NAME})
        manifest_names = dict({n: self.built[n] for n in ARCHIVE_NAMES if n != name})
        manifest_names[name] = preserved
        self.assertEqual(uploaded[CHECKSUM_NAME][1], manifest(manifest_names))

    def test_preserved_archives_with_matching_checksum_are_kept(self):
        name_a, name_b = ARCHIVE_NAMES[0], ARCHIVE_NAMES[1]
        preserved_a, preserved_b = b"preserved-a", b"preserved-b"
        for index, (name, data) in enumerate(((name_a, preserved_a), (name_b, preserved_b)), 1):
            self.store.assets.append(self.asset(index, name, digest=sha256(data)))
            self.store.bodies[name] = data
        self.write_dist()
        manifest_names = dict(self.built)
        manifest_names[name_a], manifest_names[name_b] = preserved_a, preserved_b
        checksum_asset = self.asset(9, CHECKSUM_NAME, digest=sha256(manifest(manifest_names)),
                                    label=False)
        self.store.assets.append(checksum_asset)
        self.run_assets()
        uploaded = self.uploaded()
        self.assertEqual(set(uploaded), set(ARCHIVE_NAMES) - {name_a, name_b})
        self.assertNotIn(CHECKSUM_NAME, uploaded)  # immutable manifest preserved

    def test_inconsistent_existing_checksum_fails_without_writes(self):
        preserved = "archive-preserved-v1".encode()
        name = ARCHIVE_NAMES[0]
        self.store.assets.append(self.asset(1, name, digest=sha256(preserved)))
        self.store.bodies[name] = preserved
        self.write_dist()
        conflicting = manifest(dict(self.built))  # Goreleaser manifest for the rebuild
        self.store.assets.append(self.asset(9, CHECKSUM_NAME, digest=sha256(conflicting),
                                            label=False))
        with self.assertRaisesRegex(release.ReleaseError,
                                    "Existing checksum manifest does not describe the planned"):
            self.run_assets()
        self.assertEqual(self.store.uploads, [])

    def test_unlabelled_existing_archive_must_match_build(self):
        name = ARCHIVE_NAMES[2]
        other = "archive-other-bytes".encode()
        self.store.assets.append(self.asset(3, name, digest=sha256(other), label=False))
        self.store.bodies[name] = other
        self.write_dist()
        with self.assertRaisesRegex(release.ReleaseError, "differs from tested build"):
            self.run_assets()
        self.assertEqual(self.store.uploads, [])

    def test_duplicate_release_assets_fail_closed(self):
        self.write_dist()
        self.store.assets.append(self.asset(1, ARCHIVE_NAMES[0],
                                            digest=sha256(self.built[ARCHIVE_NAMES[0]])))
        self.store.assets.append(self.asset(2, ARCHIVE_NAMES[0],
                                            digest=sha256(self.built[ARCHIVE_NAMES[0]])))
        with self.assertRaisesRegex(release.ReleaseError, "Duplicate release asset"):
            self.run_assets()
        self.assertEqual(self.store.uploads, [])

    def test_stored_digest_conflicts_with_body_rejects(self):
        name = ARCHIVE_NAMES[3]
        recorded = "sha256:" + "9" * 64
        authoritative = "sha256:" + "9" * 63 + "1"  # label proves a different digest
        self.store.assets.append({"id": 4, "name": name, "digest": recorded,
                                  "label": release.asset_label(SOURCE, VERSION, authoritative)})
        self.store.bodies[name] = b"archive-real-bytes"
        self.write_dist()
        with self.assertRaisesRegex(release.ReleaseError, "Existing asset provenance differs"):
            self.run_assets()
        self.assertEqual(self.store.uploads, [])

    def test_existing_checksum_body_describing_planned_set_is_accepted(self):
        preserved = "archive-preserved-v1".encode()
        name = ARCHIVE_NAMES[0]
        self.store.assets.append(self.asset(1, name, digest=sha256(preserved)))
        self.store.bodies[name] = preserved
        self.write_dist()
        manifest_names = dict(self.built)
        manifest_names[name] = preserved
        # Digest field absent: the manifest must be verified from downloaded bytes.
        self.store.assets.append({"id": 9, "name": CHECKSUM_NAME, "digest": None, "label": ""})
        self.store.bodies[CHECKSUM_NAME] = manifest(manifest_names)
        self.run_assets()
        uploaded = self.uploaded()
        self.assertEqual(set(uploaded), set(ARCHIVE_NAMES) - {name})
        self.assertNotIn(CHECKSUM_NAME, uploaded)

    def test_list_failure_propagates_before_any_upload(self):
        self.write_dist()
        self.store.list_error = release.ReleaseError("HTTP 403: Resource forbidden by rate limit")
        with self.assertRaisesRegex(release.ReleaseError, "HTTP 403"):
            self.run_assets()
        self.assertEqual(self.store.uploads, [])

    def test_upload_transport_failure_fails_closed_without_replacement(self):
        self.write_dist()
        self.store.upload_error = 2  # succeed twice, then the registry transport dies
        with self.assertRaises(release.RegistryCommandError):
            self.run_assets()
        uploaded = {name for name, _, _ in self.store.uploads}
        self.assertEqual(len(self.store.uploads), 2)
        self.assertNotIn(CHECKSUM_NAME, uploaded, "partial progress leaves no incomplete manifest")


if __name__ == "__main__":
    unittest.main()
