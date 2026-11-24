"""Exercise the bundled CLI helpers against the Go Bot skill format."""

import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


BUNDLE = Path(__file__).resolve().parents[2] / "internal/host/skills/skill-creator"
spec = importlib.util.spec_from_file_location(
    "quick_validate", BUNDLE / "scripts/quick_validate.py"
)
validator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validator)


class SkillCreatorTests(unittest.TestCase):
    def run_initializer(self, dsh, *args):
        return subprocess.run(
            [sys.executable, str(BUNDLE / "scripts/init_skill.py"), *args],
            env={**os.environ, "DSH_HOME": str(dsh), "PYTHONDONTWRITEBYTECODE": "1"},
            capture_output=True,
            text=True,
            check=False,
        )

    def test_initialize_complete_validate_and_preserve_existing_skill(self):
        with tempfile.TemporaryDirectory() as directory:
            dsh = Path(directory) / ".dsh"
            result = self.run_initializer(dsh, "daily-summary", "--resources", "references")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            skill = dsh / "skills/daily-summary"
            self.assertTrue((skill / "agents/openai.yaml").is_file())
            self.assertTrue((skill / "references").is_dir())
            # The upstream scaffold's TODO description is not a finished skill.
            self.assertFalse(validator.validate_skill(skill)[0])
            content = (
                "---\nname: daily-summary\ndescription: Write a daily summary\n"
                "disable-model-invocation: true\nuser-invocable: true\n"
                "whenToUse: Daily reporting\nmetadata: {version: 1}\n---\n"
                "Summarize the supplied notes.\n"
            )
            (skill / "SKILL.md").write_text(content, encoding="utf-8")
            self.assertTrue(validator.validate_skill(skill)[0])
            result = self.run_initializer(dsh, "daily-summary")
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((skill / "SKILL.md").read_text(), content)

    def test_explicit_project_path(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            project = root / "project/.dsh/skills"
            result = self.run_initializer(root / "account", "project-helper", "--path", str(project))
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertTrue((project / "project-helper/SKILL.md").is_file())
            self.assertFalse((root / "account").exists())

    def test_runtime_frontmatter_validation(self):
        cases = [
            ("name: valid-name\ndescription: Works\n", True),
            ("name: ''\ndescription: Works\n", False),
            ("name: valid-name\ndescription: ''\n", False),
            ("name: ' padded '\ndescription: Works\n", False),
            ("name: valid-name\ndescription: Works\nuserInvocable: true\n", False),
        ]
        for key in ("disable-model-invocation", "user-invocable"):
            for value in ("true", "false", "YES", "0", "1", "'OFF'", "1.0"):
                cases.append((f"name: valid-name\ndescription: Works\n{key}: {value}\n", True))
            for value in ("[]", "{}", "null", "2", "'false '"):
                cases.append((f"name: valid-name\ndescription: Works\n{key}: {value}\n", False))
        with tempfile.TemporaryDirectory() as directory:
            skill = Path(directory)
            for fields, expected in cases:
                with self.subTest(fields=fields):
                    (skill / "SKILL.md").write_text(f"---\n{fields}---\nDo the task.\n")
                    self.assertEqual(validator.validate_skill(skill)[0], expected)
            (skill / "SKILL.md").write_bytes(b"\xff")
            self.assertFalse(validator.validate_skill(skill)[0])
            (skill / "SKILL.md").write_text("---\nname: valid-name\ndescription: Works\n---\n" + "x" * (256 * 1024))
            self.assertFalse(validator.validate_skill(skill)[0])


if __name__ == "__main__":
    unittest.main()
