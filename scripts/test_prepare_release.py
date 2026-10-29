"""Exercise version allocation and atomic publishing against local Git remotes."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("prepare-cloudflared-release.sh").resolve()


class PrepareReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.remote = self.root / "remote.git"
        self.git("init", "--bare", str(self.remote))
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Test")
        self.git("config", "user.email", "test@example.com")
        (self.repo / "go.mod").write_text("module example.com/test\n")
        (self.repo / "go.sum").write_text("")
        self.git("add", ".")
        self.git("commit", "-m", "initial")
        self.git("remote", "add", "origin", str(self.remote))
        self.git("push", "origin", "main")
        self.output = self.root / "output"
        self.bin = self.root / "bin"
        self.bin.mkdir()
        gh = self.bin / "gh"
        gh.write_text('#!/bin/sh\nprintf "%s" "$RELEASE_ID"\nexit "$GH_EXIT"\n')
        gh.chmod(0o755)
        self.env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}",
                        GITHUB_OUTPUT=str(self.output), RELEASE_ID="", GH_EXIT="0")

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, check=True,
                              text=True, capture_output=True).stdout.strip()

    def run_script(self, tag="2026.10.5", check=True):
        return subprocess.run(["bash", str(SCRIPT), tag, "main"], cwd=self.repo,
                              env=self.env, check=check, text=True, capture_output=True)

    def change_dependency(self):
        (self.repo / "go.mod").write_text("module example.com/test\n// updated\n")

    def test_unchanged_without_tag_does_not_release(self):
        self.run_script()
        self.assertEqual(self.output.read_text(), "tag=\n")

    def test_next_patch_includes_drafts_and_ignores_prereleases(self):
        for tag in ["v0.9.9", "v0.10.6", "v0.10.7", "v99.0.0-beta.1"]:
            self.git("tag", tag)
        self.change_dependency()
        self.run_script()
        self.assertEqual(self.output.read_text(), "tag=v0.10.8\n")
        head = self.git("rev-parse", "HEAD")
        self.assertEqual(self.git("rev-parse", "v0.10.8^{}"), head)
        self.assertEqual(self.git("--git-dir", str(self.remote), "rev-parse", "main"), head)
        self.assertEqual(self.git("--git-dir", str(self.remote), "rev-parse", "v0.10.8^{}"), head)
        self.assertEqual(self.git("status", "--porcelain"), "")
        # A failed build can resume without a new commit or version.
        self.output.unlink()
        self.run_script()
        self.assertEqual(self.output.read_text(), "tag=v0.10.8\n")
        self.assertEqual(self.git("rev-parse", "HEAD"), head)

    def test_first_release(self):
        self.change_dependency()
        self.run_script()
        self.assertEqual(self.output.read_text(), "tag=v0.1.0\n")

    def test_published_tag_is_skipped(self):
        self.git("tag", "v1.0.0")
        self.env["RELEASE_ID"] = "123"
        self.run_script()
        self.assertEqual(self.output.read_text(), "tag=\n")

    def test_unpublished_tag_is_retried(self):
        self.git("tag", "v1.0.0")
        self.run_script()
        self.assertEqual(self.output.read_text(), "tag=v1.0.0\n")

    def test_release_api_failure_does_not_publish(self):
        self.git("tag", "v1.0.0")
        self.env["GH_EXIT"] = "1"
        result = self.run_script(check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())

    def test_branch_race_does_not_push_tag(self):
        self.git("tag", "v1.0.0")
        initial = self.git("rev-parse", "HEAD")
        self.git("commit", "--allow-empty", "-m", "concurrent change")
        advanced = self.git("rev-parse", "HEAD")
        self.git("push", "origin", "main")
        self.git("reset", "--hard", initial)
        self.change_dependency()
        result = self.run_script(check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())
        self.assertEqual(self.git("--git-dir", str(self.remote), "rev-parse", "main"), advanced)
        self.assertEqual(self.git("--git-dir", str(self.remote), "tag", "--list", "v1.0.1"), "")

    def test_invalid_upstream_tag(self):
        self.change_dependency()
        before = self.git("rev-parse", "HEAD")
        result = self.run_script(tag="latest", check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.git("rev-parse", "HEAD"), before)


if __name__ == "__main__":
    unittest.main()
