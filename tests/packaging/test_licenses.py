"""Test release notice collection without a Go toolchain or network access."""

import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


PROJECT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("tailsplit_licenses", PROJECT / "packaging/licenses.py")
licenses = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(licenses)
GO_LICENSE = PROJECT / "LICENSES/go.txt"
GO_LICENSE_SHA256 = "911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad"


class LicenseCollectionTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.write("THIRD_PARTY_NOTICES.md", "Project attribution\n")
        self.fallback = self.write("LICENSES/go.txt", GO_LICENSE.read_text(encoding="utf-8"))
        self.goroot = self.root / "toolchain/libexec"
        self.installed = self.write("toolchain/libexec/LICENSE", "Installed Go license\n")
        self.output = self.root / "artifacts/notices.txt"
        self.module_dir = self.root / "module-cache/example.org/dependency@v1.2.3"
        self.module_dir.mkdir(parents=True)
        self.module = {"Path": "example.org/dependency", "Version": "v1.2.3", "Dir": str(self.module_dir)}
        self.module_notice("LICENSE", "Dependency license\n")
        self.packages = [
            {"ImportPath": "runtime", "Dir": str(self.goroot / "src/runtime")},
            {"ImportPath": "example.org/project", "Dir": str(self.root),
             "Module": {"Path": "example.org/project", "Main": True}},
            self.package(self.module_dir),
        ]
        self.commands = []
        patches = [
            mock.patch.object(licenses, "ROOT", self.root),
            mock.patch.object(licenses, "command", side_effect=self.command),
            mock.patch.dict(os.environ, {"GO": "/custom toolchain/go", "GOWORK": "on"}, clear=True),
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ]
        for patch in patches:
            patch.start()
            self.addCleanup(patch.stop)

    def write(self, relative, text):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def module_notice(self, relative, text):
        return self.write(self.module_dir.relative_to(self.root) / relative, text)

    def package(self, directory):
        directory.mkdir(parents=True, exist_ok=True)
        return {"ImportPath": "example.org/dependency/" + directory.name,
                "Dir": str(directory), "Module": self.module}

    def command(self, *args, env):
        self.commands.append((args, env))
        if args[1] == "list":
            return " \n" + "\n\n".join(json.dumps(package) for package in self.packages) + "\n"
        self.assertEqual(args[1:], ("env", "GOROOT"))
        return str(self.goroot) + "\n"

    def collect(self, goos="linux", goarch="amd64"):
        licenses.collect(goos, goarch, self.output)
        return self.output.read_text(encoding="utf-8")

    def existing_output(self):
        self.output.parent.mkdir(parents=True)
        self.output.write_text("Previous complete notices\n", encoding="utf-8")

    def test_vendored_go_license_matches_official_release_bytes(self):
        self.assertEqual(hashlib.sha256(GO_LICENSE.read_bytes()).hexdigest(), GO_LICENSE_SHA256)
        self.assertEqual(licenses.GO_LICENSE_SOURCE,
                         "https://raw.githubusercontent.com/golang/go/go1.26.0/LICENSE")

    def test_installed_go_license_is_preferred(self):
        self.fallback.unlink()
        text = self.collect()
        self.assertIn("Installed Go license", text)
        self.assertNotIn("vendored as", text)
        self.assertIn("## example.org/dependency v1.2.3", text)
        self.assertIn("Dependency license", text)

    def test_missing_installed_go_license_uses_offline_fallback(self):
        self.installed.unlink()
        text = self.collect()
        self.assertIn(GO_LICENSE.read_text(encoding="utf-8"), text)
        self.assertIn(licenses.GO_LICENSE_SOURCE, text)
        self.assertIn("vendored as LICENSES/go.txt", text)
        self.assertIn("Dependency license", text)

    def test_missing_both_go_notices_fails_without_overwriting_output(self):
        self.installed.unlink()
        self.fallback.unlink()
        self.existing_output()
        with self.assertRaisesRegex(ValueError, "No Go license notice found") as error:
            self.collect()
        self.assertIn(str(self.installed), str(error.exception))
        self.assertIn(str(self.fallback), str(error.exception))
        self.assertEqual(self.output.read_text(encoding="utf-8"), "Previous complete notices\n")

    def test_empty_go_notices_are_rejected(self):
        self.installed.write_text(" \n", encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "Empty upstream license notice"):
            self.collect()
        self.installed.unlink()
        self.fallback.write_text("", encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "Empty upstream license notice"):
            self.collect()
        self.assertFalse(self.output.exists())

    def test_unreadable_go_notice_is_not_silently_replaced(self):
        with mock.patch.object(licenses, "read_notice", side_effect=PermissionError("denied")) as read:
            with self.assertRaises(PermissionError):
                licenses.go_license(self.goroot)
        read.assert_called_once_with(self.installed)

    def test_missing_module_notices_fail_without_creating_output(self):
        (self.module_dir / "LICENSE").unlink()
        self.module_notice("unlinked/LICENSE", "Unrelated code license\n")
        with self.assertRaisesRegex(ValueError, "No upstream license notice found for example.org/dependency"):
            self.collect()
        self.assertFalse(self.output.exists())

    def test_attribution_and_license_source_files_do_not_replace_a_module_license(self):
        (self.module_dir / "LICENSE").unlink()
        for name in ("AUTHORS", "NOTICE", "PATENTS", "COPYRIGHT", "license_test.go", "licenses.go"):
            self.module_notice(name, "Attribution or source without a license grant\n")
        self.existing_output()
        with self.assertRaisesRegex(ValueError, "No upstream license notice found"):
            self.collect()
        self.assertEqual(self.output.read_text(encoding="utf-8"), "Previous complete notices\n")

    def test_empty_module_notices_fail(self):
        for name in ("LICENSE", "NOTICE"):
            with self.subTest(name=name):
                (self.module_dir / "LICENSE").write_text("Dependency license\n", encoding="utf-8")
                self.module_notice(name, " \n")
                with self.assertRaisesRegex(ValueError, "Empty upstream license notice"):
                    self.collect()
                self.assertFalse(self.output.exists())

    def test_root_notice_names_include_copyright_and_ignore_go_sources(self):
        (self.module_dir / "LICENSE").unlink()
        notices = {
            "LiCeNcE.md": "Module license\n", "COPYING.LESSER": "Additional terms\n",
            "NOTICE.txt": "Notice attribution\n", "AUTHORS.md": "Contributor names\n",
            "PATENTS": "Patent grant\n", "COPYRIGHT": "Copyright Zoë 著作权\n",
        }
        for name, text in notices.items():
            self.module_notice(name, text)
        for name in ("license_test.go", "license.go", "licenses.go", "licensed_metadata.json"):
            self.module_notice(name, "Unrelated Go source or metadata\n")
        self.module_notice("licenses/unlinked.txt", "Unrelated license tooling\n")
        text = self.collect()
        for name, content in notices.items():
            self.assertIn(f"### {name}\n", text)
            self.assertIn(content, text)
        self.assertNotIn("Unrelated", text)

    def test_linked_package_and_ancestor_notices_are_deduplicated_and_sorted(self):
        self.module_notice("vendorcopy/LICENSE", "Ancestor license\n")
        self.module_notice("vendorcopy/NOTICE", "Ancestor notice\n")
        self.module_notice("vendorcopy/linked/COPYRIGHT", "Linked code copyright\n")
        self.module_notice("vendorcopy/unlinked/LICENSE", "Unlinked package license\n")
        self.module_notice("vendorcopy/linked/license_test.go", "License test source\n")
        linked = self.package(self.module_dir / "vendorcopy/linked")
        self.packages.extend([linked, linked, self.package(self.module_dir / "vendorcopy/linked/child")])
        text = self.collect()
        self.assertEqual(text.count("### vendorcopy/LICENSE\n"), 1)
        self.assertEqual(text.count("### vendorcopy/NOTICE\n"), 1)
        self.assertIn("### vendorcopy/linked/COPYRIGHT\n", text)
        self.assertIn("Ancestor license", text)
        self.assertIn("Ancestor notice", text)
        self.assertIn("Linked code copyright", text)
        self.assertNotIn("Unlinked package license", text)
        self.assertNotIn("License test source", text)
        self.packages.reverse()
        self.assertEqual(self.collect(), text)

    def test_replacement_module_uses_replacement_directory_and_identity(self):
        replacement = dict(self.module, Path="example.org/fork", Version="v2.0.0")
        self.packages[-1]["Module"] = {
            "Path": "example.org/original", "Version": "v1.0.0",
            "Dir": str(self.root / "missing-original"), "Replace": replacement,
        }
        text = self.collect()
        self.assertIn("## example.org/fork v2.0.0", text)
        self.assertIn("Dependency license", text)
        self.assertNotIn("## example.org/original", text)
        self.assertNotIn("## example.org/project", text)

    def test_go_command_targets_release_packages_and_platform(self):
        for goos, goarch, cgo in (("linux", "amd64", "0"), ("darwin", "arm64", "1")):
            with self.subTest(goos=goos):
                self.commands.clear()
                self.collect(goos, goarch)
                self.assertEqual(self.commands[0][0], (
                    "/custom toolchain/go", "list", "-mod=readonly", "-deps", "-json",
                    "-tags", "with_gvisor", "./cmd/...",
                ))
                for args, env in self.commands:
                    self.assertEqual(args[0], "/custom toolchain/go")
                    self.assertEqual(env["GOWORK"], "off")
                    self.assertEqual(env["GOOS"], goos)
                    self.assertEqual(env["GOARCH"], goarch)
                    self.assertEqual(env["CGO_ENABLED"], cgo)

    def test_go_list_failure_does_not_write_partial_notices(self):
        self.existing_output()
        with mock.patch.object(licenses, "command", side_effect=subprocess.CalledProcessError(1, "go list")):
            with self.assertRaises(subprocess.CalledProcessError):
                self.collect()
        self.assertEqual(self.output.read_text(encoding="utf-8"), "Previous complete notices\n")


if __name__ == "__main__":
    unittest.main()
