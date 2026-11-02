"""Exercise release credential boundaries with synthetic data and mocked tools."""

import base64
import gzip
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock


SCRIPTS = Path(__file__).resolve().parent
PROJECT = SCRIPTS.parent
TEAM = json.loads((PROJECT / "release-identity.json").read_text())["team_id"]
SOPS_ENV = {
    "SOPS_AGE_KEY": "synthetic-age-identity",
    "SOPS_AGE_KEY_FILE": "/operator/synthetic-age-key.txt",
    "SOPS_AGE_KEY_CMD": "synthetic-age-key-command",
}
PRIVATE_KEY = b"-----BEGIN PRIVATE KEY-----\nsynthetic-release-test\n-----END PRIVATE KEY-----\n"
ENCODED_KEY = base64.b64encode(gzip.compress(PRIVATE_KEY)).decode()


def load_script(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), SCRIPTS / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


notary = load_script("with-notary-credentials")
signing = load_script("with-signing-credentials")


class NotaryCredentialBoundaryTests(unittest.TestCase):
    def test_build_receives_only_private_key_path_and_file_is_removed(self):
        credential = {
            "team_id": TEAM, "key_id": "test-id", "issuer_id": "test-issuer",
            "private_key_encoding": "base64gzip", "private_key": ENCODED_KEY,
        }
        credential_path = "/operator/notary.json.sops"
        for source in ("argument", "environment"):
            with self.subTest(source=source):
                observed = {}
                arguments = ["--credentials", credential_path] if source == "argument" else []

                def decrypt(command, *, env, stderr):
                    self.assertEqual(command, [
                        "sops", "-d", "--input-type", "json", "--output-type", "json", credential_path,
                    ])
                    self.assertEqual({name: env[name] for name in SOPS_ENV}, SOPS_ENV)
                    return json.dumps(credential).encode()

                def release_command(command, *, env, check):
                    self.assertEqual(command, ["release-probe"])
                    self.assertFalse(any(name.startswith("SOPS_AGE_") for name in env))
                    self.assertEqual(env["APPLE_TEAM_ID"], TEAM)
                    self.assertEqual(env["NOTARY_KEY_ID"], "test-id")
                    self.assertEqual(env["NOTARY_ISSUER_ID"], "test-issuer")
                    key_file = Path(env["NOTARY_KEY_FILE"])
                    self.assertEqual(key_file.stat().st_mode & 0o777, 0o600)
                    self.assertEqual(key_file.parent.stat().st_mode & 0o777, 0o700)
                    self.assertEqual(key_file.read_bytes(), PRIVATE_KEY)
                    self.assertNotIn(str(PROJECT), str(key_file))
                    observed["key_file"] = key_file
                    return subprocess.CompletedProcess(command, 17)

                environment = dict(SOPS_ENV, TAILSPLIT_NOTARY_CREDENTIALS=(
                    credential_path if source == "environment" else "/operator/overridden.json.sops"
                ))
                with (
                    mock.patch.object(sys, "argv", ["with-notary-credentials.py", *arguments, "--", "release-probe"]),
                    mock.patch.dict(os.environ, environment, clear=True),
                    mock.patch.object(notary.subprocess, "check_output", side_effect=decrypt),
                    mock.patch.object(notary.subprocess, "run", side_effect=release_command),
                ):
                    self.assertEqual(notary.main(), 17)
                self.assertFalse(observed["key_file"].exists())
                self.assertFalse(observed["key_file"].parent.exists())

    def test_wrong_credential_or_environment_team_is_rejected_before_build(self):
        for credential_team, environment_team in (("WRONG-TEAM", TEAM), (TEAM, "WRONG-TEAM")):
            with self.subTest(credential_team=credential_team, environment_team=environment_team):
                with (
                    mock.patch.object(sys, "argv", [
                        "with-notary-credentials.py", "--credentials", "/operator/notary.json.sops",
                        "--", "release-probe",
                    ]),
                    mock.patch.dict(os.environ, {"APPLE_TEAM_ID": environment_team}, clear=True),
                    mock.patch.object(notary.subprocess, "check_output", return_value=json.dumps({
                        "team_id": credential_team,
                    }).encode()) as decrypt,
                    mock.patch.object(notary.subprocess, "run") as run,
                ):
                    with self.assertRaisesRegex(ValueError, "team"):
                        notary.main()
                self.assertEqual(decrypt.call_count, 1)
                run.assert_not_called()

    def test_missing_credential_path_never_invokes_tools(self):
        with (
            mock.patch.object(sys, "argv", ["with-notary-credentials.py", "--", "release-probe"]),
            mock.patch.dict(os.environ, {}, clear=True),
            mock.patch.object(sys, "stderr", io.StringIO()),
            mock.patch.object(notary.subprocess, "check_output") as decrypt,
            mock.patch.object(notary.subprocess, "run") as run,
        ):
            with self.assertRaises(SystemExit) as error:
                notary.main()
        self.assertEqual(error.exception.code, 2)
        decrypt.assert_not_called()
        run.assert_not_called()


