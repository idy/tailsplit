"""Verify release inventory, provenance, hashes and publication boundaries."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
import zipfile
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location(
    "release", Path(__file__).resolve().parents[2] / "packaging/release.py")
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.folder = Path(self.temporary.name)
        self.identity = dict(schema_version=1, project="tailsplit", version="0.5.1", tag="v0.5.1",
                             commit="a" * 40, repository="idy/tailsplit", dirty=False)

    def inventory(self):
        for goos, arch in release.PLATFORMS:
            extension = "zip" if goos == "windows" else "tar.gz"
            names = {f"tailsplit_0.5.1_{goos}_{arch}.{extension}"}
            if goos == "linux":
                names.add(f"tailsplit_0.5.1_linux_{arch}.deb")
            elif goos == "darwin":
                names.update(f"TailSplit_0.5.1_darwin_{arch}.{suffix}"
                             for suffix in ("zip", "dmg", "receipt.json"))
            assets = {}
            for name in names:
                path = self.folder / name
                path.write_bytes(name.encode())
                assets[name] = dict(sha256=release.digest(path), size=path.stat().st_size)
            fragment = dict(self.identity, os=goos, arch=arch, assets=assets)
            (self.folder / f"release-metadata-{goos}-{arch}.json").write_text(json.dumps(fragment))

    def finalize(self):
        with patch.object(release, "metadata", return_value=self.identity):
            return release.finalize(self.folder, "v0.5.1")

    def test_complete_inventory_and_checksums(self):
        self.inventory()
        manifest = self.finalize()
        self.assertEqual(set(manifest["assets"]), release.expected_assets("tailsplit", "0.5.1"))
        lines = (self.folder / "SHA256SUMS").read_text().splitlines()
        self.assertEqual(len(lines), 15)
        for line in lines:
            checksum, name = line.split("  ")
            self.assertEqual(checksum, hashlib.sha256((self.folder / name).read_bytes()).hexdigest())
        self.assertEqual(json.loads((self.folder / "release-manifest.json").read_text()), manifest)

    def test_missing_platform_refuses_publication(self):
        self.inventory()
        (self.folder / "release-metadata-darwin-arm64.json").unlink()
        with self.assertRaisesRegex(ValueError, "complete"):
            self.finalize()

    def test_source_mismatch_refuses_publication(self):
        self.inventory()
        fragment = self.folder / "release-metadata-linux-amd64.json"
        data = json.loads(fragment.read_text())
        data["commit"] = "b" * 40
        fragment.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, "source"):
            self.finalize()

    def test_tampered_asset_refuses_publication(self):
        self.inventory()
        (self.folder / "tailsplit_0.5.1_linux_arm64.deb").write_bytes(b"changed")
        with self.assertRaisesRegex(ValueError, "hash or size"):
            self.finalize()

    def test_symlink_asset_refuses_publication(self):
        self.inventory()
        path = self.folder / "tailsplit_0.5.1_linux_arm64.deb"
        target = self.folder / "outside"
        path.rename(target)
        path.symlink_to(target)
        with self.assertRaisesRegex(ValueError, "hash or size"):
            self.finalize()

    def test_missing_asset_refuses_publication(self):
        self.inventory()
        fragment = self.folder / "release-metadata-darwin-amd64.json"
        data = json.loads(fragment.read_text())
        del data["assets"]["TailSplit_0.5.1_darwin_amd64.dmg"]
        fragment.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, "inventory"):
            self.finalize()

    def test_duplicate_platform_refuses_publication(self):
        self.inventory()
        source = self.folder / "release-metadata-linux-amd64.json"
        (self.folder / "release-metadata-duplicate.json").write_bytes(source.read_bytes())
        with self.assertRaisesRegex(ValueError, "complete"):
            self.finalize()

    def test_archive_contents_and_reproducibility(self):
        binary, license_file = self.folder / "binary", self.folder / "license"
        binary.write_bytes(b"binary")
        license_file.write_text("license")
        one, two = self.folder / "one.tar.gz", self.folder / "two.tar.gz"
        files = {"tailsplit": binary, "LICENSE": license_file}
        release.archive(one, files, 123)
        release.archive(two, files, 123)
        self.assertEqual(one.read_bytes(), two.read_bytes())
        with tarfile.open(one) as bundle:
            self.assertEqual(bundle.getnames(), ["tailsplit", "LICENSE"])
            self.assertEqual(bundle.getmember("tailsplit").mode, 0o755)
            self.assertEqual(bundle.getmember("LICENSE").mode, 0o644)
            self.assertTrue(all(entry.uid == entry.gid == 0 and entry.mtime == 123 for entry in bundle))
        with self.assertRaisesRegex(ValueError, "replace"):
            release.archive(one, files, 123)

    def test_windows_zip_contents_timestamps_and_reproducibility(self):
        binary, license_file = self.folder / "binary.exe", self.folder / "license"
        binary.write_bytes(b"MZtest")
        license_file.write_text("license")
        one, two = self.folder / "one.zip", self.folder / "two.zip"
        files = {"tailsplit.exe": binary, "LICENSE": license_file}
        release.archive(one, files, 123)
        release.archive(two, files, 123)
        self.assertEqual(one.read_bytes(), two.read_bytes())
        with zipfile.ZipFile(one) as bundle:
            self.assertEqual(bundle.namelist(), ["tailsplit.exe", "LICENSE"])
            self.assertEqual(bundle.read("tailsplit.exe"), b"MZtest")
            self.assertEqual(bundle.getinfo("tailsplit.exe").external_attr >> 16, 0o100755)
            self.assertEqual(bundle.getinfo("LICENSE").external_attr >> 16, 0o100644)
            self.assertTrue(all(info.date_time == (1980, 1, 1, 0, 0, 0) for info in bundle.infolist()))
        with self.assertRaisesRegex(ValueError, "replace"):
            release.archive(one, files, 123)

    def test_missing_windows_platform_refuses_publication(self):
        self.inventory()
        (self.folder / "release-metadata-windows-arm64.json").unlink()
        with self.assertRaisesRegex(ValueError, "complete"):
            self.finalize()

    def test_windows_asset_is_required_in_complete_inventory(self):
        self.inventory()
        fragment = self.folder / "release-metadata-windows-amd64.json"
        data = json.loads(fragment.read_text())
        data["assets"].clear()
        fragment.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, "inventory"):
            self.finalize()

    def test_published_release_is_never_filled(self):
        manifest = dict(self.identity, assets={})
        published = dict(tag_name="v0.5.1", draft=False, body="<!-- tailsplit-release commit=" + "a" * 40 + " -->",
                         assets=[])
        with patch.object(release, "finalize", return_value=manifest), \
                patch.object(release, "api", return_value={"sha": "a" * 40}) as api, \
                patch.object(release, "find_release", return_value=published):
            with self.assertRaisesRegex(ValueError, "cannot be filled"):
                release.publish(self.folder, "v0.5.1")
        self.assertEqual(api.call_count, 1)

    def test_remote_tag_must_match_source(self):
        with patch.object(release, "finalize", return_value=self.identity), \
                patch.object(release, "api", return_value={"sha": "b" * 40}), \
                patch.object(release, "find_release") as find:
            with self.assertRaisesRegex(ValueError, "Remote tag"):
                release.publish(self.folder, "v0.5.1")
        find.assert_not_called()

    def test_metadata_requires_matching_clean_commit(self):
        def git(*args):
            return subprocess.check_output(["git", *args], cwd=self.folder, stderr=subprocess.PIPE, text=True).strip()
        git("init", "-q")
        (self.folder / "go.mod").write_text("module github.com/idy/tailsplit\n")
        (self.folder / "VERSION").write_text("0.5.1\n")
        git("add", "go.mod", "VERSION")
        git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture")
        git("tag", "v0.5.1")
        with patch.object(release, "ROOT", self.folder), patch.dict(os.environ, {"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "idy/tailsplit"}):
            self.assertEqual(release.metadata("v0.5.1")["commit"], git("rev-parse", "HEAD"))
            with self.assertRaisesRegex(ValueError, "tag must match"):
                release.metadata("v0.5.2")
            (self.folder / "new-source").write_text("uncommitted")
            with self.assertRaisesRegex(ValueError, "Commit release inputs"):
                release.metadata("v0.5.1")
            self.assertTrue(release.metadata("v0.5.1", allow_dirty=True)["dirty"])
            git("add", "new-source")
            git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "next commit")
            self.assertEqual(release.metadata()["commit"], git("rev-parse", "HEAD"))
            with self.assertRaisesRegex(ValueError, "selected tag"):
                release.metadata("v0.5.1")


if __name__ == "__main__":
    unittest.main()
