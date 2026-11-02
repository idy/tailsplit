#!/usr/bin/env python3
"""Verify the real app, helper payload, ZIP and mounted DMG without installing."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import subprocess
import tempfile
import xml.etree.ElementTree as ET


def run(*arguments, **kwargs):
    # hdiutil starts a diskimages helper which can inherit stdout after the
    # attach command exits. A pipe would wait for that helper's EOF indefinitely.
    with tempfile.TemporaryFile() as output:
        result = subprocess.run(arguments, stdout=output, stderr=subprocess.STDOUT, **kwargs)
        output.seek(0)
        captured = output.read()
    if result.returncode:
        # These commands only inspect release artifacts, never credentials.
        print(captured.decode(errors="replace"))
        raise subprocess.CalledProcessError(result.returncode, arguments, captured)
    return captured


def sha256(file):
    digest = hashlib.sha256()
    with file.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def tree(directory):
    result = {}
    for file in sorted(directory.rglob("*")):
        relative = str(file.relative_to(directory))
        if file.is_symlink():
            result[relative] = {"symlink": os.readlink(file)}
        elif file.is_file():
            result[relative] = {"sha256": sha256(file), "mode": file.stat().st_mode & 0o777}
    return result


def check_code(file, mode, team, runtime=True):
    run("codesign", "--verify", "--strict", str(file))
    metadata = run("codesign", "-dvv", str(file)).decode()
    if mode == "developer-id":
        require(f"TeamIdentifier={team}\n" in metadata, f"Wrong team: {file.name}")
        require("\nAuthority=Developer ID Application:" in metadata, f"Wrong certificate: {file.name}")
        require("\nTimestamp=" in metadata, f"Missing secure timestamp: {file.name}")
    else:
        require("\nSignature=adhoc\n" in metadata, f"Expected ad-hoc signature: {file.name}")
    if runtime:
        require("runtime" in metadata, f"Missing hardened runtime: {file.name}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("app", type=Path)
    parser.add_argument("--dmg", required=True, type=Path)
    parser.add_argument("--zip", required=True, type=Path)
    parser.add_argument("--signing-mode", required=True, choices=("adhoc", "developer-id"))
    parser.add_argument("--team", required=True)
    parser.add_argument("--notarized", required=True, choices=("0", "1"))
    parser.add_argument("--receipt", required=True, type=Path)
    args = parser.parse_args()
    app = args.app.resolve()
    project = Path(__file__).resolve().parents[1]
    info = plistlib.loads((app / "Contents/Info.plist").read_bytes())
    version = (project / "VERSION").read_text().strip()
    build = (project / "BUILD_NUMBER").read_text().strip()
    require(info["CFBundleIdentifier"] == "im.lbv.tailsplit", "Wrong bundle ID")
    require(info["CFBundleShortVersionString"] == version, "Wrong app version")
    require(info["CFBundleVersion"] == build, "Wrong build number")
    core = app / "Contents/MacOS/tailsplit"
    check_code(core, args.signing_mode, args.team)
    check_code(app, args.signing_mode, args.team)
    run("codesign", "--verify", "--deep", "--strict", str(app))
    notarized = args.notarized == "1"
    with tempfile.TemporaryDirectory(prefix="tailsplit-verify-") as temporary:
        scratch = Path(temporary)
        for index, (filename, identifier) in enumerate((
            ("TailSplit Network Service.pkg", "im.lbv.tailsplit.helper"),
            ("Remove TailSplit Network Service.pkg", "im.lbv.tailsplit.helper.remove"),
        )):
            package = app / "Contents/Resources" / filename
            expanded = scratch / f"pkg-{index}"
            run("pkgutil", "--expand-full", str(package), str(expanded))
            metadata = ET.parse(expanded / "PackageInfo").getroot()
            require(metadata.attrib["identifier"] == identifier, "Wrong package identifier")
            require(metadata.attrib["version"] == version, "Wrong package version")
            if index == 0:
                payload = expanded / "Payload/Library/PrivilegedHelperTools/im.lbv.tailsplit.helper"
                require(sha256(payload) == sha256(core), "App and package helper hashes differ")
                check_code(payload, args.signing_mode, args.team)
            if args.signing_mode == "developer-id":
                signature = run("pkgutil", "--check-signature", str(package)).decode()
                require(
                    re.search(r"Developer ID Installer: [^\n]*\(" + re.escape(args.team) + r"\)", signature),
                    "Installer was not signed by the expected Developer ID team",
                )
            if notarized:
                run("sh", str(project / "scripts/staple-macos.sh"), "--validate", str(package))
                run("spctl", "--assess", "--type", "install", "--verbose=2", str(package))
        zipped = scratch / "zip"
        run("ditto", "-x", "-k", str(args.zip), str(zipped))
        original_tree = tree(app)
        require(tree(zipped / "TailSplit.app") == original_tree, "ZIP app differs from source app")
        run("hdiutil", "verify", str(args.dmg))
        # macOS can reject APFS mounts nested inside an external disk (the
        # workspace and TMPDIR may both live there). Let DiskImages allocate a
        # random mount beneath the normal /Volumes root instead.
        attached = plistlib.loads(run(
            "hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen",
            "-plist", "-mountrandom", "/Volumes", str(args.dmg),
        ))
        entities = attached["system-entities"]
        device = next(entity["dev-entry"] for entity in entities if "dev-entry" in entity)
        try:
            mounts = [Path(entity["mount-point"]) for entity in entities if "mount-point" in entity]
            require(len(mounts) == 1, "Expected exactly one DMG volume")
            mount = mounts[0]
            require(tree(mount / "TailSplit.app") == original_tree, "DMG app differs from source app")
            require(os.readlink(mount / "Applications") == "/Applications", "Missing Applications link")
            require((mount / "Read Me.txt").is_file(), "Missing installation instructions")
        finally:
            run("hdiutil", "detach", device)
        if args.signing_mode == "developer-id":
            check_code(args.dmg, args.signing_mode, args.team, runtime=False)
        if notarized:
            for file in (app, args.dmg):
                run("sh", str(project / "scripts/staple-macos.sh"), "--validate", str(file))
            run("spctl", "--assess", "--type", "execute", "--verbose=2", str(app))
            run("spctl", "--assess", "--type", "open", "--context", "context:primary-signature",
                "--verbose=2", str(args.dmg))
    receipt = {
        "version": version,
        "build": build,
        "signing_mode": args.signing_mode,
        "team_id": args.team if args.signing_mode == "developer-id" else None,
        "notarized": notarized,
        "helper_sha256": sha256(core),
        "artifacts": {
            file.name: {"sha256": sha256(file), "size": file.stat().st_size}
            for file in (args.dmg, args.zip)
        },
        "checks": [
            "bundle version", "hardened runtime", "app signature", "package identity/version",
            "identical app/helper payload", "identical ZIP and DMG app", "DMG integrity",
            "Applications link",
        ],
    }
    if notarized:
        receipt["checks"].extend(["installer signature", "notarization tickets", "Gatekeeper"])
    args.receipt.write_text(json.dumps(receipt, indent=2) + "\n")
    print(json.dumps(receipt, indent=2))


if __name__ == "__main__":
    main()