class SigningCredentialBoundaryTests(unittest.TestCase):
    def signing_credential(self):
        credential = {
            "team_id": TEAM,
            "intermediate_certificate": base64.b64encode(b"synthetic-intermediate").decode(),
        }
        for role in ("application", "installer"):
            credential[role] = {
                "status": "issued-and-validated",
                "certificate": base64.b64encode(f"synthetic-{role}-certificate".encode()).decode(),
                "private_key_encoding": "base64gzip", "private_key": ENCODED_KEY,
            }
        return credential

    def exercise_signing(self, source, with_notary=False):
        credential = self.signing_credential()
        credential_path = "/operator/developer-id.json.sops"
        arguments = ["--credentials", credential_path] if source == "argument" else []
        if with_notary:
            arguments.extend(["--notary-credentials", "/operator/notary.json.sops"])
        environment = dict(SOPS_ENV, TAILSPLIT_SIGNING_CREDENTIALS=(
            credential_path if source == "environment" else "/operator/overridden.json.sops"
        ))
        fingerprints = {
            role: hashlib.sha1(base64.b64decode(credential[role]["certificate"])).hexdigest().upper()
            for role in ("application", "installer")
        }
        observed = {}

        def capture(command, **kwargs):
            if command[0] == "sops":
                self.assertEqual(command[-1], credential_path)
                self.assertEqual({name: kwargs["env"][name] for name in SOPS_ENV}, SOPS_ENV)
                return json.dumps(credential).encode()
            if command[0] == "swiftc":
                self.assertEqual(command[2], str(SCRIPTS / "signing-keychain.swift"))
                return b""
            if command[0] == "openssl":
                if "-subject" in command:
                    role = "Application" if kwargs["input"] == b"synthetic-application-certificate" else "Installer"
                    return f"subject=CN=Developer ID {role}: Release Test ({TEAM}),OU={TEAM}\n".encode()
                if command[1] == "pkey" or "-pubkey" in command:
                    return b"synthetic-public-key"
                return b"synthetic-pem-certificate"
            if Path(command[0]).name == "signing-keychain":
                request = json.loads(kwargs["input"])
                self.assertFalse(request["useDefaultKeychain"])
                keychain = Path(request["keychainPath"])
                keychain.touch()
                observed["keychain"] = keychain
                return json.dumps({
                    "keychain": str(keychain),
                    "identities": [
                        {"role": role, "sha1": fingerprint,
                         "exportedPKCS12": base64.b64encode(b"synthetic-pkcs12").decode()}
                        for role, fingerprint in fingerprints.items()
                    ],
                }).encode()
            self.assertEqual(command, ["security", "delete-keychain", str(observed["keychain"])])
            observed["deleted"] = True
            return b""

        def release_command(command, *, env, check):
            self.assertEqual(env["APPLE_TEAM_ID"], TEAM)
            self.assertEqual(env["APPLE_APPLICATION_IDENTITY"], fingerprints["application"])
            self.assertEqual(env["APPLE_INSTALLER_IDENTITY"], fingerprints["installer"])
            self.assertEqual(Path(env["APPLE_KEYCHAIN"]), observed["keychain"])
            if with_notary:
                self.assertEqual(command, [
                    sys.executable, str(SCRIPTS / "with-notary-credentials.py"),
                    "--credentials", "/operator/notary.json.sops", "--", "release-probe",
                ])
                self.assertEqual({name: env[name] for name in SOPS_ENV}, SOPS_ENV)
            else:
                self.assertEqual(command, ["release-probe"])
                self.assertFalse(any(name.startswith("SOPS_AGE_") for name in env))
            return subprocess.CompletedProcess(command, 17)

        with (
            mock.patch.object(sys, "argv", ["with-signing-credentials.py", *arguments, "--", "release-probe"]),
            mock.patch.dict(os.environ, environment, clear=True),
            mock.patch.object(signing, "capture", side_effect=capture),
            mock.patch.object(signing, "pkcs12", return_value=b"synthetic-pkcs12"),
            mock.patch.object(signing, "exported_public_key", return_value=b"synthetic-public-key"),
            mock.patch.object(signing.subprocess, "run", side_effect=release_command),
            mock.patch("builtins.print"),
        ):
            self.assertEqual(signing.main(), 17)
        self.assertTrue(observed["deleted"])
        self.assertFalse(observed["keychain"].exists())

    def test_build_drops_all_age_settings_and_cleans_temporary_keychain(self):
        for source in ("argument", "environment"):
            with self.subTest(source=source):
                self.exercise_signing(source)

    def test_only_the_local_notary_loader_inherits_age_settings(self):
        self.exercise_signing("argument", with_notary=True)

    def test_wrong_credential_or_environment_team_is_rejected_before_keychain_access(self):
        for credential_team, environment_team in (("WRONG-TEAM", TEAM), (TEAM, "WRONG-TEAM")):
            with self.subTest(credential_team=credential_team, environment_team=environment_team):
                with (
                    mock.patch.object(sys, "argv", [
                        "with-signing-credentials.py", "--credentials", "/operator/developer-id.json.sops",
                        "--", "release-probe",
                    ]),
                    mock.patch.dict(os.environ, {"APPLE_TEAM_ID": environment_team}, clear=True),
                    mock.patch.object(signing, "capture", return_value=json.dumps({
                        "team_id": credential_team,
                    }).encode()) as capture,
                    mock.patch.object(signing.subprocess, "run") as run,
                ):
                    with self.assertRaisesRegex(ValueError, "team"):
                        signing.main()
                self.assertEqual(capture.call_count, 1)
                run.assert_not_called()

    def test_missing_credential_path_never_invokes_tools(self):
        with (
            mock.patch.object(sys, "argv", ["with-signing-credentials.py", "--", "release-probe"]),
            mock.patch.dict(os.environ, {}, clear=True),
            mock.patch.object(sys, "stderr", io.StringIO()),
            mock.patch.object(signing, "capture") as capture,
            mock.patch.object(signing.subprocess, "run") as run,
        ):
            with self.assertRaises(SystemExit) as error:
                signing.main()
        self.assertEqual(error.exception.code, 2)
        capture.assert_not_called()
        run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
