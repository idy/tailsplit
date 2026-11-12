from contextlib import ExitStack, redirect_stderr, redirect_stdout
import copy
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
import urllib.error
import urllib.parse

MODULE = Path(__file__).resolve().parents[1] / "tool" / "publish_testflight.py"
spec = importlib.util.spec_from_file_location("publish_testflight", MODULE)
publish = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publish)

IDENTITIES = {
    "beta": {
        "bundle_id": "dev.idy.tailsplit.beta", "app_store_id": "6833766631", "app_store": False,
        "testflight_group": "Internal Testing", "testflight_group_id": "4be1c696-606b-4ae9-b507-9a385956c6ca",
    },
    "app-store": {
        "bundle_id": "dev.idy.tailsplit", "app_store_id": "6833766298", "app_store": True,
        "testflight_group": "Internal Testing", "testflight_group_id": "ee8bc770-f372-4c74-8700-f7035c844fc4",
    },
}


def group_payload(config):
    return {
        "data": {"type": "betaGroups", "id": config["testflight_group_id"],
                 "attributes": {"name": config["testflight_group"], "isInternalGroup": True,
                                "hasAccessToAllBuilds": False},
                 "relationships": {"app": {"data": {"type": "apps", "id": config["app_store_id"]}}}},
        "included": [{"type": "apps", "id": config["app_store_id"], "attributes": {"bundleId": config["bundle_id"]}}],
    }


def build_payload(config, state="READY_FOR_BETA_TESTING"):
    return {
        "data": [{"type": "builds", "id": "build-fixture-id", "attributes": {
            "version": "38", "processingState": "VALID", "expired": False,
        }, "relationships": {
            "preReleaseVersion": {"data": {"type": "preReleaseVersions", "id": "prerelease-fixture-id"}},
            "betaGroups": {"data": [{"type": "betaGroups", "id": config["testflight_group_id"]}]},
            "buildBetaDetail": {"data": {"type": "buildBetaDetails", "id": "detail-fixture-id"}},
            # The app is filtered by the query but is not expanded by include=.
            "app": {"links": {"related": "https://api.appstoreconnect.apple.com/v1/builds/build-fixture-id/app"}},
        }}],
        "included": [
            {"type": "preReleaseVersions", "id": "prerelease-fixture-id",
             "attributes": {"version": "0.9.7", "platform": "IOS"}},
            {"type": "buildBetaDetails", "id": "detail-fixture-id", "attributes": {"internalBuildState": state}},
            {"type": "betaGroups", "id": config["testflight_group_id"],
             "attributes": {"name": config["testflight_group"], "isInternalGroup": True}},
        ],
        "links": {"next": None},
    }


