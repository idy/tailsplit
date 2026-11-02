import contextlib
import importlib.util
import io
from pathlib import Path
import plistlib
import subprocess
import sys
import unittest

SPEC = importlib.util.spec_from_file_location("verify_macos", Path(__file__).with_name("verify-macos.py"))
verify = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verify)


class CommandOutputTests(unittest.TestCase):
    def test_plist_output_excludes_trailing_tool_warning(self):
        command = "import sys; sys.stdout.buffer.write(%r); sys.stdout.flush(); sys.stderr.write('hdiutil: deprecation warning\\n')" % plistlib.dumps({"system-entities": []})
        output = verify.run(sys.executable, "-I", "-c", command, merge_stderr=False)
        self.assertEqual(plistlib.loads(output), {"system-entities": []})

    def test_default_keeps_codesign_stderr_metadata(self):
        output = verify.run(sys.executable, "-I", "-c", "import sys; sys.stderr.write('TeamIdentifier=TEST\\n')")
        self.assertEqual(output, b"TeamIdentifier=TEST\n")

    def test_failure_retains_both_output_streams(self):
        diagnostics = io.StringIO()
        with contextlib.redirect_stdout(diagnostics), self.assertRaises(subprocess.CalledProcessError) as failure:
            verify.run(sys.executable, "-I", "-c", "import sys; print('partial output'); sys.stderr.write('attach failed\\n'); sys.exit(2)", merge_stderr=False)
        self.assertEqual(failure.exception.returncode, 2)
        self.assertIn(b"partial output", failure.exception.output)
        self.assertIn(b"attach failed", failure.exception.stderr)
        self.assertIn("partial output", diagnostics.getvalue())
        self.assertIn("attach failed", diagnostics.getvalue())


if __name__ == "__main__":
    unittest.main()
