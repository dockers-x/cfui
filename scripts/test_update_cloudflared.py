#!/usr/bin/env python3
"""Offline coverage for the dependency update command and its failure cases."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("update-cloudflared.sh").resolve()
CLOUDFLARED = "github.com/cloudflare/cloudflared"
QUIC = "github.com/quic-go/quic-go"
FORK = "github.com/cloudflare/quic-go"
TARGET = "v0.0.0-20261021105109-de6376c5af4a"


class UpdateCloudflaredTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.log = self.root / "calls.jsonl"
        self.upstream = self.root / "upstream.mod"
        self.current = self.root / "current.json"
        self.current.write_text(json.dumps({
            "Require": [
                {"Path": CLOUDFLARED, "Version": "v0.0.0-old"},
                {"Path": QUIC, "Version": "v0.50.0"},
            ],
            "Replace": [{"Old": {"Path": QUIC, "Version": ""},
                         "New": {"Path": FORK, "Version": "v0.50.0-fork"}}],
        }))
        # Delegate go.mod parsing to the real Go parser; all download and mutation
        # commands are recorded locally instead of accessing the network.
        self.go = shutil.which("go")
        self.env = dict(os.environ, PATH=f"{self.root}:{os.environ['PATH']}",
                        REAL_GO=self.go, TEST_ROOT=str(self.root), GOTOOLCHAIN="local")
        fake_go = self.root / "go"
        fake_go.write_text('''#!/usr/bin/env python3
import json, os, pathlib, subprocess, sys
root = pathlib.Path(os.environ["TEST_ROOT"])
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as log:
    log.write(json.dumps(args) + "\\n")
if args[:3] == ["mod", "download", "-json"]:
    if os.environ.get("TEST_DOWNLOAD_ERROR"):
        print(json.dumps({"Error": os.environ["TEST_DOWNLOAD_ERROR"]}))
        sys.exit(7)
    print(json.dumps({"Version": "TARGET", "GoMod": str(root / "upstream.mod")}))
elif args == ["mod", "edit", "-json"]:
    print((root / "current.json").read_text())
elif args[:3] == ["mod", "edit", "-json"]:
    sys.exit(subprocess.call([os.environ["REAL_GO"], *args]))
'''.replace('"TARGET"', repr(TARGET)))
        fake_go.chmod(0o755)

    def run_update(self, directive="", tag="2026.10.0", required=True):
        requirement = f"require (\n\t{QUIC} v0.60.0\n)" if required else ""
        self.upstream.write_text(f"module {CLOUDFLARED}\n\ngo 1.20\n\n{requirement}\n{directive}\n")
        result = subprocess.run(["bash", str(SCRIPT), tag], cwd=self.root,
                                env=self.env, text=True, capture_output=True)
        self.calls = [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []
        return result

    def assert_updated(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(["get", f"{CLOUDFLARED}@2026.10.0", f"{QUIC}@v0.60.0"], self.calls)
        self.assertEqual(self.calls[-1], ["mod", "tidy"])
        self.assertFalse(any("@latest" in arg for call in self.calls for arg in call))

    def test_single_line_replacement(self):
        result = self.run_update(f"replace {QUIC} => {FORK} v0.60.0-fork")
        self.assert_updated(result)
        self.assertIn(["mod", "edit", f"-replace={QUIC}={FORK}@v0.60.0-fork"], self.calls)

    def test_replacement_block(self):
        result = self.run_update(f"replace (\n\t{QUIC} => {FORK} v0.60.0-fork\n)")
        self.assert_updated(result)
        self.assertIn(["mod", "edit", f"-replace={QUIC}={FORK}@v0.60.0-fork"], self.calls)

    def test_upstream_without_replacement_drops_old_fork(self):
        self.assert_updated(self.run_update())
        self.assertIn(["mod", "edit", f"-dropreplace={QUIC}"], self.calls)
        self.assertFalse(any(arg.startswith("-replace=") for call in self.calls for arg in call))

    def test_version_specific_replacement(self):
        result = self.run_update(f"replace {QUIC} v0.60.0 => {FORK} v0.60.0-fork")
        self.assert_updated(result)
        self.assertIn(["mod", "edit", f"-replace={QUIC}@v0.60.0={FORK}@v0.60.0-fork"], self.calls)

    def test_stale_version_specific_replacement_is_removed(self):
        current = json.loads(self.current.read_text())
        current["Replace"][0]["Old"]["Version"] = "v0.50.0"
        self.current.write_text(json.dumps(current))
        self.assert_updated(self.run_update())
        self.assertIn(["mod", "edit", f"-dropreplace={QUIC}@v0.50.0"], self.calls)

    def test_same_cloudflared_with_stale_quic_is_repaired(self):
        current = json.loads(self.current.read_text())
        current["Require"][0]["Version"] = TARGET
        self.current.write_text(json.dumps(current))
        self.assert_updated(self.run_update())

    def test_invalid_release_queries_do_not_run_go(self):
        for tag in ["latest", "main", "2026.10.0-rc1", TARGET, "2026.10.0/branch"]:
            with self.subTest(tag=tag):
                self.assertNotEqual(self.run_update(tag=tag).returncode, 0)
                self.assertEqual(self.calls, [])

    def test_local_replacement_fails_before_mutating(self):
        result = self.run_update(f"replace {QUIC} => ../quic-go")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("local quic-go replacement", result.stderr)
        self.assertFalse(any(call[0] == "get" or any(arg.startswith("-replace=") for arg in call) for call in self.calls))

    def test_download_failure_reports_error_and_preserves_exit_status(self):
        self.env["TEST_DOWNLOAD_ERROR"] = "proxy request failed: connection timed out"
        result = self.run_update()
        self.assertEqual(result.returncode, 7)
        self.assertIn("Failed to download cloudflared release 2026.10.0", result.stderr)
        self.assertIn(self.env["TEST_DOWNLOAD_ERROR"], result.stderr)
        self.assertEqual(self.calls, [["mod", "download", "-json", f"{CLOUDFLARED}@2026.10.0"]])

    def test_missing_requirement_fails_before_mutating(self):
        result = self.run_update(required=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("missing or ambiguous quic-go requirement", result.stderr)

    def test_already_current_is_noop(self):
        self.current.write_text(json.dumps({"Require": [
            {"Path": CLOUDFLARED, "Version": TARGET},
            {"Path": QUIC, "Version": "v0.60.0"}], "Replace": []}))
        result = self.run_update()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("already at", result.stdout)
        self.assertFalse(any(call[0] == "get" or call == ["mod", "tidy"] for call in self.calls))


if __name__ == "__main__":
    unittest.main()