class IdentityAndAuthenticationTests(unittest.TestCase):
    def test_trusted_identity_file_and_both_exact_channels(self):
        for channel, expected in IDENTITIES.items():
            with self.subTest(channel=channel), mock.patch.object(
                    Path, "read_text", autospec=True, return_value=json.dumps({"channels": IDENTITIES})) as read:
                self.assertEqual(publish.identity(channel), expected)
                read.assert_called_once_with(MODULE.parent.parent / "release-identity.json", encoding="utf-8")

    def test_missing_wrong_and_malformed_channel_metadata_fail_closed(self):
        for channel in IDENTITIES:
            for field in IDENTITIES[channel]:
                for replacement in (None, "different", not IDENTITIES[channel][field]):
                    value = copy.deepcopy(IDENTITIES)
                    value[channel][field] = replacement
                    with self.subTest(channel=channel, field=field, value=replacement), mock.patch.object(
                            Path, "read_text", return_value=json.dumps({"channels": value})):
                        with self.assertRaises(publish.PublicationError):
                            publish.identity(channel)
        for raw in ("not json", "null", "[]", "{}", '{"channels": []}', '{"channels": {"beta": []}}'):
            with self.subTest(raw=raw), mock.patch.object(Path, "read_text", return_value=raw):
                with self.assertRaises(publish.PublicationError):
                    publish.identity("beta")
        with self.assertRaises(publish.PublicationError):
            publish.identity("other")

    def test_module_import_does_not_require_jwt(self):
        with mock.patch.dict(sys.modules, {"jwt": None}):
            imported = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(imported)
        self.assertTrue(callable(imported.publish))

    def test_jwt_uses_in_memory_es256_credentials_and_ten_minute_lifetime(self):
        encoder = mock.Mock(encode=mock.Mock(return_value="fixture-jwt"))
        env = dict(zip(publish.API_ENV, ("fixture-issuer", "fixture-key-id", "fixture-private-key")))
        with mock.patch.dict(sys.modules, {"jwt": encoder}), mock.patch.object(publish.os, "environ", env), \
                mock.patch.object(publish.time, "time", return_value=1000), mock.patch.object(Path, "open") as opened:
            self.assertEqual(publish.authorization(), "fixture-jwt")
        encoder.encode.assert_called_once_with(
            {"iss": "fixture-issuer", "iat": 1000, "exp": 1600, "aud": "appstoreconnect-v1"},
            "fixture-private-key", algorithm="ES256", headers={"kid": "fixture-key-id", "typ": "JWT"})
        opened.assert_not_called()

    def test_jwt_failure_never_repeats_private_key_or_dependency_exception(self):
        env = dict(zip(publish.API_ENV, ("fixture-issuer", "fixture-key-id", "PRIVATE-KEY-FIXTURE")))
        encoder = mock.Mock(encode=mock.Mock(side_effect=ValueError("PRIVATE-KEY-FIXTURE in parser error")))
        for module in (None, encoder):
            with self.subTest(module=module), mock.patch.dict(sys.modules, {"jwt": module}), \
                    mock.patch.object(publish.os, "environ", env):
                with self.assertRaisesRegex(publish.PublicationError, "Cannot create") as error:
                    publish.authorization()
                self.assertNotIn("PRIVATE-KEY-FIXTURE", str(error.exception))
                self.assertTrue(error.exception.__suppress_context__)
        with mock.patch.object(publish.os, "environ", {}), self.assertRaises(publish.PublicationError):
            publish.authorization()


