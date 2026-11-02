#!/usr/bin/env python3
"""Use operator-supplied SOPS identities in a temporary or retained local keychain."""

import argparse
import base64
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile


def capture(arguments, **kwargs):
    return subprocess.check_output(arguments, stderr=subprocess.PIPE, **kwargs)


def private_key(entry):
    if entry["status"] != "issued-and-validated" or entry["private_key_encoding"] != "base64gzip":
        raise ValueError("signing entry is not ready")
    with gzip.GzipFile(fileobj=io.BytesIO(base64.b64decode(entry["private_key"]))) as stream:
        key = stream.read(16385)
    if len(key) > 16384 or not key.startswith(b"-----BEGIN PRIVATE KEY-----"):
        raise ValueError("invalid signing private key")
    return key


def pkcs12(key, certificate, intermediate, passphrase):
    descriptor, writer = os.pipe()
    try:
        os.write(writer, passphrase.encode() + b"\n")
    finally:
        os.close(writer)
    try:
        return capture([
            "openssl", "pkcs12", "-export", "-inkey", "/dev/stdin",
            "-in", str(certificate), "-certfile", str(intermediate),
            "-keypbe", "PBE-SHA1-3DES", "-certpbe", "PBE-SHA1-3DES", "-macalg", "sha1",
            "-passout", f"fd:{descriptor}",
        ], input=key, pass_fds=(descriptor,))
    finally:
        os.close(descriptor)


def exported_public_key(pkcs12_data, passphrase):
    descriptor, writer = os.pipe()
    try:
        os.write(writer, passphrase.encode() + b"\n")
    finally:
        os.close(writer)
    try:
        key = capture([
            "openssl", "pkcs12", "-legacy", "-nocerts", "-nodes",
            "-passin", f"fd:{descriptor}",
        ], input=pkcs12_data, pass_fds=(descriptor,))
        return capture(["openssl", "pkey", "-pubout", "-outform", "DER"], input=key)
    finally:
        os.close(descriptor)


