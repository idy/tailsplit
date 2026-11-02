#!/usr/bin/env python3
"""Run a release command using an operator-supplied SOPS notarization credential."""

import argparse
import base64
import gzip
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--credentials", type=Path,
        default=os.environ.get("TAILSPLIT_NOTARY_CREDENTIALS") or None,
        help="SOPS JSON file (or set TAILSPLIT_NOTARY_CREDENTIALS)",
    )
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        parser.error("provide a release command after --")
    if args.credentials is None:
        parser.error("provide --credentials or set TAILSPLIT_NOTARY_CREDENTIALS")
    project = Path(__file__).resolve().parent.parent
    expected_team = json.loads((project / "release-identity.json").read_text())["team_id"]
    credential_file = args.credentials.expanduser().resolve()
    credential = json.loads(subprocess.check_output(
        ["sops", "-d", "--input-type", "json", "--output-type", "json", str(credential_file)],
        env=dict(os.environ),
        stderr=subprocess.PIPE,
    ))
    if credential["team_id"] != expected_team:
        raise ValueError("notarization credential does not belong to TailSplit's release team")
    if os.environ.get("APPLE_TEAM_ID", expected_team) != expected_team:
        raise ValueError("APPLE_TEAM_ID does not match TailSplit's release team")
    if credential["private_key_encoding"] != "base64gzip":
        raise ValueError("unsupported private-key encoding")
    private_key = gzip.decompress(base64.b64decode(credential["private_key"]))
    if len(private_key) > 8192 or not private_key.startswith(b"-----BEGIN PRIVATE KEY-----"):
        raise ValueError("invalid Apple API private key")
    # The key is outside the build and DMG trees, with a private parent and file.
    # Only the child release command receives its path. Never print key material.
    with tempfile.TemporaryDirectory(prefix="tailsplit-notary-") as temporary:
        key_file = Path(temporary) / "AuthKey.p8"
        descriptor = os.open(key_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(private_key)
        environment = dict(
            os.environ,
            APPLE_TEAM_ID=expected_team,
            NOTARY_KEY_FILE=str(key_file),
            NOTARY_KEY_ID=credential["key_id"],
            NOTARY_ISSUER_ID=credential["issuer_id"],
        )
        for name in tuple(environment):
            if name.startswith("SOPS_AGE_"):
                environment.pop(name)
        return subprocess.run(command, env=environment, check=False).returncode


if __name__ == "__main__":
    # Unwind the private-key context on a normal termination signal as well.
    def terminate(signum, _frame):
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, terminate)
    try:
        raise SystemExit(main())
    except (KeyError, ValueError, OSError, subprocess.SubprocessError):
        raise SystemExit("Apple notarization credential loading failed; secrets were not printed.")
