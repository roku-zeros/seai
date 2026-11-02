import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("mcp_config", Path(__file__).resolve().parents[1] / "configure_gigacode_mcp.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class MCPConfigurationTests(unittest.TestCase):
    def test_merge_keeps_other_settings_and_trust_is_scoped(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            project = root / "project"
            project.mkdir()
            data = root / "runtime"
            data.mkdir()
            binary = root / "seai"
            binary.write_text("#!/bin/sh\nexit 0\n")
            binary.chmod(0o700)
            folder = project / ".gigacode"
            folder.mkdir()
            settings = folder / "settings.json"
            settings.write_text(json.dumps({"approvalMode": "unchanged", "mcpServers": {"other": {"command": "original"}}}))
            module.configure(project, project, binary, data, True, True, False)
            result = json.loads(settings.read_text())
            self.assertEqual(result["approvalMode"], "unchanged")
            self.assertEqual(result["mcpServers"]["other"], {"command": "original"})
            self.assertTrue(result["mcpServers"]["seai"]["trust"])
            self.assertFalse(result["mcpServers"]["seai-candidate"]["trust"])
            before = settings.read_bytes()
            module.configure(project, project, binary, data, True, True, False)
            self.assertEqual(before, settings.read_bytes())
            with self.assertRaises(ValueError):
                module.configure(project, project, binary, data, True, True, True)
            self.assertEqual(before, settings.read_bytes())

    def test_research_server_is_separate_and_trust_is_explicit(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            project = root / "repo"
            project.mkdir()
            data = root / "runtime"
            data.mkdir()
            binary = root / "seai"
            binary.write_text("#!/bin/sh\nexit 0\n")
            binary.chmod(0o700)
            module.configure(project, project, binary, data, research=True, trust_research=True)
            servers = json.loads((project / ".gigacode/settings.json").read_text())["mcpServers"]
            self.assertFalse(servers["seai"]["trust"])
            research = servers["seai_research"]
            self.assertTrue(research["trust"])
            self.assertEqual(research["args"][-1], "--research")
            self.assertNotIn("seai_candidate", research["includeTools"])
            self.assertEqual(len(research["includeTools"]), 4)