class TestFlightPublishingTests(unittest.TestCase):
    def setUp(self):
        folder = tempfile.TemporaryDirectory(prefix="tailsplit-testflight-test-")
        self.addCleanup(folder.cleanup)
        self.root = Path(folder.name)
        self.runner = self.root / "runner"
        self.runner.mkdir()
        self.artifact = self.root / "artifact-data"
        self.artifact.mkdir()
        self.ipa = self.artifact / "TailSplit.ipa"
        self.ipa.write_bytes(b"verified IPA fixture; signer validates the actual archive")
        self.receipt = self.root / "receipt.json"
        self.config = IDENTITIES["app-store"]
        self.group = group_payload(self.config)
        self.build = build_payload(self.config)
        self.responses = []
        self.environment = {
            "HOME": str(self.root / "home"), "RUNNER_TEMP": str(self.runner), "TMPDIR": str(self.runner),
            "LANG": "en_US.UTF-8", "DEVELOPER_DIR": "/Applications/Xcode.app/Contents/Developer",
            "PATH": "/untrusted/bin", "PYTHONPATH": str(self.artifact), "PYTHONHOME": str(self.artifact),
            "DYLD_INSERT_LIBRARIES": "/untrusted/lib", "HTTP_PROXY": "http://untrusted-proxy", "HTTPS_PROXY": "http://untrusted-proxy",
            "APP_STORE_CONNECT_ISSUER_ID": "fixture-issuer", "APP_STORE_CONNECT_KEY_IDENTIFIER": "fixture-key-id",
            "APP_STORE_CONNECT_PRIVATE_KEY": "fixture-api-private-key", "APP_STORE_CONNECT_DISABLE_JWT_CACHE": "false",
            "TAILSPLIT_MOBILE_RELEASE_AGE_KEY": "fixture-age-key", "SOPS_AGE_KEY": "fixture-age-key",
            "SOPS_AGE_KEY_FILE": "/private/key", "SOPS_AGE_KEY_CMD": "/untrusted/cmd",
            "CERTIFICATE_PRIVATE_KEY": "fixture-certificate-private-key", "KEYCHAIN_PASSWORD": "fixture-keychain-password",
            "OTHER_SECRET": "fixture-unrelated-secret",
        }
        stack = ExitStack()
        self.addCleanup(stack.close)
        stack.enter_context(mock.patch.object(publish.os, "environ", self.environment))
        stack.enter_context(mock.patch.object(publish, "identity", side_effect=lambda channel: IDENTITIES[channel].copy()))
        stack.enter_context(mock.patch.object(publish, "authorization", return_value="fixture-jwt"))
        self.sleep = stack.enter_context(mock.patch.object(publish.time, "sleep"))
        self.commands = stack.enter_context(mock.patch.object(
            publish.subprocess, "run", side_effect=self.command))
        self.opener = mock.Mock()
        self.opener.open.side_effect = self.request
        self.openers = stack.enter_context(mock.patch.object(publish.urllib.request, "build_opener", return_value=self.opener))

    def command(self, args, **kwargs):
        self.assertFalse(self.receipt.exists())
        self.assertEqual(Path(args[0]), Path(sys.executable).parent / "app-store-connect")
        self.assertTrue(Path(args[0]).is_absolute())
        self.assertEqual(args[1:4], ["publish", "--path", str(self.ipa)])
        self.assertIn("--enable-package-validation", args)
        self.assertEqual(args[args.index("--beta-group") + 1], "Internal Testing")
        self.assertNotIn("--testflight", args)
        self.assertNotIn("tester", " ".join(args).lower())
        self.assertNotIn("shell", kwargs)
        self.assertEqual(kwargs["cwd"], str(self.runner))
        self.assertEqual(kwargs["stdin"], subprocess.DEVNULL)
        self.assertEqual(kwargs["stdout"], subprocess.PIPE)
        self.assertEqual(kwargs["stderr"], subprocess.PIPE)
        self.assertTrue(kwargs["check"])
        self.assertEqual(kwargs["timeout"], publish.PUBLISH_TIMEOUT)
        allowed = {key: self.environment[key] for key in (
            "HOME", "RUNNER_TEMP", "TMPDIR", "LANG", "DEVELOPER_DIR", *publish.API_ENV)}
        allowed["PATH"] = f"{Path(sys.executable).parent}:/usr/bin:/bin:/usr/sbin:/sbin"
        allowed["APP_STORE_CONNECT_DISABLE_JWT_CACHE"] = "true"
        self.assertEqual(kwargs["env"], allowed)
        for key in publish.API_ENV:
            self.assertNotIn(self.environment[key], " ".join(args))
        return subprocess.CompletedProcess(args, 0, stdout=b"fixture private CLI output", stderr=b"")

    def request(self, request, **kwargs):
        self.assertFalse(self.receipt.exists())
        self.assertEqual(request.get_method(), "GET")
        self.assertIsNone(request.data)
        self.assertEqual(request.get_header("Authorization"), "Bearer fixture-jwt")
        self.assertEqual(kwargs, {"timeout": publish.HTTP_TIMEOUT})
        parsed = urllib.parse.urlsplit(request.full_url)
        self.assertEqual(parsed.scheme, "https")
        self.assertEqual(parsed.netloc, "api.appstoreconnect.apple.com")
        if parsed.path.startswith("/v1/betaGroups/"):
            self.commands.assert_not_called()
            self.assertEqual(parsed.path, "/v1/betaGroups/" + self.config["testflight_group_id"])
            self.assertEqual(urllib.parse.parse_qs(parsed.query), {"include": ["app"]})
            payload = self.group
        elif parsed.path == "/v1/builds":
            self.commands.assert_called_once()
            self.assertEqual(urllib.parse.parse_qs(parsed.query), {
                "filter[app]": [self.config["app_store_id"]], "filter[version]": ["38"],
                "include": ["preReleaseVersion,betaGroups,buildBetaDetail"], "limit": ["200"],
            })
            payload = self.responses.pop(0) if self.responses else self.build
        else:
            self.fail("Unexpected API action: " + parsed.path)
        if isinstance(payload, Exception):
            raise payload
        return io.BytesIO(payload if isinstance(payload, bytes) else json.dumps(payload).encode())

    def execute(self, channel="app-store", submit_for_review=False, build_number="38", version="0.9.7"):
        return publish.publish(self.ipa, channel, build_number, version, self.receipt, submit_for_review)

    def assert_failure(self, message):
        with self.assertRaisesRegex(publish.PublicationError, message):
            self.execute()
        self.assertFalse(self.receipt.exists())

    def test_both_channels_upload_and_write_exact_verified_receipt(self):
        for channel, config in IDENTITIES.items():
            with self.subTest(channel=channel):
                self.config = config
                self.group, self.build = group_payload(config), build_payload(config)
                self.commands.reset_mock()
                result = self.execute(channel)
                self.assertEqual(json.loads(self.receipt.read_text()), result)
                self.assertEqual(result, {
                    "channel": channel, "app_store_id": config["app_store_id"], "bundle_id": config["bundle_id"],
                    "build_number": "38", "version": "0.9.7", "platform": "IOS", "build_id": "build-fixture-id",
                    "testflight_group_id": config["testflight_group_id"], "testflight_group": "Internal Testing",
                    "processing_state": "VALID", "internal_build_state": "READY_FOR_BETA_TESTING", "submitted_for_review": False,
                })
                self.assertNotIn("fixture-api-private-key", self.receipt.read_text())
                self.assertNotIn("--app-store", self.commands.call_args.args[0])
                self.receipt.unlink()
        self.sleep.assert_not_called()

    def test_in_beta_testing_is_also_ready(self):
        self.build = build_payload(self.config, state="IN_BETA_TESTING")
        self.assertEqual(self.execute()["internal_build_state"], "IN_BETA_TESTING")

    def test_group_app_linkage_is_required_even_if_included_app_is_omitted(self):
        del self.group["included"]
        self.execute()
        self.assertTrue(self.receipt.exists())

    def test_missing_wrong_external_or_malformed_group_blocks_upload(self):
        variants = []
        for field, value in (("id", "different"), ("type", "apps")):
            payload = group_payload(self.config)
            payload["data"][field] = value
            variants.append(payload)
        for field, value in (("name", "Other group"), ("isInternalGroup", False), ("isInternalGroup", "true"), ("isInternalGroup", 1)):
            payload = group_payload(self.config)
            payload["data"]["attributes"][field] = value
            variants.append(payload)
        variants.extend(({"data": None}, {"data": []}, {"data": {}}, {}))
        for index, payload in enumerate(variants):
            with self.subTest(index=index):
                self.group = payload
                self.assert_failure("internal|group")
                self.commands.assert_not_called()

    def test_group_from_other_app_or_bundle_blocks_upload(self):
        variants = []
        for field in ("id", "type"):
            payload = group_payload(self.config)
            payload["data"]["relationships"]["app"]["data"][field] = "other"
            variants.append(payload)
        payload = group_payload(self.config)
        payload["data"]["relationships"] = {}
        variants.append(payload)
        for field in ("id", "bundleId"):
            payload = group_payload(self.config)
            target = payload["included"][0] if field == "id" else payload["included"][0]["attributes"]
            target[field] = "other"
            variants.append(payload)
        for index, payload in enumerate(variants):
            with self.subTest(index=index):
                self.group = payload
                self.assert_failure("different app|app bundle")
                self.commands.assert_not_called()

    def test_cli_silent_success_without_membership_fails_after_bounded_reads(self):
        self.build["data"][0]["relationships"]["betaGroups"]["data"] = []
        self.assert_failure("not assigned")
        self.commands.assert_called_once()
        self.assertEqual(self.opener.open.call_count, publish.READBACK_ATTEMPTS + 1)
        self.assertEqual(self.sleep.call_args_list, [mock.call(publish.READBACK_DELAY)] * (publish.READBACK_ATTEMPTS - 1))

    def test_other_channel_group_membership_does_not_count(self):
        self.build["data"][0]["relationships"]["betaGroups"]["data"][0]["id"] = IDENTITIES["beta"]["testflight_group_id"]
        self.assert_failure("not assigned")

    def test_eventual_consistency_retries_readback_without_reuploading(self):
        unassigned = build_payload(self.config)
        unassigned["data"][0]["relationships"]["betaGroups"]["data"] = []
        self.responses = [{"data": []}, unassigned, self.build]
        self.execute()
        self.commands.assert_called_once()
        self.assertEqual(self.sleep.call_count, 2)
        self.assertTrue(self.receipt.exists())

    def test_missing_wrong_build_number_version_platform_or_prerelease_fails(self):
        variants = [{"data": []}]
        payload = build_payload(self.config)
        payload["data"][0]["attributes"]["version"] = "39"
        variants.append(payload)
        for field, value in (("version", "0.9.8"), ("platform", "MAC_OS")):
            payload = build_payload(self.config)
            payload["included"][0]["attributes"][field] = value
            variants.append(payload)
        payload = build_payload(self.config)
        payload["included"] = payload["included"][1:]
        variants.append(payload)
        payload = build_payload(self.config)
        payload["data"][0]["relationships"]["preReleaseVersion"]["data"]["id"] = "different"
        variants.append(payload)
        for index, payload in enumerate(variants):
            with self.subTest(index=index):
                self.commands.reset_mock()
                self.build = payload
                self.assert_failure("exact iOS build")
                self.commands.assert_called_once()

    def test_readback_selects_exact_version_and_ios_among_other_builds(self):
        for field, value in (("version", "0.9.8"), ("platform", "MAC_OS")):
            other = copy.deepcopy(self.build["data"][0])
            other["id"] = "other-build-" + field
            other["relationships"]["preReleaseVersion"]["data"]["id"] = "other-prerelease-" + field
            prerelease = copy.deepcopy(self.build["included"][0])
            prerelease["id"] = "other-prerelease-" + field
            prerelease["attributes"][field] = value
            self.build["data"].insert(0, other)
            self.build["included"].append(prerelease)
        self.assertEqual(self.execute()["build_id"], "build-fixture-id")

    def test_ambiguous_builds_and_paginated_responses_fail_closed(self):
        other = copy.deepcopy(self.build["data"][0])
        other["id"] = "second-matching-build"
        self.build["data"].append(other)
        self.assert_failure("Multiple builds")
        self.build = build_payload(self.config)
        self.build["links"]["next"] = "https://untrusted.example/steal-token"
        self.commands.reset_mock()
        self.assert_failure("unpaginated")
        self.sleep.assert_not_called()

    def test_wrong_expanded_build_or_prerelease_app_is_rejected(self):
        for resource in ("build", "prerelease"):
            with self.subTest(resource=resource):
                self.build = build_payload(self.config)
                target = self.build["data"][0] if resource == "build" else self.build["included"][0]
                target.setdefault("relationships", {})["app"] = {"data": {"type": "apps", "id": "different"}}
                self.commands.reset_mock()
                self.assert_failure("different app")

    def test_invalid_failed_expired_and_unready_builds_never_write_receipts(self):
        for field, value in (("processingState", "INVALID"), ("processingState", "FAILED"),
                             ("processingState", "PROCESSING"), ("expired", True), ("expired", None), ("expired", 0)):
            with self.subTest(field=field, value=value):
                self.build = build_payload(self.config)
                self.build["data"][0]["attributes"][field] = value
                self.commands.reset_mock()
                self.assert_failure("processing|expired|ready")
        for state in ("PROCESSING", "PROCESSING_EXCEPTION", "EXPIRED", "UNKNOWN", None, []):
            with self.subTest(state=state):
                self.build = build_payload(self.config, state=state)
                self.commands.reset_mock()
                self.assert_failure("processing|expired|ready")

    def test_export_compliance_is_a_clear_failure_without_a_declaration(self):
        for state in ("MISSING_EXPORT_COMPLIANCE", "IN_EXPORT_COMPLIANCE_REVIEW"):
            with self.subTest(state=state):
                self.build = build_payload(self.config, state=state)
                self.commands.reset_mock()
                self.assert_failure("Export compliance blocks")
        self.sleep.assert_not_called()
        self.assertNotIn("--uses-non-exempt-encryption", self.commands.call_args.args[0])

    def test_missing_internal_detail_does_not_assume_ready(self):
        self.build["included"] = [item for item in self.build["included"] if item["type"] != "buildBetaDetails"]
        self.assert_failure("ready")

    def test_upload_error_timeout_or_missing_cli_does_not_read_builds_or_leak_logs(self):
        for error in (subprocess.CalledProcessError(1, ["app-store-connect"], output=b"PRIVATE-KEY-FIXTURE"),
                      subprocess.TimeoutExpired(["app-store-connect"], 5, output=b"PRIVATE-KEY-FIXTURE"),
                      OSError("PRIVATE-KEY-FIXTURE")):
            with self.subTest(error=type(error).__name__):
                self.commands.reset_mock()
                self.opener.open.reset_mock()
                self.commands.side_effect = error
                with self.assertRaisesRegex(publish.PublicationError, "command failed") as stopped:
                    self.execute()
                self.assertNotIn("PRIVATE-KEY-FIXTURE", str(stopped.exception))
                self.assertTrue(stopped.exception.__suppress_context__)
                self.assertFalse(self.receipt.exists())
                self.assertEqual(self.opener.open.call_count, 1)

    def test_only_explicit_app_store_option_submits_public_review(self):
        self.execute(submit_for_review=True)
        command = self.commands.call_args.args[0]
        self.assertEqual(command[-5:], ["--app-store", "--platform", "IOS", "--release-type", "MANUAL"])
        self.assertTrue(json.loads(self.receipt.read_text())["submitted_for_review"])

    def test_beta_public_review_is_rejected_before_network_or_upload(self):
        with self.assertRaisesRegex(publish.PublicationError, "beta is TestFlight-only"):
            self.execute("beta", submit_for_review=True)
        self.commands.assert_not_called()
        self.opener.open.assert_not_called()
        self.assertFalse(self.receipt.exists())

    def test_bad_build_number_or_version_fails_before_network(self):
        for number in ("0", "-1", "01", "1.0", "", "38\n", 38):
            with self.subTest(number=number), self.assertRaises(publish.PublicationError):
                self.execute(build_number=number)
        for version in ("1.0", "v1.2.3", "1.2.3+38", "1.2.3\n", "", None):
            with self.subTest(version=version), self.assertRaises(publish.PublicationError):
                self.execute(version=version)
        self.commands.assert_not_called()
        self.opener.open.assert_not_called()

    def test_receipt_must_be_new_without_overwriting_an_old_success(self):
        self.receipt.write_text("old receipt")
        with self.assertRaisesRegex(publish.PublicationError, "receipt path must be new"):
            self.execute()
        self.assertEqual(self.receipt.read_text(), "old receipt")
        self.commands.assert_not_called()
        self.opener.open.assert_not_called()

    def test_ipa_link_or_missing_runner_temp_blocks_network_and_upload(self):
        original = self.root / "original.ipa"
        self.ipa.rename(original)
        self.ipa.symlink_to(original)
        self.assert_failure("regular IPA")
        self.ipa.unlink()
        original.rename(self.ipa)
        del self.environment["RUNNER_TEMP"]
        self.assert_failure("RUNNER_TEMP")
        self.commands.assert_not_called()
        self.opener.open.assert_not_called()

    def test_missing_api_credential_blocks_network_and_upload(self):
        for key in publish.API_ENV:
            value = self.environment.pop(key)
            with self.subTest(key=key):
                self.assert_failure("Configure")
            self.environment[key] = value
        self.commands.assert_not_called()
        self.opener.open.assert_not_called()

    def test_http_errors_and_malformed_json_never_repeat_response_content(self):
        for status in (401, 403, 404, 429, 500):
            self.group = urllib.error.HTTPError("https://fixture", status, "PRIVATE-KEY-FIXTURE", {}, io.BytesIO(b"PRIVATE-KEY-FIXTURE"))
            with self.subTest(status=status), self.assertRaisesRegex(publish.PublicationError, f"HTTP {status}") as stopped:
                self.execute()
            self.assertNotIn("PRIVATE-KEY-FIXTURE", str(stopped.exception))
        for payload in (b"PRIVATE-KEY-FIXTURE", b"[]", urllib.error.URLError("PRIVATE-KEY-FIXTURE")):
            self.group = payload
            with self.subTest(payload=type(payload).__name__), self.assertRaises(publish.PublicationError) as stopped:
                self.execute()
            self.assertNotIn("PRIVATE-KEY-FIXTURE", str(stopped.exception))
        self.commands.assert_not_called()
        self.assertFalse(self.receipt.exists())

    def test_malformed_builds_fail_with_nonsecret_errors(self):
        for payload in ({"data": None}, {"data": [{}]}, {"data": [], "included": None},
                        {"data": [], "included": [{}]}, {"data": [], "included": [{"type": [], "id": "x"}]}):
            with self.subTest(payload=payload):
                self.build = payload
                self.commands.reset_mock()
                self.assert_failure("invalid|ambiguous")

    def test_api_does_not_inherit_proxy_or_follow_bearer_redirects(self):
        self.execute()
        for call in self.openers.call_args_list:
            self.assertEqual(call.args[0].proxies, {})
            handler = call.args[1]
            self.assertIsInstance(handler, publish.NoRedirect)
            self.assertIsNone(handler.redirect_request(None, None, 302, "", {}, "https://untrusted.example"))

    def test_cli_main_accepts_contract_and_prints_only_nonsecret_success(self):
        args = ["publish_testflight.py", "--ipa", str(self.ipa), "--channel", "app-store", "--build-number", "38",
                "--version", "0.9.7", "--receipt", str(self.receipt)]
        output = io.StringIO()
        with mock.patch.object(sys, "argv", args), redirect_stdout(output):
            publish.main()
        self.assertIn("dev.idy.tailsplit 0.9.7 (38)", output.getvalue())
        self.assertNotIn("private", output.getvalue())

    def test_cli_main_sanitizes_failures(self):
        args = ["publish_testflight.py", "--ipa", str(self.ipa), "--channel", "app-store", "--build-number", "38",
                "--version", "0.9.7", "--receipt", str(self.receipt)]
        self.commands.side_effect = subprocess.CalledProcessError(1, ["app-store-connect"], stderr=b"PRIVATE-KEY-FIXTURE")
        error = io.StringIO()
        with mock.patch.object(sys, "argv", args), redirect_stderr(error), self.assertRaises(SystemExit) as stopped:
            publish.main()
        self.assertEqual(stopped.exception.code, 1)
        self.assertIn("command failed", error.getvalue())
        self.assertNotIn("PRIVATE-KEY-FIXTURE", error.getvalue())
        self.assertNotIn("Traceback", error.getvalue())


if __name__ == "__main__":
    unittest.main()
