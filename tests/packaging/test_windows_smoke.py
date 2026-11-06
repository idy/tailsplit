"""Windows smoke cleanup retries preserve archive-validation failures."""
from contextlib import redirect_stderr
import importlib.util
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("windows_cli_smoke", ROOT / "tests/packaging/windows.py")
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


def windows_error(code):
    error = PermissionError("Windows fixture lock")
    error.winerror = code
    return error


class SmokeCleanupTests(unittest.TestCase):
    def test_transient_executable_lock_is_retried(self):
        for code in (32, 33):
            with self.subTest(code=code), \
                    patch.object(smoke.shutil, "rmtree", side_effect=[windows_error(code), windows_error(code), None]) as remove, \
                    patch.object(smoke.time, "sleep") as wait:
                smoke.cleanup_smoke_directory("fixture-only")
                self.assertEqual(remove.call_count, 3)
                self.assertEqual(wait.call_count, 2)

    def test_other_errors_are_not_retried(self):
        error = windows_error(5)
        with patch.object(smoke.shutil, "rmtree", side_effect=error) as remove, \
                patch.object(smoke.time, "sleep") as wait:
            with self.assertRaises(PermissionError) as raised:
                smoke.cleanup_smoke_directory("fixture-only")
            self.assertIs(raised.exception, error)
            self.assertEqual(remove.call_count, 1)
            wait.assert_not_called()

    def test_persistent_lock_has_a_finite_budget_and_fails(self):
        error = windows_error(32)
        with patch.object(smoke.shutil, "rmtree", side_effect=error) as remove, \
                patch.object(smoke.time, "sleep") as wait:
            with self.assertRaises(PermissionError):
                smoke.cleanup_smoke_directory("fixture-only")
            self.assertEqual(remove.call_count, 20)
            self.assertEqual(wait.call_count, 19)

    def test_cleanup_failure_does_not_mask_an_archive_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            original = ValueError("archive checksum mismatch")
            warning = io.StringIO()
            with patch.object(smoke.tempfile, "mkdtemp", return_value=directory), \
                    patch.object(smoke, "cleanup_smoke_directory", side_effect=windows_error(32)), \
                    redirect_stderr(warning):
                with self.assertRaises(ValueError) as raised:
                    with smoke.smoke_directory():
                        raise original
            self.assertIs(raised.exception, original)
            self.assertIn("cleanup failed", warning.getvalue())

    def test_successful_verification_still_fails_on_permanent_cleanup_error(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(smoke.tempfile, "mkdtemp", return_value=directory), \
                    patch.object(smoke, "cleanup_smoke_directory", side_effect=windows_error(32)):
                with self.assertRaises(PermissionError):
                    with smoke.smoke_directory():
                        pass

    def test_normal_context_removes_its_own_temporary_contents(self):
        with smoke.smoke_directory() as directory:
            path = Path(directory)
            (path / "tailsplit.exe").write_bytes(b"fixture")
        self.assertFalse(path.exists())


if __name__ == "__main__":
    unittest.main()
