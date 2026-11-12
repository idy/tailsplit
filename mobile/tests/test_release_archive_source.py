"""Execute the workflow's archive selection shell with local GitHub API fixtures."""
import copy
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest

WORKFLOW = Path(__file__).resolve().parents[2] / ".github/workflows/mobile-release.yml"
BASH = shutil.which("bash")
JQ = shutil.which("jq")
REPOSITORY = "idy/tailsplit"
CURRENT_SHA = "b" * 40
SOURCE_SHA = "a" * 40
ZIP_BYTES = b"PK\x03\x04opaque archive fixture; never extract here\x00\xff"


def archive_input_script():
    match = re.search(r"(?ms)^        id: archive_input\n(.*?)(?=^      - |\Z)", WORKFLOW.read_text())
    if match is None or "        run: |\n" not in match[1]:
        raise AssertionError("The archive_input step must have a literal run block")
    return textwrap.dedent(match[1].split("        run: |\n", 1)[1])


GH_STUB = """import json
import os
from pathlib import Path
import sys

arguments = sys.argv[1:]
with Path(os.environ['GH_FIXTURE_LOG']).open('a') as log:
    log.write(json.dumps(arguments) + '\\n')
routes = json.loads(Path(os.environ['GH_FIXTURE_ROUTES']).read_text())
route = routes.get(arguments[-1]) if arguments else None
if route is None or arguments != route['arguments']:
    raise SystemExit('Unexpected mocked gh invocation')
if route.get('zip'):
    sys.stdout.buffer.write(Path(os.environ['GH_FIXTURE_ZIP']).read_bytes())
else:
    json.dump(route['payload'], sys.stdout)
"""


