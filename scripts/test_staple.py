import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("staple-macos.sh")


class StapleTests(unittest.TestCase):
    def run_stapler(self, failures, code=68, arguments=None, operation="staple"):
        with tempfile.TemporaryDirectory(prefix="tailsplit-staple-test-") as temporary:
            directory = Path(temporary)
            (directory / "xcrun").write_text(
                "#!/bin/sh\n"
                'test "$#" = 3 && test "$1:$2" = "stapler:$TAILSPLIT_TEST_OPERATION" || exit 64\n'
                'test "$3" = "artifact with spaces.pkg" || exit 64\n'
                "count=0\n"
                'test ! -e "$TAILSPLIT_TEST_DIR/count" || count=$(cat "$TAILSPLIT_TEST_DIR/count")\n'
                "count=$((count + 1))\n"
                'echo "$count" > "$TAILSPLIT_TEST_DIR/count"\n'
                'test "$count" -gt "$TAILSPLIT_TEST_FAILURES" || exit "$TAILSPLIT_TEST_CODE"\n'
            )
            (directory / "sleep").write_text(
                '#!/bin/sh\nprintf "%s\\n" "$1" >> "$TAILSPLIT_TEST_DIR/delays"\n'
            )
            for name in ("xcrun", "sleep"):
                (directory / name).chmod(0o700)
            environment = dict(
                os.environ,
                PATH=str(directory) + os.pathsep + os.environ.get("PATH", "/usr/bin:/bin"),
                TAILSPLIT_TEST_DIR=str(directory),
                TAILSPLIT_TEST_FAILURES=str(failures),
                TAILSPLIT_TEST_CODE=str(code),
                TAILSPLIT_TEST_OPERATION=operation,
            )
            if arguments is None:
                arguments = ["artifact with spaces.pkg"]
                if operation == "validate":
                    arguments.insert(0, "--validate")
            result = subprocess.run(
                ["sh", str(SCRIPT), *arguments],
                env=environment, capture_output=True, text=True, timeout=10,
            )
            count = int((directory / "count").read_text()) if (directory / "count").exists() else 0
            delays = (directory / "delays").read_text().splitlines() if (directory / "delays").exists() else []
            return result, count, delays

    def test_recovers_from_cloudkit_connection_loss(self):
        result, count, delays = self.run_stapler(2)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(count, 3)
        self.assertEqual(delays, ["2", "4"])

    def test_temporary_failure_is_also_retried(self):
        result, count, delays = self.run_stapler(1, code=75)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(count, 2)
        self.assertEqual(delays, ["2"])

    def test_validation_recovers_from_the_same_cloudkit_failure(self):
        result, count, delays = self.run_stapler(2, operation="validate")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(count, 3)
        self.assertEqual(delays, ["2", "4"])

    def test_invalid_validation_ticket_is_not_retried(self):
        result, count, delays = self.run_stapler(10, code=65, operation="validate")
        self.assertEqual(result.returncode, 65)
        self.assertEqual(count, 1)
        self.assertEqual(delays, [])

    def test_permanent_failure_is_not_retried(self):
        result, count, delays = self.run_stapler(10, code=65)
        self.assertEqual(result.returncode, 65)
        self.assertEqual(count, 1)
        self.assertEqual(delays, [])

    def test_network_retries_are_bounded_and_do_not_hide_failure(self):
        result, count, delays = self.run_stapler(10)
        self.assertEqual(result.returncode, 68)
        self.assertEqual(count, 5)
        self.assertEqual(delays, ["2", "4", "8", "16"])

    def test_missing_argument_never_invokes_stapler(self):
        result, count, delays = self.run_stapler(0, arguments=[])
        self.assertEqual(result.returncode, 64)
        self.assertEqual(count, 0)
        self.assertEqual(delays, [])


if __name__ == "__main__":
    unittest.main()
