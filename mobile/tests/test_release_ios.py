from contextlib import ExitStack
import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path
import plistlib
import subprocess
import unittest
from unittest import mock

MODULE = Path(__file__).resolve().parents[1] / "tool" / "release_ios.py"
spec = importlib.util.spec_from_file_location("release_ios", MODULE)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


def profile_for(bundle, group):
    return {
        "TeamIdentifier": [release.TEAM],
        "ExpirationDate": dt.datetime.now(dt.timezone.utc).replace(tzinfo=None) + dt.timedelta(days=30),
        "Entitlements": {
            "application-identifier": f"{release.TEAM}.{bundle}",
            "com.apple.developer.team-identifier": release.TEAM,
            "com.apple.security.application-groups": [group],
            "com.apple.developer.networking.networkextension": ["packet-tunnel-provider"],
            "get-task-allow": False,
        },
    }


class ReleaseIdentityTests(unittest.TestCase):
    def setUp(self):
        self.bundle = "dev.idy.tailsplit"
        self.group = "group.dev.idy.tailsplit"
        self.profile = profile_for(self.bundle, self.group)

    def check(self, profile):
        release.validate_profile(profile, self.bundle, self.group)

    def test_pinned_organization(self):
        self.assertEqual(release.identity()["team_id"], "BGL4WMWD3W")
        project = MODULE.parent.parent / "ios/Runner.xcodeproj/project.pbxproj"
        self.assertNotIn("SV4P7RNRDK", project.read_text())

    def test_accepts_matching_app_store_vpn_profile(self):
        self.check(self.profile)

    def test_accepts_omitted_get_task_allow(self):
        del self.profile["Entitlements"]["get-task-allow"]
        self.check(self.profile)

    def test_rejects_malformed_get_task_allow(self):
        for value in (None, "false", 0, 1):
            profile = copy.deepcopy(self.profile)
            profile["Entitlements"]["get-task-allow"] = value
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.check(profile)

    def test_rejects_another_team(self):
        for key in ("TeamIdentifier", "com.apple.developer.team-identifier"):
            profile = copy.deepcopy(self.profile)
            if key == "TeamIdentifier":
                profile[key] = ["OTHERTEAM1"]
            else:
                profile["Entitlements"][key] = "OTHERTEAM1"
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.check(profile)

    def test_rejects_another_bundle_or_wildcard(self):
        for bundle in ("dev.idy.other", "dev.idy.*", "dev.idy.tailsplit.PacketTunnel"):
            profile = copy.deepcopy(self.profile)
            profile["Entitlements"]["application-identifier"] = f"{release.TEAM}.{bundle}"
            with self.subTest(bundle=bundle), self.assertRaises(ValueError):
                self.check(profile)

    def test_rejects_missing_vpn_entitlements(self):
        for key in ("com.apple.security.application-groups", "com.apple.developer.networking.networkextension"):
            profile = copy.deepcopy(self.profile)
            profile["Entitlements"][key] = []
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.check(profile)

    def test_rejects_development_ad_hoc_enterprise_and_expired_profiles(self):
        variants = []
        profile = copy.deepcopy(self.profile)
        profile["Entitlements"]["get-task-allow"] = True
        variants.append(profile)
        for key, value in (("ProvisionedDevices", ["device"]), ("ProvisionsAllDevices", True),
                           ("ExpirationDate", dt.datetime(2020, 1, 1)), ("ExpirationDate", None)):
            profile = copy.deepcopy(self.profile)
            profile[key] = value
            variants.append(profile)
        for index, profile in enumerate(variants):
            with self.subTest(index=index), self.assertRaises(ValueError):
                self.check(profile)

    def test_credentials_require_the_letsolo_schema(self):
        for raw in (None, "not json", "[]", "{}"):
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                release.credentials(raw)
        value = {key: "fixture-not-a-credential" for key in (
            "issuer_id", "api_key_id", "api_private_key", "certificate_private_key")}
        self.assertEqual(release.credentials(json.dumps(value)), value)
        value["team_id"] = "OTHERTEAM1"
        with self.assertRaises(ValueError):
            release.credentials(json.dumps(value))


