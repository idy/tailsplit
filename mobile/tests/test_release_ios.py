from contextlib import ExitStack
import copy
import datetime as dt
import importlib.util
import io
import json
from pathlib import Path
import plistlib
import re
import stat
import subprocess
import tempfile
import unittest
from unittest import mock
import zipfile

MODULE = Path(__file__).resolve().parents[1] / "tool" / "release_ios.py"
spec = importlib.util.spec_from_file_location("release_ios", MODULE)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


def profile_for(bundle, group):
    return {
        "UUID": "22222222-2222-2222-2222-222222222222" if bundle.endswith(".PacketTunnel") else "11111111-1111-1111-1111-111111111111",
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


def zip_bytes(files):
    data = io.BytesIO()
    with zipfile.ZipFile(data, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, content in files.items():
            archive.writestr(name, content)
    return data.getvalue()


def bundle_files(root, bundle):
    executable = "PacketTunnel" if root.endswith(".appex") else "Runner"
    return {
        f"{root}/Info.plist": plistlib.dumps({
            "CFBundleIdentifier": bundle, "CFBundleVersion": "38",
            "CFBundleShortVersionString": "1.0.0", "CFBundleExecutable": executable,
        }),
        f"{root}/{executable}": bytes.fromhex("cffaedfe") + b"mock Mach-O",
    }


class ReleaseIdentityTests(unittest.TestCase):
    def setUp(self):
        self.bundle = "dev.idy.tailsplit"
        self.group = "group.dev.idy.tailsplit"
        self.profile = profile_for(self.bundle, self.group)

    def check(self, profile):
        release.validate_profile(profile, self.bundle, self.group)

    def test_pinned_organization_and_channel_identities(self):
        project = MODULE.parent.parent / "ios/Runner.xcodeproj/project.pbxproj"
        self.assertNotIn("SV4P7RNRDK", project.read_text())
        original = json.loads((MODULE.parent.parent / "release-identity.json").read_text())
        for channel in ("beta", "app-store"):
            self.assertEqual(release.identity(channel)["team_id"], "BGL4WMWD3W")
            self.assertIs(release.identity(channel)["app_store"], channel == "app-store")
            for key in ("bundle_id", "packet_tunnel_bundle_id", "app_group", "name", "app_store",
                        "app_store_id", "testflight_group", "testflight_group_id",
                        "team_id", "organization", "account_type"):
                config = copy.deepcopy(original)
                target = config if key in ("team_id", "organization", "account_type") else config["channels"][channel]
                target[key] = "different"
                with self.subTest(channel=channel, key=key), mock.patch.object(Path, "read_text", return_value=json.dumps(config)):
                    with self.assertRaises(ValueError):
                        release.identity(channel)

    def test_channels_have_distinct_bundles_and_storage(self):
        beta, store = release.identity("beta"), release.identity("app-store")
        for key in ("bundle_id", "packet_tunnel_bundle_id", "app_group"):
            self.assertNotEqual(beta[key], store[key])
        with self.assertRaises(ValueError):
            release.identity("unknown")

    def test_beta_cannot_submit_to_app_store_even_with_publish_enabled(self):
        with self.assertRaisesRegex(ValueError, "beta is TestFlight-only"):
            release.release(Path("unused.zip"), Path("unused"), publish=True,
                            channel="beta", submit_for_review=True)

    def test_app_review_requires_upload(self):
        with self.assertRaisesRegex(ValueError, "requires --publish"):
            release.release(Path("unused.zip"), Path("unused"), submit_for_review=True)

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


class ArchiveFixture(unittest.TestCase):
    channel = "app-store"

    def setUp(self):
        folder = tempfile.TemporaryDirectory(prefix=".release-test-", dir=MODULE.parent.parent)
        self.addCleanup(folder.cleanup)
        self.root = Path(folder.name)
        self.config = release.identity(self.channel)
        self.archive_app = "Runner.xcarchive/Products/Applications/Runner.app"
        self.archive_extension = f"{self.archive_app}/PlugIns/PacketTunnel.appex"
        self.archive_files = {
            "Runner.xcarchive/Info.plist": plistlib.dumps({
                "ApplicationProperties": {"ApplicationPath": "/outside.app", "IconPaths": ["../../outside"]},
                "SchemeName": "untrusted", "UnknownHook": "run-me.sh",
            }),
            **bundle_files(self.archive_app, self.config["bundle_id"]),
            **bundle_files(self.archive_extension, self.config["packet_tunnel_bundle_id"]),
        }
        self.artifact = self.root / "artifact.zip"

    def write_artifact(self):
        self.artifact.write_bytes(zip_bytes({"unsigned.xcarchive.zip": zip_bytes(self.archive_files)}))


class ArchiveTests(ArchiveFixture):
    def test_archive_metadata_cannot_redirect_export(self):
        self.write_artifact()
        archive = release.prepare_archive(self.artifact, self.root, self.config, "38")
        info = plistlib.loads((archive / "Info.plist").read_bytes())
        self.assertEqual(info["ApplicationProperties"]["ApplicationPath"], "Applications/Runner.app")
        self.assertEqual(info["SchemeName"], "Runner")
        self.assertNotIn("UnknownHook", info)
        self.assertNotIn("IconPaths", info["ApplicationProperties"])

    def test_outer_artifact_cannot_overwrite_helper_or_runner_files(self):
        self.artifact.write_bytes(zip_bytes({"../release_ios.py": b"malicious", "unsigned.xcarchive.zip": b""}))
        with self.assertRaises(ValueError):
            release.prepare_archive(self.artifact, self.root, self.config, "38")
        self.assertFalse((self.root / "transfer").exists())

    def test_inner_archive_rejects_path_traversal(self):
        self.archive_files["Runner.xcarchive/../../release_ios.py"] = b"malicious"
        self.write_artifact()
        with self.assertRaises(ValueError):
            release.prepare_archive(self.artifact, self.root, self.config, "38")
        self.assertFalse((self.root / "unsigned").exists())

    def test_zip_rejects_aliases_special_files_and_conflicting_paths_before_writing(self):
        variants = [
            [("/Payload/app", 0)], [("Payload/../escape", 0)], [("Payload//app", 0)],
            [("Payload/./app", 0)], [("Payload\\escape", 0)], [("Payload/C:escape", 0)],
            [("Payload/escape\n", 0)], [("Payload/link", stat.S_IFLNK | 0o777)],
            [("Payload/fifo", stat.S_IFIFO | 0o600)], [("Payload/socket", stat.S_IFSOCK | 0o600)],
            [("Payload/file", 0), ("Payload/file/child", 0)],
            [("Payload/File", 0), ("Payload/file", 0)],
            [("Payload/café", 0), ("Payload/café", 0)],
            [("Payload/app/one", 0), ("Payload/APP/two", 0)],
        ]
        for entries in variants:
            with self.subTest(entries=entries):
                with zipfile.ZipFile(self.artifact, "w") as archive:
                    for name, mode in entries:
                        item = zipfile.ZipInfo(name)
                        item.create_system = 3
                        item.external_attr = mode << 16
                        archive.writestr(item, b"untrusted")
                destination = self.root / "rejected"
                with self.assertRaises(ValueError):
                    release.extract_zip(self.artifact, destination, {"Payload"})
                self.assertFalse(destination.exists())

    def test_zip_rejects_duplicate_members(self):
        with zipfile.ZipFile(self.artifact, "w") as archive:
            archive.writestr("Payload/app", b"one")
            with self.assertWarns(UserWarning):
                archive.writestr("Payload/app", b"two")
        with self.assertRaises(ValueError):
            release.extract_zip(self.artifact, self.root / "rejected", {"Payload"})

    def test_zip_cannot_reuse_destination_or_follow_input_link(self):
        self.artifact.write_bytes(zip_bytes({"Payload/file": b"value"}))
        with self.assertRaises(FileExistsError):
            release.extract_zip(self.artifact, self.root, {"Payload"})
        link = self.root / "link.zip"
        link.symlink_to(self.artifact)
        with self.assertRaises(ValueError):
            release.extract_zip(link, self.root / "rejected", {"Payload"})

    def test_zip_enforces_size_limit_and_drops_privileged_modes(self):
        self.artifact.write_bytes(zip_bytes({"Payload/file": b"value"}))
        with mock.patch.object(release, "MAX_ZIP_BYTES", 1), self.assertRaises(ValueError):
            release.extract_zip(self.artifact, self.root / "rejected", {"Payload"})
        with zipfile.ZipFile(self.artifact, "w") as archive:
            item = zipfile.ZipInfo("Payload/binary")
            item.external_attr = (stat.S_IFREG | 0o6777) << 16
            archive.writestr(item, b"inert binary")
        release.extract_zip(self.artifact, self.root / "accepted", {"Payload"})
        self.assertEqual((self.root / "accepted/Payload/binary").stat().st_mode & 0o7777, 0o700)

    def test_unexpected_archive_products_or_build_scripts_fail_closed(self):
        self.archive_files["Runner.xcarchive/project.xcodeproj/project.pbxproj"] = b"malicious"
        self.write_artifact()
        with self.assertRaisesRegex(ValueError, "Unexpected archive contents"):
            release.prepare_archive(self.artifact, self.root, self.config, "38")

    def test_archive_must_match_build_number(self):
        self.write_artifact()
        with self.assertRaisesRegex(ValueError, "build number"):
            release.prepare_archive(self.artifact, self.root, self.config, "39")

    def test_bundle_executable_cannot_reference_an_external_file(self):
        info_path = f"{self.archive_app}/Info.plist"
        info = plistlib.loads(self.archive_files[info_path])
        info["CFBundleExecutable"] = "/usr/bin/security"
        self.archive_files[info_path] = plistlib.dumps(info)
        self.write_artifact()
        with self.assertRaisesRegex(ValueError, "executable path"):
            release.prepare_archive(self.artifact, self.root, self.config, "38")

    def test_framework_executable_cannot_reference_an_external_file(self):
        framework = f"{self.archive_app}/Frameworks/Library.framework"
        self.archive_files[f"{framework}/Info.plist"] = plistlib.dumps({"CFBundleExecutable": "../../outside"})
        self.write_artifact()
        with self.assertRaisesRegex(ValueError, "executable path"):
            release.prepare_archive(self.artifact, self.root, self.config, "38")

    def test_archive_executable_cannot_be_a_script(self):
        self.archive_files[f"{self.archive_app}/Runner"] = b"#!/bin/sh\nexit 99\n"
        self.write_artifact()
        with self.assertRaisesRegex(ValueError, "Mach-O"):
            release.prepare_archive(self.artifact, self.root, self.config, "38")

    def test_archive_rejects_additional_nested_apps(self):
        self.archive_files.update(bundle_files(f"{self.archive_app}/Extra.app", "dev.idy.other"))
        self.write_artifact()
        with self.assertRaisesRegex(ValueError, "Unexpected nested"):
            release.prepare_archive(self.artifact, self.root, self.config, "38")


class ReleaseExecutionTests(ArchiveFixture):
    """Real fixture ZIP/files; every external process is mocked, including Keychain."""

    def setUp(self):
        super().setUp()
        self.archive_files[f"{self.archive_app}/sitecustomize.py"] = b"raise RuntimeError('artifact script executed')"
        self.archive_files[f"{self.archive_app}/post-build.sh"] = b"#!/bin/sh\nexit 99\n"
        self.write_artifact()
        self.output = self.root / "verified"
        self.home = self.root / "home"
        self.app = "Payload/Runner.app"
        self.extension = f"{self.app}/PlugIns/PacketTunnel.appex"
        self.bundles = {self.app: self.config["bundle_id"], self.extension: self.config["packet_tunnel_bundle_id"]}
        self.profiles = {bundle: profile_for(bundle, self.config["app_group"]) for bundle in self.bundles.values()}
        self.signed = {bundle: copy.deepcopy(profile["Entitlements"]) for bundle, profile in self.profiles.items()}
        self.values = {key: f"fixture-{key}" for key in (
            "issuer_id", "api_key_id", "api_private_key", "certificate_private_key")}
        self.sops_payload = json.dumps(self.values).encode()
        self.environment = {
            "PATH": "/untrusted/build/bin", "HOME": str(self.home), "RUNNER_TEMP": str(self.root),
            "GITHUB_RUN_NUMBER": "38", release.AGE_SECRET: "AGE-SECRET-KEY-FIXTURE",
            "SOPS_AGE_KEY": "inherited-age-key", "SOPS_AGE_KEY_FILE": "/untrusted/key.txt",
            "SOPS_AGE_KEY_CMD": "/untrusted/key-command",
            "APP_STORE_CONNECT_PRIVATE_KEY": "inherited-api-key", "CERTIFICATE_PRIVATE_KEY": "inherited-certificate",
            "KEYCHAIN_PASSWORD": "inherited-password", "PYTHONPATH": "/untrusted/build", "DYLD_INSERT_LIBRARIES": "/untrusted/lib",
        }
        self.failures = set()
        self.signer = release.TEAM
        self.export_options = None
        self.archive_signatures = {}
        stack = ExitStack()
        self.addCleanup(stack.close)
        self.commands = stack.enter_context(mock.patch.object(release.subprocess, "run", side_effect=self.command))
        stack.enter_context(mock.patch.object(release.os, "environ", self.environment))
        stack.enter_context(mock.patch.object(Path, "home", return_value=self.home))
        stack.enter_context(mock.patch.object(release.secrets, "token_urlsafe", return_value="fixture-keychain-password"))
        stack.enter_context(mock.patch("builtins.print"))

    def command(self, args, **kwargs):
        self.assertTrue(Path(args[0]).is_absolute())
        args = (Path(args[0]).name, *args[1:])
        if args[:2] in self.failures:
            raise subprocess.CalledProcessError(1, args)
        stdout, stderr = b"", b""
        if args[:2] == ("sops", "--decrypt"):
            self.assertEqual(args[-1], str(release.CREDENTIALS_FILE))
            stdout = self.sops_payload
        elif args[:2] == ("security", "default-keychain"):
            stdout = b'"/fixture/login.keychain-db"\n'
        elif args[:2] == ("security", "cms"):
            stdout = Path(args[-1]).read_bytes()
        elif args[:2] == ("keychain", "initialize"):
            Path(args[args.index("--path") + 1]).touch()
        elif args[:2] == ("keychain", "delete"):
            Path(args[-1]).unlink()
        elif args[:2] == ("app-store-connect", "fetch-signing-files"):
            profiles = Path(args[args.index("--profiles-dir") + 1])
            (profiles / f"{args[2]}.mobileprovision").write_bytes(plistlib.dumps(self.profiles[args[2]]))
        elif args[:2] == ("xcodebuild", "-exportArchive"):
            self.assertEqual(set(args[2::2]), {"-archivePath", "-exportPath", "-exportOptionsPlist"})
            self.assertTrue(Path(args[args.index("-archivePath") + 1]).is_dir())
            self.export_options = plistlib.loads(Path(args[-1]).read_bytes())
            export = Path(args[args.index("-exportPath") + 1])
            export.mkdir()
            files = {}
            for root, bundle in self.bundles.items():
                files.update(bundle_files(root, bundle))
                files[f"{root}/embedded.mobileprovision"] = plistlib.dumps(self.profiles[bundle])
            (export / "TailSplit.ipa").write_bytes(zip_bytes(files))
        elif args[:2] == ("codesign", "--force"):
            self.assertNotIn("--deep", args)
            self.assertIn("--keychain", args)
            target = Path(args[-1])
            if target.suffix in (".app", ".appex"):
                info = plistlib.loads((target / "Info.plist").read_bytes())
                signed = plistlib.loads(Path(args[args.index("--entitlements") + 1]).read_bytes())
                embedded = plistlib.loads((target / "embedded.mobileprovision").read_bytes())
                self.assertEqual(embedded["UUID"], self.profiles[info["CFBundleIdentifier"]]["UUID"])
                release.validate_profile(embedded, info["CFBundleIdentifier"], self.config["app_group"])
                self.archive_signatures[info["CFBundleIdentifier"]] = signed
            else:
                self.assertNotIn("--entitlements", args)
        elif args[:2] == ("python3", "-I"):
            self.assertEqual(args[2:4], ("-B", str(MODULE.parent / "publish_testflight.py")))
            receipt = Path(args[args.index("--receipt") + 1])
            receipt.write_text(json.dumps({"fixture": "verified TestFlight delivery"}))
        elif args[:2] == ("codesign", "-d"):
            info = plistlib.loads((Path(args[-1]) / "Info.plist").read_bytes())
            signed = self.signed[info["CFBundleIdentifier"]]
            stdout = signed if isinstance(signed, bytes) else plistlib.dumps(signed)
            stderr = f"Executable={args[-1]}\nTeamIdentifier={self.signer}\n".encode()
        else:
            # Any Flutter, project, shell, archive-supplied or dependency command fails.
            self.assertIn(args[:2], {("keychain", "add-certificates"), ("python3", "-I"), ("codesign", "--verify")})
        return subprocess.CompletedProcess(args, 0, stdout=stdout, stderr=stderr)

    def execute(self, publish=True, submit_for_review=False):
        release.release(self.artifact, self.output, publish=publish, channel=self.channel,
                        submit_for_review=submit_for_review)

    def command_names(self):
        return [(Path(call.args[0][0]).name, *call.args[0][1:]) for call in self.commands.call_args_list]

    def assert_credential_scopes(self):
        api = {
            "APP_STORE_CONNECT_ISSUER_ID": self.values["issuer_id"],
            "APP_STORE_CONNECT_KEY_IDENTIFIER": self.values["api_key_id"],
            "APP_STORE_CONNECT_PRIVATE_KEY": self.values["api_private_key"],
        }
        allowed = {
            ("sops", "--decrypt"): {"SOPS_AGE_KEY": "AGE-SECRET-KEY-FIXTURE"},
            ("app-store-connect", "fetch-signing-files"): {**api, "CERTIFICATE_PRIVATE_KEY": self.values["certificate_private_key"]},
            ("python3", "-I"): api,
            ("keychain", "initialize"): {"KEYCHAIN_PASSWORD": "fixture-keychain-password"},
            ("keychain", "add-certificates"): {"KEYCHAIN_PASSWORD": "fixture-keychain-password"},
        }
        sensitive = set(api) | {release.AGE_SECRET, "SOPS_AGE_KEY", "SOPS_AGE_KEY_FILE", "SOPS_AGE_KEY_CMD",
                                "CERTIFICATE_PRIVATE_KEY", "KEYCHAIN_PASSWORD"}
        for call, args in zip(self.commands.call_args_list, self.command_names()):
            env = call.kwargs["env"]
            self.assertEqual({key: env[key] for key in sensitive if key in env}, allowed.get(args[:2], {}))
            self.assertNotIn("/untrusted", env["PATH"])
            self.assertNotIn("PYTHONPATH", env)
            self.assertNotIn("DYLD_INSERT_LIBRARIES", env)
            self.assertEqual(call.kwargs["cwd"], str(self.root))
            for value in (*self.values.values(), "fixture-keychain-password"):
                self.assertNotIn(value, " ".join(args))

    def assert_not_uploaded(self):
        commands = self.command_names()
        self.assertFalse(any(args[:2] == ("python3", "-I") for args in commands))
        self.assertEqual(commands[-2][:2], ("security", "default-keychain"))
        self.assertEqual(commands[-1][:2], ("keychain", "delete"))
        self.assertFalse(list(self.home.rglob("*.mobileprovision")))
        self.assert_credential_scopes()

    def test_release_exports_without_building_and_verifies_both_bundles_before_upload(self):
        self.execute()
        self.assert_credential_scopes()
        self.assertEqual(self.export_options, {
            "method": "app-store-connect", "destination": "export", "teamID": release.TEAM,
            "signingStyle": "manual", "signingCertificate": "Apple Distribution",
            "provisioningProfiles": {bundle: profile["UUID"] for bundle, profile in self.profiles.items()},
            "manageAppVersionAndBuildNumber": False,
        })
        commands = self.command_names()
        publish = next(i for i, args in enumerate(commands) if args[:2] == ("python3", "-I"))
        for suffix in ("Runner.app", "PacketTunnel.appex"):
            verify = next(i for i, args in enumerate(commands) if args[:2] == ("codesign", "--verify") and args[-1].endswith(suffix))
            display = next(i for i, args in enumerate(commands) if args[:2] == ("codesign", "-d") and args[-1].endswith(suffix))
            self.assertLess(verify, display)
            self.assertLess(display, publish)
        upload = commands[publish]
        self.assertEqual(upload[upload.index("--ipa") + 1], str(self.output / "TailSplit.ipa"))
        self.assertEqual(upload[upload.index("--channel") + 1], self.channel)
        self.assertEqual(upload[upload.index("--build-number") + 1], "38")
        self.assertEqual(upload[upload.index("--version") + 1], "1.0.0")
        self.assertEqual(upload[upload.index("--receipt") + 1], str(self.output / "testflight.json"))
        self.assertTrue((self.output / "testflight.json").is_file())
        self.assertTrue((self.output / "TailSplit.ipa").is_file())
        self.assertFalse(list(self.home.rglob("*.mobileprovision")))

    def test_archive_has_explicit_app_group_and_vpn_entitlements_before_export(self):
        self.execute(publish=False)
        self.assertEqual(set(self.archive_signatures), set(self.bundles.values()))
        for bundle, entitlements in self.archive_signatures.items():
            release.validate_entitlements(entitlements, bundle, self.config["app_group"], "Archive signing")
            self.assertEqual(entitlements["com.apple.security.application-groups"], [self.config["app_group"]])
        commands = self.command_names()
        signed = [i for i, args in enumerate(commands) if args[:2] == ("codesign", "--force")]
        export = next(i for i, args in enumerate(commands) if args[:2] == ("xcodebuild", "-exportArchive"))
        self.assertEqual(len(signed), 2)
        self.assertTrue(commands[signed[0]][-1].endswith("PacketTunnel.appex"))
        self.assertTrue(commands[signed[1]][-1].endswith("Runner.app"))
        self.assertLess(max(signed), export)

    def test_embedded_code_is_signed_before_the_extension_and_host_without_app_entitlements(self):
        framework = f"{self.archive_extension}/Frameworks/Core.framework"
        self.archive_files.update({
            f"{framework}/Info.plist": plistlib.dumps({"CFBundleExecutable": "Core"}),
            f"{framework}/Core": bytes.fromhex("cffaedfe") + b"fixture framework",
            f"{self.archive_app}/Frameworks/libSupport.dylib": bytes.fromhex("cffaedfe") + b"fixture dylib",
        })
        self.write_artifact()
        self.execute(publish=False)
        signed = [args for args in self.command_names() if args[:2] == ("codesign", "--force")]
        self.assertEqual(len(signed), 4)
        self.assertTrue(any(args[-1].endswith("Core.framework") for args in signed[:2]))
        self.assertTrue(any(args[-1].endswith("libSupport.dylib") for args in signed[:2]))
        for args in signed[:2]:
            self.assertNotIn("--entitlements", args)
            self.assertNotIn("--deep", args)

    def test_reused_archive_keeps_its_original_build_number(self):
        self.environment["GITHUB_RUN_NUMBER"] = "999"
        release.release(self.artifact, self.output, channel=self.channel, build_number="38")
        self.assertTrue((self.output / "TailSplit.ipa").is_file())
        self.assert_not_uploaded()

    def test_wrong_reused_archive_build_number_stops_before_decryption(self):
        with self.assertRaisesRegex(ValueError, "build number"):
            release.release(self.artifact, self.output, channel=self.channel, build_number="39")
        self.commands.assert_not_called()

    def test_publish_defaults_to_false(self):
        release.release(self.artifact, self.output, channel=self.channel)
        self.assert_not_uploaded()
        self.assertTrue((self.output / "TailSplit.ipa").exists())

    def test_testflight_does_not_submit_to_public_review(self):
        self.execute()
        publish = next(args for args in self.command_names() if args[:2] == ("python3", "-I"))
        self.assertNotIn("--app-store", publish)
        self.assertNotIn("--testflight", publish)  # External beta review is also explicit.

    def test_only_store_channel_can_submit_to_public_review(self):
        if self.channel == "beta":
            with self.assertRaisesRegex(ValueError, "beta is TestFlight-only"):
                self.execute(submit_for_review=True)
            self.commands.assert_not_called()
        else:
            self.execute(submit_for_review=True)
            publish = next(args for args in self.command_names() if args[:2] == ("python3", "-I"))
            self.assertIn("--submit-for-review", publish)

    def test_archive_from_other_channel_is_rejected_before_signing(self):
        other = "beta" if self.channel == "app-store" else "app-store"
        with self.assertRaisesRegex(ValueError, "bundle identity"):
            release.release(self.artifact, self.output, channel=other)
        self.commands.assert_not_called()

    def test_invalid_archive_stops_before_credentials_are_used_by_any_tool(self):
        self.artifact.write_bytes(zip_bytes({"../escape": b"bad"}))
        with self.assertRaises(ValueError):
            self.execute()
        self.commands.assert_not_called()

    def test_missing_ci_age_key_stops_before_decryption_or_signing(self):
        del self.environment[release.AGE_SECRET]
        with self.assertRaisesRegex(ValueError, release.AGE_SECRET):
            self.execute()
        self.commands.assert_not_called()
        self.assertFalse(self.output.exists())

    def test_generated_age_key_file_comments_are_supported(self):
        self.environment[release.AGE_SECRET] = "# created: fixture\n# public key: fixture\nAGE-SECRET-KEY-FIXTURE\n"
        self.execute()
        self.assert_credential_scopes()

    def test_failed_decryption_never_opens_keychain_or_exports(self):
        self.failures = {("sops", "--decrypt")}
        with self.assertRaises(subprocess.CalledProcessError):
            self.execute()
        self.assertEqual([args[:2] for args in self.command_names()], [("sops", "--decrypt")])
        self.assertFalse(self.output.exists())

    def test_invalid_decrypted_json_never_reaches_signing_tools(self):
        self.sops_payload = b'{}'
        with self.assertRaisesRegex(ValueError, "Apple credentials require"):
            self.execute()
        self.assertEqual([args[:2] for args in self.command_names()], [("sops", "--decrypt")])
        self.assertFalse(self.output.exists())

    def test_accepts_omitted_debug_entitlement_in_profiles_and_signatures(self):
        for bundle in self.profiles:
            del self.profiles[bundle]["Entitlements"]["get-task-allow"]
            del self.signed[bundle]["get-task-allow"]
        self.execute()
        self.assert_credential_scopes()

    def test_invalid_app_or_extension_entitlements_stop_upload(self):
        for index, bundle in enumerate(self.bundles.values()):
            with self.subTest(bundle=bundle):
                original = copy.deepcopy(self.signed[bundle])
                self.signed[bundle]["com.apple.security.application-groups"] = []
                self.output = self.root / f"verified-{index}"
                self.commands.reset_mock()
                with self.assertRaisesRegex(ValueError, "Signed entitlements"):
                    self.execute()
                self.assert_not_uploaded()
                self.assertFalse(list(self.output.iterdir()))
                self.signed[bundle] = original

    def test_missing_malformed_or_wrong_signed_entitlements_stop_upload(self):
        bundle = self.config["packet_tunnel_bundle_id"]
        original = self.signed[bundle]
        variants = [b"", b"not a plist", b"<?xml version='1.0'?><plist><dict>", []]
        for key, value in (
            ("com.apple.developer.team-identifier", "OTHERTEAM1"),
            ("application-identifier", f"{release.TEAM}.dev.idy.*"),
            ("com.apple.security.application-groups", ["group.dev.idy.other"]),
            ("com.apple.security.application-groups", self.config["app_group"]),
            ("com.apple.developer.networking.networkextension", ["app-proxy-provider"]),
            ("com.apple.developer.networking.networkextension", "packet-tunnel-provider"),
            *(("get-task-allow", value) for value in (True, "false", 0, 1)),
        ):
            variants.append({**original, key: value})
        for key in original:
            if key != "get-task-allow":
                variants.append({k: value for k, value in original.items() if k != key})
        for index, value in enumerate(variants):
            with self.subTest(value=value):
                self.signed[bundle] = value
                self.output = self.root / f"verified-{index}"
                self.commands.reset_mock()
                with self.assertRaises(ValueError):
                    self.execute()
                self.assert_not_uploaded()

    def test_export_or_codesign_failure_stops_upload(self):
        for index, command in enumerate((("xcodebuild", "-exportArchive"), ("codesign", "--verify"), ("codesign", "-d"))):
            with self.subTest(command=command):
                self.failures = {command}
                self.output = self.root / f"verified-{index}"
                self.commands.reset_mock()
                with self.assertRaises(subprocess.CalledProcessError):
                    self.execute()
                self.assert_not_uploaded()

    def test_wrong_signer_stops_upload(self):
        self.signer = "OTHERTEAM1"
        with self.assertRaisesRegex(ValueError, "not signed by Termitoc"):
            self.execute()
        self.assert_not_uploaded()

    def test_profile_uuid_cannot_escape_installation_directory(self):
        self.profiles[self.config["bundle_id"]]["UUID"] = "../../escape"
        with self.assertRaisesRegex(ValueError, "invalid UUID"):
            self.execute()
        self.assert_not_uploaded()


class BetaReleaseExecutionTests(ReleaseExecutionTests):
    channel = "beta"


class WorkflowIsolationTests(unittest.TestCase):
    def test_release_jobs_keep_build_scripts_off_the_signing_runner(self):
        workflow = (MODULE.parents[2] / ".github/workflows/mobile-release.yml").read_text()
        # actionlint validates the YAML; these are security contract guards for its jobs.
        jobs = dict(re.findall(r"^  (\w+):\n(.*?)(?=^  \w+:\n|\Z)", workflow.split("\njobs:\n", 1)[1], re.M | re.S))
        self.assertEqual(set(jobs), {"build", "sign"})
        build, sign = jobs["build"], jobs["sign"]
        for text in jobs.values():
            self.assertIn("runs-on: macos-26", text)
            self.assertIn("github.ref_protected", text)
            self.assertIn("github.event.repository.default_branch", text)
            self.assertIn("persist-credentials: false", text)
        self.assertNotRegex(build, r"secrets\.|\benvironment:|TAILSPLIT_MOBILE_RELEASE_AGE_KEY|codemagic-cli-tools")
        wrapper = (MODULE.parent / "build_ios_release.sh").read_text()
        self.assertIn('bash tool/build_ios_release.sh "$RELEASE_CHANNEL"', build)
        self.assertIn('ruby ios/configure_project.rb "$channel"', wrapper)
        self.assertIn("flutter build ipa --release --no-codesign --no-pub", wrapper)
        self.assertIn('--dart-define="TAILSPLIT_IOS_CHANNEL=$channel"', wrapper)
        self.assertIn("path: ${{ runner.temp }}/unsigned.xcarchive.zip", build)
        self.assertIn("needs: build", sign)
        self.assertIn("environment: mobile-release", sign)
        self.assertIn("ref: ${{ github.sha }}", sign)
        self.assertNotRegex(sign, r"flutter-action|flutter build|flutter pub|dart run|pod install|xcode-project|restore-cache|download-artifact")
        self.assertIn("/mobile/tool/release_ios.py", sign)
        self.assertIn("/mobile/release-identity.json", sign)
        self.assertIn("--artifact", sign)
        self.assertIn(".workflow_run.head_sha == $sha", sign)
        self.assertIn("(.workflow_run.id | tostring) == $run", sign)
        self.assertRegex(workflow, r"destination:\n(?:.*\n)*?        default: testflight")
        self.assertIn("options: [beta, app-store]", workflow)
        self.assertIn("options: [artifact, testflight, app-store]", workflow)
        self.assertIn("beta/artifact|beta/testflight|app-store/artifact|app-store/testflight|app-store/app-store)", build)
        self.assertIn('--channel "$RELEASE_CHANNEL"', sign)
        self.assertIn("--publish --submit-for-review", sign)
        self.assertIn("tailsplit-ios-unsigned-${{ inputs.channel }}-", workflow)
        self.assertIn("tailsplit-ios-${{ inputs.channel }}-termitoc-", workflow)
        self.assertEqual(workflow.count("secrets.TAILSPLIT_MOBILE_RELEASE_AGE_KEY"), 1)
        self.assertNotIn("secrets.BGL4WMWD3W_APPLE_CREDENTIALS", workflow)
        self.assertIn("/mobile/credentials/apple-distribution.json.sops", sign)
        self.assertIn("sops-v3.13.3.darwin.arm64", sign)
        self.assertIn("b97c0d434aab577dc40310e8d22ff9e45eef4c80638ab978daae9b4681c59286", sign)
        self.assertIn("shasum -a 256 --check", sign)


if __name__ == "__main__":
    unittest.main()
