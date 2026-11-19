"""Offline producer-proof tests; no GitHub/Apple credentials or writes."""
import copy
import importlib.util
from pathlib import Path
import unittest

MODULE = Path(__file__).resolve().parents[1] / "tool/verify_release_producer.py"
spec = importlib.util.spec_from_file_location("release_producer", MODULE)
producer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(producer)


def producer_fixture():
    expected = {"run_id": "123", "attempt": "2", "sha": "a" * 40, "channel": "beta",
                "artifact_id": "700", "artifact_name": "tailsplit-ios-beta-termitoc-38", "job_id": "800"}
    job = {"id": 800, "run_id": 123, "run_attempt": 2, "head_sha": "a" * 40,
           "name": "Sign and deliver beta with TERMITOC PTE. LTD.", "status": "completed", "conclusion": "success",
           "steps": [{"name": producer.UPLOAD_STEP, "number": 8, "status": "completed", "conclusion": "success",
                      "started_at": "2026-11-20T01:02:03Z", "completed_at": "2026-11-20T01:02:05Z"}]}
    log = (f"2026-11-20T01:02:03.1234567Z ##[group]Run {producer.UPLOAD_ACTION}\n"
           "2026-11-20T01:02:03.1234568Z   name: tailsplit-ios-beta-termitoc-38\n"
           "2026-11-20T01:02:03.1234569Z ##[endgroup]\n"
           "2026-11-20T01:02:05.9999999Z Artifact tailsplit-ios-beta-termitoc-38 has been successfully uploaded! Final size is 123 bytes. Artifact ID is 700\n")
    return job, log, expected


class ReleaseProducerTests(unittest.TestCase):
    def test_exact_original_channel_attempt_job_step_and_id_pass(self):
        job, log, expected = producer_fixture()
        self.assertTrue(producer.verify(job, log, **expected))

    def test_failed_skipped_wrong_job_run_attempt_source_or_channel_fail(self):
        job, log, expected = producer_fixture()
        for field, value in (("conclusion", "failure"), ("conclusion", "skipped"), ("status", "in_progress"),
                             ("id", 801), ("run_id", 124), ("run_attempt", 1), ("head_sha", "b" * 40),
                             ("name", "Build unsigned iOS archive (beta, no release credentials)"),
                             ("name", "Sign and deliver app-store with TERMITOC PTE. LTD.")):
            with self.subTest(field=field, value=value):
                changed = copy.deepcopy(job)
                changed[field] = value
                self.assertFalse(producer.verify(changed, log, **expected))

    def test_step_skipped_failed_missing_duplicate_or_success_without_artifact_fails(self):
        job, log, expected = producer_fixture()
        for steps in ([], job["steps"] * 2, [{**job["steps"][0], "conclusion": "skipped"}],
                      [{**job["steps"][0], "conclusion": "failure"}],
                      [{**job["steps"][0], "name": "some other artifact upload"}]):
            with self.subTest(steps=steps):
                self.assertFalse(producer.verify({**job, "steps": steps}, log, **expected))
        no_output = log[:log.index("2026-11-20T01:02:05")]
        self.assertFalse(producer.verify(job, no_output, **expected))

    def test_wrong_id_step_time_name_action_and_ambiguous_outputs_fail(self):
        job, log, expected = producer_fixture()
        cases = (log.replace("ID is 700", "ID is 701"), log.replace("01:02:05.9999999", "01:02:06.0000000"),
                 log.replace("  name: tailsplit-ios-beta", "  name: tailsplit-ios-app-store"),
                 log.replace(producer.UPLOAD_ACTION, "actions/upload-artifact@untrusted"),
                 log + log, log.replace("##[endgroup]", "##[endgroup]\n2026-11-20T01:02:04Z Post job cleanup."))
        for value in cases:
            with self.subTest(log=value):
                self.assertFalse(producer.verify(job, value, **expected))


if __name__ == "__main__":
    unittest.main()
