import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("installer", Path(__file__).resolve().parents[1] / "install_gigacode_skills.py")
installer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(installer)


class InstallFixture:
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.project = self.base / "project with spaces"
        self.project.mkdir()
        subprocess.run(["git", "-C", str(self.project), "init", "-q"], check=True)
        self.binary = self.base / "seai"
        self.binary.write_text("#!/bin/sh\nexit 0\n")
        self.binary.chmod(0o700)
        self.data = self.base / "runtime"

    def install(self, layout="cli"):
        return installer.install(self.project, self.binary, self.data, layout)


class InstallTests(InstallFixture, unittest.TestCase):
    def test_preserves_user_memory_and_is_idempotent(self):
        memory = self.project / "GIGACODE.md"
        memory.write_text("User instructions\n")
        (self.project / ".gitignore").write_text("/user-cache/\n")
        first = self.install()
        self.assertTrue(first["written"])
        self.assertTrue(memory.read_text().startswith("User instructions\n"))
        self.assertEqual(memory.read_text().count("SEAI-ONLY:BEGIN"), 1)
        self.assertEqual(len(list((self.project / ".gigacode/skills").glob("*/SKILL.md"))), 5)
        self.assertEqual(self.install()["written"], [])
        ignored = subprocess.check_output(["git", "-C", str(self.project), "check-ignore", ".gigacode/seai/session.json"], text=True)
        self.assertTrue(ignored.strip())
        self.assertEqual((self.project / ".gigacode/seai/session.json").stat().st_mode & 0o777, 0o600)

    def test_conflict_causes_no_partial_installation(self):
        dest = self.project / ".gigacode/skills/seai-review/SKILL.md"
        dest.parent.mkdir(parents=True)
        dest.write_text("user-edited skill")
        with self.assertRaises(ValueError):
            self.install()
        self.assertEqual(dest.read_text(), "user-edited skill")
        self.assertFalse((self.project / "GIGACODE.md").exists())
        self.assertFalse((self.project / ".gigacode/seai/session.json").exists())

    def test_runtime_in_source_and_nonroot_are_rejected(self):
        with self.assertRaises(ValueError):
            installer.install(self.project, self.binary, self.project / "runtime", "cli")
        nested = self.project / "nested"
        nested.mkdir()
        with self.assertRaises(ValueError):
            installer.install(nested, self.binary, self.data, "cli")

    def test_vscode_layout_and_symlink_destination(self):
        self.install("vscode")
        self.assertEqual(len(list((self.project / ".gigacode_vsc/skills").glob("*/SKILL.md"))), 5)
        other = self.base / "outside"
        other.mkdir()
        (self.project / ".gigacode/skills").symlink_to(other, target_is_directory=True)
        with self.assertRaises(ValueError):
            self.install()
        self.assertEqual(list(other.iterdir()), [])

    def test_edited_managed_policy_is_preserved(self):
        self.install()
        memory = self.project / "GIGACODE.md"
        memory.write_text(memory.read_text().replace("SEAI-only", "User policy"))
        before = memory.read_bytes()
        with self.assertRaises(ValueError):
            self.install()
        self.assertEqual(memory.read_bytes(), before)

class WorkspaceTests(InstallFixture, unittest.TestCase):
    def test_parent_workspace_installs_startup_rule_at_opened_root(self):
        original = self.project / "GIGACODE.md"
        original.write_text("Child project instructions\n")
        result = installer.install(self.project, self.binary, self.data, "cli", self.base)
        self.assertEqual(result["workspace"], str(self.base.resolve()))
        self.assertTrue((self.base / "GIGACODE.md").is_file())
        self.assertFalse((self.project / ".gigacode").exists())
        self.assertEqual(original.read_text(), "Child project instructions\n")
        self.assertEqual(len(list((self.base / ".gigacode/skills").glob("*/SKILL.md"))), 5)
        self.assertEqual(installer.install(self.project, self.binary, self.data, "cli", self.base)["written"], [])
        status = installer.check(self.base, self.project)
        self.assertEqual(status["status"], "files_configured")
        self.assertEqual(status["project"], str(self.project.resolve()))
        self.assertFalse(status["client_loaded_verified"])

    def test_opening_parent_of_existing_project_setup_is_diagnosed(self):
        self.install()
        status = installer.check(self.base, self.project)
        self.assertEqual(status["status"], "issues_found")
        self.assertIn("NESTED_RULE_IS_NOT_WORKSPACE_RULE", {i["code"] for i in status["issues"]})
        self.assertFalse((self.base / "GIGACODE.md").exists())

    def test_workspace_must_contain_selected_project(self):
        outside = self.base / "unrelated"
        outside.mkdir()
        with self.assertRaises(ValueError):
            installer.install(self.project, self.binary, self.data, "cli", outside)
        self.assertFalse((outside / "GIGACODE.md").exists())

    def test_check_requires_the_selected_client_layout(self):
        self.install("vscode")
        self.assertEqual(installer.check(self.project, layout="vscode")["status"], "files_configured")
        status = installer.check(self.project, layout="cli")
        self.assertIn("INCOMPLETE_SKILL_SET", {i["code"] for i in status["issues"]})

    def test_probe_rejects_daemon_for_another_project(self):
        import json
        self.binary.write_text("#!/usr/bin/env python3\nimport json,sys\nprint(json.dumps({'schema_version':'seai.api/v1','status':'ok','data':{'root':'/different/project'}}))\n")
        self.install()
        status = installer.check(self.project, probe=True)
        self.assertIn("DAEMON_PROJECT_MISMATCH", {i["code"] for i in status["issues"]})

    def test_cli_check_exit_code_reports_wrong_opened_directory(self):
        import sys
        import json
        self.install()
        result = subprocess.run([sys.executable, str(installer.SOURCE / "scripts/install_gigacode_skills.py"), "--check", "--workspace", str(self.base), "--project", str(self.project)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 3)
        self.assertEqual(json.loads(result.stdout)["status"], "issues_found")


if __name__ == "__main__":
    unittest.main()
