#!/usr/bin/env python3
"""Materialize encrypted release inputs outside the public checkout."""
import os
from pathlib import Path
import tempfile

folder = Path(tempfile.mkdtemp(prefix="tailsplit-signing-", dir=os.environ["RUNNER_TEMP"]))
folder.chmod(0o700)
paths = {}
for secret, field, name in (
    ("TAILSPLIT_DEVELOPER_ID_SOPS", "TAILSPLIT_SIGNING_CREDENTIALS", "developer-id.json.sops"),
    ("TAILSPLIT_NOTARY_SOPS", "TAILSPLIT_NOTARY_CREDENTIALS", "notary.json.sops"),
):
    value = os.environ.get(secret)
    if not value:
        raise ValueError("Missing release secret: " + secret)
    path = folder / name
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        stream.write(value)
    paths[field] = str(path)
with open(os.environ["GITHUB_ENV"], "a") as output:
    for field, path in paths.items():
        output.write(field + "=" + path + "\n")
print("Prepared encrypted signing inputs outside the checkout")
