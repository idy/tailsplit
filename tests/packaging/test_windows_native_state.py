"""The no-cloud desktop fixture keeps migration and installer checks distinct."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("native_desktop_state_fixture", ROOT / "tests/packaging/windows_desktop.py")
fixture = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(fixture)


class ServiceOwnedFixtureTests(unittest.TestCase):
    def test_seed_requires_initialized_unused_primary_and_system_ownership(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime = Path(temporary)
            (runtime / ".initialized").write_bytes(b"2\n")
            sentinel = b"fixture-only-state"
            def private(path, sid):
                self.assertEqual(sid, "S-1-5-18")
                Path(path).mkdir()
            with patch.object(fixture, "enable_fixture_owner_assignment") as enable, \
                    patch.object(fixture, "private_directory", side_effect=private) as create:
                state = fixture.seed_service_owned_fixture(runtime, sentinel)
            enable.assert_called_once_with()
            self.assertEqual(state.read_bytes(), sentinel)
            self.assertEqual((runtime / ".initialized").read_bytes(), b"2\n")
            create.assert_called_once_with(runtime / "tailscale", "S-1-5-18")

    def test_seed_never_overwrites_existing_or_uninitialized_state(self):
        for initialized in (False, True):
            for existing in (False, True):
                if initialized and not existing:
                    continue
                with self.subTest(initialized=initialized, existing=existing), tempfile.TemporaryDirectory() as temporary:
                    runtime = Path(temporary)
                    if initialized:
                        (runtime / ".initialized").write_bytes(b"2\n")
                    state = runtime / "tailscale/tailscaled.state"
                    if existing:
                        state.parent.mkdir()
                        state.write_bytes(b"existing-state")
                    with patch.object(fixture, "private_directory") as create:
                        with self.assertRaisesRegex(RuntimeError, "without a primary identity"):
                            fixture.seed_service_owned_fixture(runtime, b"replacement")
                    create.assert_not_called()
                    if existing:
                        self.assertEqual(state.read_bytes(), b"existing-state")
                    else:
                        self.assertFalse(state.parent.exists())


if __name__ == "__main__":
    unittest.main()
