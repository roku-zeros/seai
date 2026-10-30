import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("installer", Path(__file__).resolve().parents[1] / "install_gigacode_skills.py")
installer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(installer)


class InstallTests(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
