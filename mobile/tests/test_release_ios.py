import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path
import unittest

MODULE = Path(__file__).resolve().parents[1] / "tool" / "release_ios.py"
spec = importlib.util.spec_from_file_location("release_ios", MODULE)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseIdentityTests(unittest.TestCase):
    def setUp(self):
        self.bundle = "dev.idy.tailsplit"
        self.group = "group.dev.idy.tailsplit"
        self.profile = {
            "TeamIdentifier": [release.TEAM],
            "ExpirationDate": dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=30),
            "Entitlements": {
                "application-identifier": f"{release.TEAM}.{self.bundle}",
                "com.apple.developer.team-identifier": release.TEAM,
                "com.apple.security.application-groups": [self.group],
                "com.apple.developer.networking.networkextension": ["packet-tunnel-provider"],
                "get-task-allow": False,
            },
        }

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


if __name__ == "__main__":
    unittest.main()