class ReleaseExecutionTests(unittest.TestCase):
    """Exercise publication with fake credentials, files, profiles and subprocesses."""

    def setUp(self):
        self.config = release.identity()
        self.work = Path("/fixture/signing")
        self.app = self.work / "exported/Payload/Runner.app"
        self.extension = self.app / "PlugIns/PacketTunnel.appex"
        self.bundles = {
            self.app: self.config["bundle_id"],
            self.extension: self.config["packet_tunnel_bundle_id"],
        }
        self.profiles = {path: profile_for(bundle, self.config["app_group"])
                         for path, bundle in self.bundles.items()}
        self.signed = {path: copy.deepcopy(profile["Entitlements"])
                       for path, profile in self.profiles.items()}
        self.profile_files = {self.work / "profiles" / f"{path.stem}.mobileprovision": profile
                              for path, profile in self.profiles.items()}
        self.profile_files.update({path / "embedded.mobileprovision": profile
                                   for path, profile in self.profiles.items()})
        self.files = {path / "Info.plist": plistlib.dumps({"CFBundleIdentifier": bundle})
                      for path, bundle in self.bundles.items()}
        self.globs = {
            (self.work / "profiles", "*.mobileprovision"): [
                path for path in self.profile_files if path.parent == self.work / "profiles"],
            (release.MOBILE / "build/ios/ipa", "*.ipa"): [release.MOBILE / "build/ios/ipa/TailSplit.ipa"],
            (self.work / "exported/Payload", "*.app"): [self.app],
            (self.app / "PlugIns", "*.appex"): [self.extension],
        }
        self.values = {
            "issuer_id": "fixture-issuer-id",
            "api_key_id": "fixture-key-id",
            "api_private_key": "fixture-api-private-key",
            "certificate_private_key": "fixture-certificate-private-key",
        }
        self.inherited_secrets = {
            release.SECRET: json.dumps(self.values),
            "APP_STORE_CONNECT_PRIVATE_KEY": "inherited-api-private-key",
            "CERTIFICATE_PRIVATE_KEY": "inherited-certificate-private-key",
            "KEYCHAIN_PASSWORD": "inherited-keychain-password",
        }
        self.environment = {
            "PATH": "/fixture/bin",
            "HOME": "/fixture/home",
            "RUNNER_TEMP": "/fixture/runner",
            "GITHUB_RUN_NUMBER": "38",
            **self.inherited_secrets,
        }
        self.failures = set()
        self.signer = release.TEAM
        stack = ExitStack()
        self.addCleanup(stack.close)
        # No real process, credential lookup, temporary file or Keychain mutation.
        self.commands = stack.enter_context(mock.patch.object(release.subprocess, "run", side_effect=self.command))
        for patcher in (
            mock.patch.object(release.os, "environ", self.environment),
            mock.patch.object(release.secrets, "token_urlsafe", return_value="fixture-keychain-password"),
            mock.patch.object(release.tempfile, "TemporaryDirectory"),
            mock.patch.object(Path, "mkdir"),
            mock.patch.object(Path, "exists", return_value=True),
            mock.patch.object(Path, "read_bytes", autospec=True, side_effect=lambda path: self.files[path]),
            mock.patch.object(Path, "glob", autospec=True,
                              side_effect=lambda path, pattern: iter(self.globs[(path, pattern)])),
            mock.patch("builtins.print"),
        ):
            stack.enter_context(patcher)
        release.tempfile.TemporaryDirectory.return_value.__enter__.return_value = str(self.work)

    def command(self, args, **kwargs):
        args = tuple(args)
        if args in self.failures:
            raise subprocess.CalledProcessError(1, args)
        stdout, stderr = b"", b""
        if args[:2] == ("security", "default-keychain"):
            stdout = b'"/fixture/login.keychain-db"\n'
        elif args[:2] == ("security", "cms"):
            stdout = plistlib.dumps(self.profile_files[Path(args[-1])])
        elif args[:2] == ("codesign", "-d"):
            signed = self.signed[Path(args[-1])]
            stdout = signed if isinstance(signed, bytes) else plistlib.dumps(signed)
            stderr = f"Executable={args[-1]}\nTeamIdentifier={self.signer}\n".encode()
        else:
            self.assertIn(args[:2], {
                ("keychain", "initialize"), ("keychain", "add-certificates"), ("keychain", "delete"),
                ("app-store-connect", "fetch-signing-files"), ("app-store-connect", "publish"),
                ("xcode-project", "use-profiles"), ("flutter", "build"), ("ditto", "-x"),
                ("codesign", "--verify"),
            })
        return subprocess.CompletedProcess(args, 0, stdout=stdout, stderr=stderr)

    def assert_credential_scopes(self):
        api = {
            "APP_STORE_CONNECT_ISSUER_ID": self.values["issuer_id"],
            "APP_STORE_CONNECT_KEY_IDENTIFIER": self.values["api_key_id"],
            "APP_STORE_CONNECT_PRIVATE_KEY": self.values["api_private_key"],
        }
        keychain = {"KEYCHAIN_PASSWORD": "fixture-keychain-password"}
        allowed = {
            ("app-store-connect", "fetch-signing-files"): {
                **api, "CERTIFICATE_PRIVATE_KEY": self.values["certificate_private_key"]},
            ("app-store-connect", "publish"): api,
            ("keychain", "initialize"): keychain,
            ("keychain", "add-certificates"): keychain,
        }
        sensitive = set(api) | set(self.inherited_secrets)
        for call in self.commands.call_args_list:
            args, env = tuple(call.args[0]), call.kwargs.get("env")
            with self.subTest(command=args):
                self.assertIsNotNone(env, "Subprocess must not inherit the release environment")
                self.assertEqual({key: env[key] for key in sensitive if key in env}, allowed.get(args[:2], {}))
                self.assertEqual(env["PATH"], self.environment["PATH"])
                for value in self.inherited_secrets.values():
                    self.assertNotIn(value, env.values())
                for value in (*self.values.values(), *self.inherited_secrets.values(), "fixture-keychain-password"):
                    self.assertNotIn(value, " ".join(args))

    def assert_not_uploaded(self):
        self.assertFalse(any(tuple(call.args[0][:2]) == ("app-store-connect", "publish")
                             for call in self.commands.call_args_list))
        # Failure still restores the default keychain and removes the temporary one.
        self.assertEqual(tuple(self.commands.call_args_list[-2].args[0][:2]), ("security", "default-keychain"))
        self.assertEqual(tuple(self.commands.call_args_list[-1].args[0][:2]), ("keychain", "delete"))
        self.assert_credential_scopes()

    def test_release_scopes_credentials_and_checks_both_bundles_before_upload(self):
        release.release(publish=True)
        self.assert_credential_scopes()
        commands = [tuple(call.args[0]) for call in self.commands.call_args_list]
        publish = next(i for i, args in enumerate(commands) if args[:2] == ("app-store-connect", "publish"))
        for path in self.bundles:
            verify = ("codesign", "--verify", "--deep", "--strict", str(path))
            display = ("codesign", "-d", "--verbose=4", "--entitlements", "-", "--xml", str(path))
            self.assertLess(commands.index(verify), commands.index(display))
            self.assertLess(commands.index(display), publish)
        self.assertEqual(self.environment[release.SECRET], json.dumps(self.values))

    def test_accepts_omitted_debug_entitlement_in_profiles_and_signatures(self):
        for path in self.bundles:
            del self.profiles[path]["Entitlements"]["get-task-allow"]
            del self.signed[path]["get-task-allow"]
        release.release(publish=True)
        self.assert_credential_scopes()

    def test_valid_profiles_do_not_allow_wrong_signed_entitlements(self):
        variants = [
            ("com.apple.developer.team-identifier", "OTHERTEAM1"),
            ("application-identifier", f"OTHERTEAM1.{self.config['bundle_id']}"),
            ("application-identifier", f"{release.TEAM}.dev.idy.*"),
            ("application-identifier", f"{release.TEAM}.dev.idy.other"),
            ("com.apple.security.application-groups", []),
            ("com.apple.security.application-groups", ["group.dev.idy.other"]),
            ("com.apple.security.application-groups", self.config["app_group"]),
            ("com.apple.developer.networking.networkextension", []),
            ("com.apple.developer.networking.networkextension", ["app-proxy-provider"]),
            ("com.apple.developer.networking.networkextension", "packet-tunnel-provider"),
            *(("get-task-allow", value) for value in (True, "false", 0, 1)),
        ]
        required = ("application-identifier", "com.apple.developer.team-identifier",
                    "com.apple.security.application-groups", "com.apple.developer.networking.networkextension")
        for path, bundle in self.bundles.items():
            valid = copy.deepcopy(self.signed[path])
            release.validate_profile(self.profiles[path], bundle, self.config["app_group"])
            for key, value in [*variants, *((key, None) for key in required)]:
                with self.subTest(bundle=bundle, key=key, value=value):
                    self.signed[path] = {**valid, key: value}
                    if value is None:
                        del self.signed[path][key]
                    with self.assertRaisesRegex(ValueError, "Signed entitlements"):
                        release.verify_bundle(path, bundle, self.config["app_group"])

    def test_invalid_app_or_extension_entitlements_stop_upload(self):
        for path in self.bundles:
            with self.subTest(bundle=path):
                original = copy.deepcopy(self.signed[path])
                self.signed[path]["com.apple.security.application-groups"] = []
                self.commands.reset_mock()
                with self.assertRaisesRegex(ValueError, "Signed entitlements"):
                    release.release(publish=True)
                self.assert_not_uploaded()
                self.signed[path] = original

    def test_missing_or_malformed_signed_entitlements_stop_upload(self):
        for value in (b"", b"not a plist", b"<?xml version='1.0'?><plist><dict>", []):
            with self.subTest(value=value):
                self.signed[self.extension] = value
                self.commands.reset_mock()
                with self.assertRaises(ValueError):
                    release.release(publish=True)
                self.assert_not_uploaded()

    def test_codesign_failure_stops_upload(self):
        for path in self.bundles:
            for options in (("--verify", "--deep", "--strict"),
                            ("-d", "--verbose=4", "--entitlements", "-", "--xml")):
                with self.subTest(bundle=path, options=options):
                    self.failures = {("codesign", *options, str(path))}
                    self.commands.reset_mock()
                    with self.assertRaises(subprocess.CalledProcessError):
                        release.release(publish=True)
                    self.assert_not_uploaded()

    def test_wrong_signer_stops_upload(self):
        self.signer = "OTHERTEAM1"
        with self.assertRaisesRegex(ValueError, "not signed by Termitoc"):
            release.release(publish=True)
        self.assert_not_uploaded()


if __name__ == "__main__":
    unittest.main()
