#!/usr/bin/env python3
"""Verify that an original protected signing job actually uploaded a candidate ID.

Inputs come from GitHub's job/artifact APIs and the selected job's raw log, not
from either artifact. No archive contents or log commands are executed.
"""
import argparse
import datetime as dt
import json
from pathlib import Path
import re

UPLOAD_STEP = "Retain the verified organization-signed IPA and TestFlight receipt"
UPLOAD_ACTION = "actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02"


def instant(value):
    value = re.sub(r"(\.\d{6})\d+(?=Z|\+)", r"\1", value)
    return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))


def verify(job, log, *, run_id, attempt, sha, channel, artifact_id, artifact_name, job_id):
    """Require the exact successful sign job/step, action, name and emitted ID."""
    if (str(job.get("id")) != job_id or str(job.get("run_id")) != run_id or str(job.get("run_attempt")) != attempt
            or job.get("head_sha") != sha
            or job.get("name") != f"Sign and deliver {channel} with TERMITOC PTE. LTD."
            or job.get("status") != "completed" or job.get("conclusion") != "success"):
        return False
    steps = [step for step in job.get("steps", []) if step.get("name") == UPLOAD_STEP]
    if len(steps) != 1 or steps[0].get("status") != "completed" or steps[0].get("conclusion") != "success":
        return False
    step = steps[0]
    start = instant(step["started_at"])
    # Job API timestamps have second precision; raw action logs have fractions.
    end = instant(step["completed_at"]) + dt.timedelta(seconds=1)
    messages = []
    for line in log.splitlines():
        match = re.fullmatch(r"\ufeff?(\d{4}-\d\d-\d\dT\S+Z) (.*)", line)
        if match and start <= instant(match[1]) < end:
            messages.append(match[2])
    # Only the pinned upload action's group for this fixed step is eligible.
    marker = f"##[group]Run {UPLOAD_ACTION}"
    if messages.count(marker) != 1:
        return False
    messages = messages[messages.index(marker) + 1:]
    for index, message in enumerate(messages):
        if message.startswith("##[group]") or message == "Post job cleanup.":
            messages = messages[:index]
            break
    # Config is logged inside the action group. A later/earlier step's ID cannot
    # attest this upload, even when the jobs share a run, SHA or artifact name.
    if f"  name: {artifact_name}" not in messages:
        return False
    ids = []
    for message in messages:
        match = re.fullmatch(r"Artifact (.+) has been successfully uploaded! Final size is [0-9]+ bytes\. Artifact ID is ([1-9][0-9]*)", message)
        if match:
            if match[1] != artifact_name:
                return False
            ids.append(match[2])
    return ids == [artifact_id]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--job", type=Path, required=True)
    parser.add_argument("--log", type=Path, required=True)
    for name in ("run-id", "attempt", "sha", "channel", "artifact-id", "artifact-name", "job-id"):
        parser.add_argument("--" + name, required=True)
    args = parser.parse_args()
    try:
        accepted = verify(json.loads(args.job.read_bytes()), args.log.read_text(encoding="utf-8-sig"),
                          run_id=args.run_id, attempt=args.attempt, sha=args.sha, channel=args.channel,
                          artifact_id=args.artifact_id, artifact_name=args.artifact_name, job_id=args.job_id)
    except (OSError, ValueError, TypeError, KeyError, AttributeError):
        accepted = False
    if not accepted:
        parser.exit(1, "Original signing-job artifact producer could not be verified.\n")
    print("Verified original signing-job upload artifact ID")


if __name__ == "__main__":
    main()
