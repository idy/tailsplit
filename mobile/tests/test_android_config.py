from pathlib import Path
import re
import unittest
import xml.etree.ElementTree as ET


ANDROID = Path(__file__).resolve().parents[1] / "android"
MAIN = ANDROID / "app/src/main"
ANDROID_ATTRIBUTE = "{http://schemas.android.com/apk/res/android}"


class AndroidKotlinConfigTests(unittest.TestCase):
    def test_external_kotlin_plugin_is_applied_before_flutter(self):
        properties = (ANDROID / "gradle.properties").read_text()
        self.assertRegex(properties, r"(?m)^android\.builtInKotlin\s*=\s*false\s*$")
        settings = (ANDROID / "settings.gradle.kts").read_text()
        self.assertRegex(
            settings,
            r'(?m)^\s*id\("org\.jetbrains\.kotlin\.android"\)\s+version\s+"[^"]+"\s+apply false\s*$',
        )

        build = (ANDROID / "app/build.gradle.kts").read_text()
        plugins_block = re.search(r"(?m)^plugins\s*\{([^}]+)\}", build)
        self.assertIsNotNone(plugins_block)
        # Match applied declarations, not comments or `apply false` entries.
        plugins = re.findall(
            r'(?m)^\s*id\("([^"]+)"\)\s*$', plugins_block.group(1)
        )
        for plugin in (
            "com.android.application",
            "org.jetbrains.kotlin.android",
            "dev.flutter.flutter-gradle-plugin",
        ):
            self.assertIn(plugin, plugins)
        self.assertLess(
            plugins.index("com.android.application"),
            plugins.index("org.jetbrains.kotlin.android"),
        )
        self.assertLess(
            plugins.index("org.jetbrains.kotlin.android"),
            plugins.index("dev.flutter.flutter-gradle-plugin"),
        )


class AndroidBackupConfigTests(unittest.TestCase):
    def setUp(self):
        manifest = ET.parse(MAIN / "AndroidManifest.xml").getroot()
        self.application = manifest.find("application")
        self.assertIsNotNone(self.application)

    def extraction_rules(self):
        # Follow the shipping manifest's reference; an unused XML file is not protection.
        reference = self.application.get(ANDROID_ATTRIBUTE + "dataExtractionRules", "")
        self.assertRegex(reference, r"^@xml/[a-z0-9_]+$")
        resource = MAIN / "res/xml" / (reference.removeprefix("@xml/") + ".xml")
        self.assertTrue(resource.is_file(), f"Missing extraction rules: {resource}")
        rules = ET.parse(resource).getroot()
        self.assertEqual(rules.tag, "data-extraction-rules")
        return rules

    def assert_all_data_excluded(self, transport):
        sections = self.extraction_rules().findall(transport)
        self.assertEqual(len(sections), 1)
        section = sections[0]
        self.assertEqual(section.findall("include"), [])
        exclusions = {(item.get("domain"), item.get("path")) for item in section.findall("exclude")}
        # AppPaths uses path_provider's Android filesDir for exit.json, settings.json,
        # tailscale/tailscaled.state and cache.db. Root alone does not cover file,
        # sharedpref or database domains. Keep every backup domain disabled.
        for domain in (
            "root", "file", "database", "sharedpref", "external",
            "device_root", "device_file", "device_database", "device_sharedpref",
        ):
            with self.subTest(transport=transport, domain=domain):
                self.assertIn((domain, "."), exclusions)

    def test_legacy_backup_remains_disabled(self):
        for attribute in ("allowBackup", "fullBackupContent"):
            with self.subTest(attribute=attribute):
                self.assertEqual(self.application.get(ANDROID_ATTRIBUTE + attribute), "false")

    def test_main_manifest_references_extraction_rules(self):
        self.assertEqual(
            self.application.get(ANDROID_ATTRIBUTE + "dataExtractionRules"),
            "@xml/data_extraction_rules",
        )
        self.extraction_rules()

    def test_cloud_backup_excludes_private_app_data(self):
        self.assert_all_data_excluded("cloud-backup")

    def test_device_transfer_excludes_private_app_data(self):
        self.assert_all_data_excluded("device-transfer")


if __name__ == "__main__":
    unittest.main()