def check_signing(scratch, environment, team):
    available = capture([
        "security", "find-identity", "-v", "-p", "codesigning", environment["APPLE_KEYCHAIN"],
    ]).decode()
    if environment["APPLE_APPLICATION_IDENTITY"] not in available:
        raise ValueError("Application identity is not usable for code signing: " + available.strip())
    probe = scratch / "signing-probe"
    shutil.copyfile("/usr/bin/true", probe)
    probe.chmod(0o755)
    capture([
        "codesign", "--verbose=4", "--force", "--options", "runtime", "--timestamp",
        "--sign", environment["APPLE_APPLICATION_IDENTITY"],
        "--keychain", environment["APPLE_KEYCHAIN"], str(probe),
    ], timeout=45)
    # codesign writes display information to stderr, even on success.
    result = subprocess.run(["codesign", "-dvv", str(probe)], capture_output=True, text=True, check=True)
    metadata = result.stdout + result.stderr
    if f"TeamIdentifier={team}\n" not in metadata or "Authority=Developer ID Application:" not in metadata:
        raise ValueError("signing probe has the wrong team or certificate")
    capture(["codesign", "--verify", "--strict", str(probe)])
    package = scratch / "signing-probe.pkg"
    capture([
        "pkgbuild", "--nopayload", "--identifier", "im.lbv.tailsplit.signing-probe",
        "--version", "1", "--sign", environment["APPLE_INSTALLER_IDENTITY"],
        "--keychain", environment["APPLE_KEYCHAIN"], "--timestamp", str(package),
    ], timeout=45)
    signature = capture(["pkgutil", "--check-signature", str(package)]).decode()
    if not re.search(r"Developer ID Installer: [^\n]*\(" + re.escape(team) + r"\)", signature):
        raise ValueError("installer probe has the wrong certificate")
    print(f"Both Developer ID identities signed and verified successfully for team {team}.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--credentials", type=Path,
        default=os.environ.get("TAILSPLIT_SIGNING_CREDENTIALS") or None,
        help="SOPS JSON file (or set TAILSPLIT_SIGNING_CREDENTIALS)",
    )
    parser.add_argument(
        "--notary-credentials", type=Path,
        help="Load this SOPS notarization credential before running the release command",
    )
    parser.add_argument("--check", action="store_true", help="Sign temporary probes without building or installing the app")
    parser.add_argument("--install", action="store_true", help="Retain both identities in the default local keychain")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command
    if command[:1] == ["--"]:
        command = command[1:]
    if not command and not args.check and not args.install:
        parser.error("provide a command after --, --check, or --install")
    if args.notary_credentials and (not command or args.install):
        parser.error("--notary-credentials requires a command and a temporary signing keychain")
    if args.credentials is None:
        parser.error("provide --credentials or set TAILSPLIT_SIGNING_CREDENTIALS")
    scripts = Path(__file__).resolve().parent
    team = json.loads((scripts.parent / "release-identity.json").read_text())["team_id"]
    credential_file = args.credentials.expanduser().resolve()
    credential = json.loads(capture(
        ["sops", "-d", "--input-type", "json", "--output-type", "json", str(credential_file)],
        env=dict(os.environ),
    ))
    if credential["team_id"] != team or os.environ.get("APPLE_TEAM_ID", team) != team:
        raise ValueError("signing team does not match TailSplit's release team")
    with tempfile.TemporaryDirectory(prefix="tailsplit-signing-") as temporary:
        scratch = Path(temporary)
        keychain = scratch / "release.keychain-db"
        helper = scratch / "signing-keychain"
        capture([
            "swiftc", "-suppress-warnings",
            str(scripts / "signing-keychain.swift"), "-o", str(helper),
        ])
        intermediate = scratch / "intermediate.pem"
        intermediate.write_bytes(capture(
            ["openssl", "x509", "-inform", "DER", "-outform", "PEM"],
            input=base64.b64decode(credential["intermediate_certificate"]),
        ))
        request = {
            "keychainPath": str(keychain), "keychainPassword": secrets.token_urlsafe(48),
            "intermediateCertificate": credential["intermediate_certificate"],
            "useDefaultKeychain": args.install, "identities": [],
        }
        fingerprints = {}
        public_keys = {}
        for role, label in (("application", "Application"), ("installer", "Installer")):
            entry = credential[role]
            key = private_key(entry)
            der = base64.b64decode(entry["certificate"])
            subject = capture(
                ["openssl", "x509", "-inform", "DER", "-noout", "-subject", "-nameopt", "RFC2253"],
                input=der,
            ).decode()
            if not re.search(r"(?:^|,)OU=" + re.escape(team) + r"(?:,|$)", subject):
                raise ValueError("certificate has the wrong team")
            if not re.search(r"CN=Developer ID " + label + r": [^\n]*\(" + re.escape(team) + r"\)", subject):
                raise ValueError("certificate has the wrong Developer ID type")
            cert_pub = capture(["openssl", "x509", "-inform", "DER", "-pubkey", "-noout"], input=der)
            cert_pub = capture(["openssl", "pkey", "-pubin", "-outform", "DER"], input=cert_pub)
            key_pub = capture(["openssl", "pkey", "-pubout", "-outform", "DER"], input=key)
            if cert_pub != key_pub:
                raise ValueError("certificate and private key do not match")
            certificate = scratch / f"{role}.pem"
            certificate.write_bytes(capture(
                ["openssl", "x509", "-inform", "DER", "-outform", "PEM"], input=der,
            ))
            passphrase = secrets.token_urlsafe(48)
            request["identities"].append({
                "role": role, "passphrase": passphrase,
                "pkcs12": base64.b64encode(pkcs12(key, certificate, intermediate, passphrase)).decode(),
            })
            fingerprints[role] = hashlib.sha1(der).hexdigest().upper()
            public_keys[role] = cert_pub
        try:
            result = json.loads(capture([str(helper)], input=json.dumps(request).encode(), timeout=30))
            imported = result["identities"]
            if {entry["role"]: entry["sha1"] for entry in imported} != fingerprints:
                raise ValueError("imported signing identities do not match validated certificates")
            passphrases = {entry["role"]: entry["passphrase"] for entry in request["identities"]}
            for entry in imported:
                public_key = exported_public_key(
                    base64.b64decode(entry["exportedPKCS12"]), passphrases[entry["role"]],
                )
                if public_key != public_keys[entry["role"]]:
                    raise ValueError("exported identity does not match the saved certificate")
            print(f"Both keychain identities exported and passed private-key round-trip verification for {team}.", flush=True)
            environment = dict(
                os.environ, APPLE_TEAM_ID=team, APPLE_KEYCHAIN=result["keychain"],
                APPLE_APPLICATION_IDENTITY=fingerprints["application"],
                APPLE_INSTALLER_IDENTITY=fingerprints["installer"],
            )
            for name in tuple(environment):
                if name.startswith("SOPS_AGE_"):
                    environment.pop(name)
            if args.check:
                check_signing(scratch, environment, team)
            if args.notary_credentials:
                # Only this known credential loader inherits SOPS age settings.
                # It removes them before executing the actual build command.
                command = [
                    sys.executable, str(scripts / "with-notary-credentials.py"),
                    "--credentials", str(args.notary_credentials.expanduser().resolve()), "--", *command,
                ]
                environment.update({
                    name: value for name, value in os.environ.items()
                    if name.startswith("SOPS_AGE_")
                })
            return subprocess.run(command, env=environment, check=False).returncode if command else 0
        finally:
            if not args.install and keychain.exists():
                capture(["security", "delete-keychain", str(keychain)])


if __name__ == "__main__":
    def terminate(signum, _frame):
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, terminate)
    try:
        raise SystemExit(main())
    except subprocess.CalledProcessError as error:
        # Helper diagnostics contain only operation names and OSStatus values.
        detail = (error.stderr or b"").decode(errors="replace")[-2000:]
        raise SystemExit("Signing credential command failed:\n" + detail)
    except (KeyError, ValueError, OSError, subprocess.TimeoutExpired) as error:
        raise SystemExit(f"Signing credential preparation failed: {error}")
