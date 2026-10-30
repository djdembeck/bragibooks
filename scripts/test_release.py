"""Real temporary git-history release reservation regressions; no production writes."""


from pathlib import Path

import subprocess

import tempfile

import unittest

import release

class LocalGitHub(release.Repository):

    def __init__(self, root, remote):
        super().__init__(root)
        self.remote = remote

    def repository(self):
        return 'example/m4b-merge'

    def remote_git(self, *args):
        return self.run('git', '--git-dir', str(self.remote), *args)

    def api(self, endpoint, method='GET', fields=None):
        path = endpoint.removeprefix('repos/example/m4b-merge/')
        if path == 'git/refs' and method == 'POST':
            self.remote_git('update-ref', fields['ref'], fields['sha'], '0' * 40)
            return {'ref': fields['ref']}
        if path.startswith('git/ref/'):
            ref = path.removeprefix('git/ref/')
            return {'object': {'type': 'commit', 'sha': self.remote_git('rev-parse', f'refs/{ref}^{{commit}}')}}
        raise AssertionError((endpoint, method, fields))

class ReleaseBehavior(unittest.TestCase):

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='m4b-reservation-')
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.remote = root / 'remote.git'
        self.checkout = root / 'checkout'
        self.cmd(root, 'git', 'init', '--bare', str(self.remote))
        self.cmd(root, 'git', 'init', '-b', 'main', str(self.checkout))
        self.repo = LocalGitHub(self.checkout, self.remote)
        for key, value in (('user.name', 'Release Test'), ('user.email', 'test@example.invalid'), ('commit.gpgsign', 'false'), ('tag.gpgsign', 'false')):
            self.git('config', key, value)
        self.git('remote', 'add', 'origin', str(self.remote))
        self.commit('initial', 'one')
        self.git('tag', 'v1.0.0')
        self.git('push', 'origin', 'main', '--tags')
        self.git('switch', '-c', 'develop')
        self.commit('feat: new behavior', 'two')
        self.git('push', 'origin', 'develop')
        self.git('switch', 'main')
        self.git('merge', '--no-ff', 'develop', '-m', 'Promote develop')
        self.git('push', 'origin', 'main')
        self.source = self.git('rev-parse', 'HEAD')
        self.repo.fetch()

    def cmd(self, cwd, *args):
        result = subprocess.run(args, cwd=cwd, env=release.isolated_git_environment(), text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def git(self, *args):
        return self.cmd(self.checkout, 'git', *args)

    def commit(self, message, content):
        (self.checkout / 'source.txt').write_text(content)
        self.git('add', 'source.txt')
        self.git('commit', '-m', message)

    def next_develop(self):
        self.git('switch', 'develop')
        self.commit('fix: next behavior', 'three')
        self.git('push', 'origin', 'develop')
        self.repo.fetch()
        return self.git('rev-parse', 'HEAD')

    def test_reservation_reuses_exact_source_after_newer_release(self):
        tag = self.repo.reserve(self.source)
        self.assertEqual(tag, 'v1.1.0')
        self.next_develop()
        self.git('switch', 'main')
        self.git('merge', '--no-ff', 'develop', '-m', 'Next promotion')
        self.git('push', 'origin', 'main')
        newer = self.git('rev-parse', 'HEAD')
        self.assertEqual(self.repo.reserve(newer), 'v1.1.1')
        self.assertEqual(self.repo.reserve(self.source), tag)
        self.assertEqual(self.repo.remote_git('rev-parse', 'refs/tags/v1.1.0'), self.source)

    def test_unfinished_reservation_is_not_reallocated(self):
        self.assertEqual(self.repo.reserve(self.source), 'v1.1.0')
        self.assertEqual(self.repo.reserve(self.source, bump='major'), 'v1.1.0')
        self.assertEqual(self.repo.select(self.source, 'v1.1.0'), 'v1.1.0')
        with self.assertRaises(release.ReleaseError):
            self.repo.reserve(self.source, 'v2.0.0')

    def test_other_branch_tag_cannot_set_reachable_baseline(self):
        self.git('switch', '-c', 'unrelated', 'v1.0.0')
        self.commit('unrelated', 'elsewhere')
        self.git('tag', 'v99.0.0')
        self.git('push', 'origin', 'v99.0.0')
        self.repo.fetch()
        self.assertEqual(self.repo.select(self.source), 'v1.1.0')

    def test_explicit_different_source_collision_never_moves_tag(self):
        with self.assertRaises(release.ReleaseError):
            self.repo.reserve(self.source, 'v1.0.0')
        self.assertEqual(self.repo.remote_git('rev-parse', 'refs/tags/v1.0.0'), self.git('rev-parse', 'v1.0.0'))

    def test_ambiguous_exact_source_reservations_fail(self):
        for tag in ('v1.1.0', 'v1.2.0'):
            self.git('tag', tag, self.source)
            self.git('push', 'origin', tag)
        self.repo.fetch()
        with self.assertRaises(release.ReleaseError):
            self.repo.select(self.source)

    def test_release_refuses_tag_pointing_at_another_source(self):
        initial = self.git('rev-parse', 'v1.0.0')
        self.assertNotEqual(initial, self.source)
        # Release metadata routes raise AssertionError in this fixture, so a
        # ReleaseError proves the frozen-source check failed closed first.
        with self.assertRaisesRegex(release.ReleaseError, 'does not point at the frozen source'):
            self.repo.release('v1.0.0', self.source, publish=True)
        self.assertEqual(self.repo.remote_git('rev-parse', 'refs/tags/v1.0.0'), initial)

    def test_unreserved_automatic_candidate_is_coalesced_but_manual_is_not(self):
        old = self.source
        self.next_develop()
        self.git('switch', 'main')
        self.git('merge', '--no-ff', 'develop', '-m', 'Next promotion')
        self.git('push', 'origin', 'main')
        self.repo.reserve(self.git('rev-parse', 'HEAD'), 'v1.2.0')
        self.repo.fetch()
        self.assertIsNone(self.repo.select(old, automatic=True))
        self.assertEqual(self.repo.select(old), 'v1.1.0')

if __name__ == "__main__":
    unittest.main()