@unittest.skipUnless(BASH and JQ, "Archive shell execution tests require bash and jq")
class ReleaseArchiveSourceTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="tailsplit-archive-source-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.script = self.root / "archive-input.sh"
        self.script.write_text(archive_input_script())
        self.bin = self.root / "bin"
        self.bin.mkdir()
        stub = self.root / "gh_fixture.py"
        stub.write_text(GH_STUB)
        executable = self.bin / "gh"
        executable.write_text(f"#!{BASH}\nexec {shlex.quote(sys.executable)} -I -B {shlex.quote(str(stub))} \"$@\"\n")
        executable.chmod(0o700)
        self.zip = self.root / "opaque.zip"
        self.zip.write_bytes(ZIP_BYTES)
        self.counter = 0
        self.configure()

    def configure(self, channel="beta", reuse=True):
        self.environment = {
            "PATH": os.pathsep.join((str(self.bin), str(Path(JQ).parent), "/usr/bin", "/bin")),
            "LANG": "C", "GITHUB_REPOSITORY": REPOSITORY, "GITHUB_RUN_ID": "900", "GITHUB_RUN_NUMBER": "78",
            "GITHUB_SHA": CURRENT_SHA, "DEFAULT_BRANCH": "main", "RELEASE_CHANNEL": channel,
            "RELEASE_DESTINATION": "testflight", "ARCHIVE_RUN_ID": "123" if reuse else "",
            "ARTIFACT_ID": "501", "EXPECTED_NAME": f"tailsplit-ios-unsigned-{channel}-900-2",
        }
        self.source_run = {
            "event": "workflow_dispatch", "status": "completed", "head_branch": "main",
            "head_repository": {"full_name": REPOSITORY}, "path": ".github/workflows/mobile-release.yml",
            "head_sha": SOURCE_SHA, "run_number": 38,
            # A signing failure must not discard the successful unsigned build.
            "conclusion": "failure",
        }
        self.compare = {"status": "ahead", "merge_base_commit": {"sha": SOURCE_SHA}}
        self.jobs = [{"jobs": [{"name": "Unrelated job", "conclusion": "success"}]}, {"jobs": [{
            "name": f"Build unsigned iOS archive ({channel}, no release credentials)", "conclusion": "success",
        }]}]
        self.artifacts = {"artifacts": [
            {"id": 600, "name": f"tailsplit-ios-unsigned-{channel}-123-1", "expired": False,
             "created_at": "2026-11-12T01:00:00Z"},
            {"id": 601, "name": f"tailsplit-ios-unsigned-{channel}-123-2", "expired": False,
             "created_at": "2026-11-12T02:00:00Z"},
            {"id": 602, "name": "tailsplit-ios-unsigned-other-123-3", "expired": False,
             "created_at": "2026-11-12T03:00:00Z"},
            {"id": 603, "name": f"tailsplit-ios-unsigned-{channel}-123-4", "expired": True,
             "created_at": "2026-11-12T04:00:00Z"},
        ]}
        self.download_id = "601" if reuse else "501"
        self.artifact_owner = {
            "name": f"tailsplit-ios-unsigned-{channel}-123-2" if reuse else self.environment["EXPECTED_NAME"],
            "expired": False,
            "workflow_run": {"id": 123 if reuse else 900, "head_sha": SOURCE_SHA if reuse else CURRENT_SHA},
        }

    def execute(self):
        self.counter += 1
        case = self.root / f"case-{self.counter}"
        case.mkdir()
        output = case / "output"
        output.touch()
        self.routes = {}

        def route(endpoint, payload=None, paginate=False, zip_download=False):
            self.routes[endpoint] = {"arguments": ["api", *(["--paginate", "--slurp"] if paginate else []), endpoint],
                                     "payload": payload, "zip": zip_download}

        route(f"repos/{REPOSITORY}/actions/runs/123", self.source_run)
        route(f"repos/{REPOSITORY}/compare/{SOURCE_SHA}...{CURRENT_SHA}", self.compare)
        route(f"repos/{REPOSITORY}/compare/{CURRENT_SHA}...{CURRENT_SHA}", self.compare)
        route(f"repos/{REPOSITORY}/actions/runs/123/jobs?filter=all&per_page=100", self.jobs, paginate=True)
        route(f"repos/{REPOSITORY}/actions/runs/123/artifacts?per_page=100", self.artifacts)
        route(f"repos/{REPOSITORY}/actions/artifacts/{self.download_id}", self.artifact_owner)
        route(f"repos/{REPOSITORY}/actions/artifacts/{self.download_id}/zip", zip_download=True)
        routes = case / "routes.json"
        routes.write_text(json.dumps(self.routes))
        log = case / "gh-calls.jsonl"
        env = {**self.environment, "RUNNER_TEMP": str(case), "GITHUB_OUTPUT": str(output),
               "GH_FIXTURE_ROUTES": str(routes), "GH_FIXTURE_LOG": str(log), "GH_FIXTURE_ZIP": str(self.zip)}
        result = subprocess.run([BASH, "--noprofile", "--norc", "-e", "-o", "pipefail", str(self.script)],
                                cwd=case, env=env, capture_output=True, timeout=10)
        self.output = output.read_text()
        self.download = case / "release-input/unsigned-artifact.zip"
        self.calls = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
        return result

    def assert_rejected(self):
        result = self.execute()
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.output, "")
        self.assertFalse(self.download.exists())
        self.assertFalse(any(call[-1].endswith("/zip") for call in self.calls))

    def assert_downloaded(self, result, number):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.output, f"build-number={number}\n")
        self.assertEqual(self.download.read_bytes(), ZIP_BYTES)
        self.assertEqual(list(self.download.parent.iterdir()), [self.download])
        self.assertEqual(self.calls[-1], ["api", f"repos/{REPOSITORY}/actions/artifacts/{self.download_id}/zip"])

    def test_fresh_archive_uses_current_run_metadata_and_downloads_opaque_zip(self):
        for channel in ("beta", "app-store"):
            with self.subTest(channel=channel):
                self.configure(channel=channel, reuse=False)
                self.assert_downloaded(self.execute(), "78")
                self.assertEqual(len(self.calls), 2)
                self.assertEqual(self.calls[0], ["api", f"repos/{REPOSITORY}/actions/artifacts/501"])

    def test_reuse_preserves_source_number_and_selects_latest_unexpired_channel_artifact(self):
        for channel in ("beta", "app-store"):
            with self.subTest(channel=channel):
                self.configure(channel=channel)
                self.assert_downloaded(self.execute(), "38")
                self.assertEqual(len(self.calls), 6)
                self.assertIn(["api", f"repos/{REPOSITORY}/compare/{SOURCE_SHA}...{CURRENT_SHA}"], self.calls)
                self.assertIn(["api", "--paginate", "--slurp",
                               f"repos/{REPOSITORY}/actions/runs/123/jobs?filter=all&per_page=100"], self.calls)

    def test_reuse_allows_identical_source_commit(self):
        self.source_run["head_sha"] = CURRENT_SHA
        self.compare = {"status": "identical", "merge_base_commit": {"sha": CURRENT_SHA}}
        self.artifact_owner["workflow_run"]["head_sha"] = CURRENT_SHA
        self.assert_downloaded(self.execute(), "38")

    def test_invalid_source_run_id_fails_before_any_github_request(self):
        for value in ("0", "-1", "0123", "abc", "123; false", "123\n"):
            with self.subTest(value=value):
                self.environment["ARCHIVE_RUN_ID"] = value
                self.assert_rejected()
                self.assertEqual(self.calls, [])

    def test_fresh_archive_rejects_invalid_artifact_id_before_github_request(self):
        self.configure(reuse=False)
        for value in ("0", "-1", "artifact-id"):
            with self.subTest(value=value):
                self.environment["ARTIFACT_ID"] = value
                self.assert_rejected()
                self.assertEqual(self.calls, [])

    def test_reuse_rejects_wrong_source_event_branch_repository_workflow_or_status(self):
        for field, value in (("event", "push"), ("event", "pull_request"), ("head_branch", "feature"),
                             ("head_repository", {"full_name": "other/tailsplit"}),
                             ("path", ".github/workflows/other.yml"), ("status", "in_progress"),
                             ("status", "queued"), ("head_repository", None)):
            with self.subTest(field=field, value=value):
                self.configure()
                self.source_run[field] = value
                self.assert_rejected()
                self.assertEqual(len(self.calls), 1)

    def test_reuse_rejects_invalid_source_sha_or_original_build_number(self):
        for field, value in (("head_sha", "not-a-sha"), ("head_sha", "A" * 40),
                             ("run_number", 0), ("run_number", "38.1"), ("run_number", None)):
            with self.subTest(field=field, value=value):
                self.configure()
                self.source_run[field] = value
                self.assert_rejected()
                self.assertEqual(len(self.calls), 1)

    def test_reuse_requires_source_sha_to_be_an_ancestor_of_current_revision(self):
        for status, merge_base in (("diverged", SOURCE_SHA), ("behind", SOURCE_SHA),
                                   ("ahead", "c" * 40), ("identical", "c" * 40)):
            with self.subTest(status=status, merge_base=merge_base):
                self.compare = {"status": status, "merge_base_commit": {"sha": merge_base}}
                self.assert_rejected()
                self.assertEqual(len(self.calls), 2)

    def test_reuse_requires_a_successful_build_for_the_selected_channel(self):
        expected = self.jobs[1]["jobs"][0]
        for jobs in ([{**expected, "conclusion": "failure"}], [{**expected, "conclusion": "cancelled"}],
                     [{**expected, "name": "Build unsigned iOS archive (app-store, no release credentials)"}], []):
            with self.subTest(jobs=jobs):
                self.jobs = [{"jobs": jobs}]
                self.assert_rejected()
                self.assertEqual(len(self.calls), 3)

    def test_reuse_rejects_wrong_channel_missing_expired_or_wrong_run_artifacts(self):
        selected = copy.deepcopy(self.artifacts["artifacts"][1])
        for artifacts in ([], [{**selected, "expired": True}],
                          [{**selected, "name": "tailsplit-ios-unsigned-app-store-123-2"}],
                          [{**selected, "name": "tailsplit-ios-unsigned-beta-999-2"}]):
            with self.subTest(artifacts=artifacts):
                self.artifacts = {"artifacts": artifacts}
                self.assert_rejected()
                self.assertEqual(len(self.calls), 4)

    def test_final_artifact_metadata_rejects_owner_sha_run_name_or_expiry_mismatch(self):
        for reuse in (False, True):
            for field, value in (("head_sha", "c" * 40), ("id", 456), ("name", "other-archive"), ("expired", True)):
                with self.subTest(reuse=reuse, field=field):
                    self.configure(reuse=reuse)
                    target = self.artifact_owner["workflow_run"] if field in ("head_sha", "id") else self.artifact_owner
                    target[field] = value
                    self.assert_rejected()
                    self.assertEqual(self.calls[-1], ["api", f"repos/{REPOSITORY}/actions/artifacts/{self.download_id}"])


if __name__ == "__main__":
    unittest.main()
