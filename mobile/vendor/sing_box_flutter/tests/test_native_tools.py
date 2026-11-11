"""Focused tests for native input integrity; no SDK, network or Go is required."""
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import urllib.error
import zipfile

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tool"))
from native_common import (ARTIFACTS, BuildError, ROOT, REQUIRED_TAGS, archive_name, artifact_hashes,
                           build_directory, download, load_pins, parse_manifest, pin_identity, safe_extract,
                           sha256, validate_metadata)
from build_libbox import (CORE_REPO, GOMOBILE_MODULE, UPSTREAM_REQUIRED_TAGS, bootstrap_go,
                          go_environment, mobile_profile_bytes, resolved_tailscale_dependency,
                          source_checkout, verify_dependency_toolchain)
from source_inputs import ADAPTER_INPUTS, adapter_digest, source_digest
import install_libbox


class ReleaseTagTests(unittest.TestCase):
    def test_installer_accepts_and_encodes_build_metadata(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            code = install_libbox.main(["--target", "all", "--release", "v0.2.1+build-prod", "--dry-run"])
        self.assertEqual(code, 0)
        plan = json.loads(output.getvalue())
        self.assertIn("/v0.2.1%2Bbuild-prod/SHA256SUMS", plan["manifest"])
        self.assertTrue(all("/v0.2.1%2Bbuild-prod/" in url for url in plan["archives"]))

    def test_release_paths_and_oversized_tags_are_rejected(self):
        for tag in ("latest", "../v0.2.1", "v0.2.1%2Fother", "v0.2.1+" + "x" * 128):
            with self.subTest(tag=tag), contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(install_libbox.main(["--target", "android", "--release", tag, "--dry-run"]), 1)


class SourceInputTests(unittest.TestCase):
    CONTENTS = {
        "endpoint.go": b"package wgc\n",
        "endpoint_test.go": b"package wgc\n// endpoint tests\n",
        "rebind.go": b"package wgc\n// rebind\n",
        "rebind_test.go": b"package wgc\n// rebind tests\n",
        "transport.go": b"package wgc\n// transport\n",
        "go.mod": b"module example.com/wgc\n",
        "go.sum": b"example.com/dependency v1.0.0 h1:fixture\n",
    }

    def adapter_fixture(self, root, *, reverse=False):
        root.mkdir(parents=True)
        for name in sorted(self.CONTENTS, reverse=reverse):
            (root / name).write_bytes(self.CONTENTS[name])
        return root

    def test_canonical_digest_preserves_framing_and_creation_order(self):
        self.assertEqual(ADAPTER_INPUTS, set(self.CONTENTS))
        expected = "9be2dd5774320f3515b49ebaf02cd83c312d74aca57bfbeea62c874f0c9b3d67"
        with tempfile.TemporaryDirectory() as temp:
            for reverse in (False, True):
                root = self.adapter_fixture(Path(temp) / str(reverse), reverse=reverse)
                with self.subTest(reverse=reverse):
                    self.assertEqual(adapter_digest(root), expected)
                    self.assertEqual(source_digest(root), expected)

    def test_vcs_metadata_does_not_change_published_adapter_identity(self):
        with tempfile.TemporaryDirectory() as temp:
            root = self.adapter_fixture(Path(temp) / "adapter")
            before = adapter_digest(root)
            (root / ".git").write_text("gitdir: /checkout/.git/worktrees/adapter\n")
            for name in (".hg", ".svn"):
                (root / name).mkdir()
                (root / name / "metadata").write_bytes(b"checkout-only data")
            self.assertEqual(adapter_digest(root), before)

    def test_pub_ignored_debug_log_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = self.adapter_fixture(Path(temp) / "adapter")
            before = adapter_digest(root)
            (root / "debug.log").write_bytes(b"checkout-only debug output\n")
            self.assertNotEqual(source_digest(root), before)
            with self.assertRaisesRegex(BuildError, r"unexpected: debug\.log"):
                adapter_digest(root)

    def test_missing_canonical_inputs_are_named(self):
        with tempfile.TemporaryDirectory() as temp:
            root = self.adapter_fixture(Path(temp) / "adapter")
            for name, content in self.CONTENTS.items():
                with self.subTest(name=name):
                    (root / name).unlink()
                    with self.assertRaisesRegex(BuildError, "missing: " + name.replace(".", r"\.")):
                        adapter_digest(root)
                    (root / name).write_bytes(content)

    def test_each_canonical_input_contributes_to_identity(self):
        with tempfile.TemporaryDirectory() as temp:
            root = self.adapter_fixture(Path(temp) / "adapter")
            before = adapter_digest(root)
            for name, content in self.CONTENTS.items():
                with self.subTest(name=name):
                    (root / name).write_bytes(content + b"// changed\n")
                    self.assertNotEqual(adapter_digest(root), before)
                    (root / name).write_bytes(content)

    def test_unexpected_source_files_and_directories_are_rejected(self):
        for name in ("extra.go", "assets"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temp:
                root = self.adapter_fixture(Path(temp) / "adapter")
                extra = root / name
                if name == "assets":
                    extra.mkdir()
                    (extra / "payload.bin").write_bytes(b"\0\xff")
                else:
                    extra.write_text("package wgc\n")
                with self.assertRaisesRegex(BuildError, "unexpected: " + name.replace(".", r"\.")):
                    adapter_digest(root)

    def test_canonical_paths_must_be_regular_files_without_symlinks(self):
        for kind in ("directory", "symlink", "dangling-symlink"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temp:
                root = self.adapter_fixture(Path(temp) / "adapter")
                path = root / "endpoint.go"
                path.unlink()
                if kind == "directory":
                    path.mkdir()
                else:
                    target = Path(temp) / "outside.go"
                    if kind == "symlink":
                        target.write_bytes(self.CONTENTS["endpoint.go"])
                    path.symlink_to(target)
                with self.assertRaisesRegex(BuildError, r"regular file.*endpoint\.go"):
                    adapter_digest(root)

    def test_missing_and_symlinked_adapter_roots_are_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            with self.assertRaisesRegex(BuildError, "directory is missing"):
                adapter_digest(root / "missing")
            actual = self.adapter_fixture(root / "actual")
            (root / "link").symlink_to(actual, target_is_directory=True)
            with self.assertRaisesRegex(BuildError, "directory must not be a symlink"):
                adapter_digest(root / "link")

    def test_pin_identity_checks_adapter_inputs_before_acceptance(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            adapter = self.adapter_fixture(root / "native/wgc")
            (root / "tool").mkdir()
            (root / "tool/wgc_overlay.py").write_bytes(b"overlay fixture\n")
            pins = load_pins()
            with mock.patch("native_common.ROOT", root):
                before = pin_identity(pins)
                self.assertEqual(before["wgc_source_sha256"], adapter_digest(adapter))
                (adapter / "debug.log").write_bytes(b"debug output\n")
                with self.assertRaisesRegex(BuildError, r"unexpected: debug\.log"):
                    pin_identity(pins)
                (adapter / "debug.log").unlink()
                (adapter / "endpoint.go").unlink()
                with self.assertRaisesRegex(BuildError, r"missing: endpoint\.go"):
                    pin_identity(pins)
                (adapter / "endpoint.go").write_bytes(b"package wgc\n// changed\n")
                after = pin_identity(pins)
                self.assertEqual(after["wgc_source_sha256"], adapter_digest(adapter))
                self.assertNotEqual(after["wgc_source_sha256"], before["wgc_source_sha256"])


class SourceCheckoutTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name)
        self.source = self.base / "sing-box"
        self.source.mkdir()
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        self.env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                        GIT_TERMINAL_PROMPT="0")
        self.git("init", "--quiet", "--template=")
        self.git("config", "user.name", "Native fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.git("config", "commit.gpgSign", "false")
        self.git("remote", "add", "origin", CORE_REPO)
        self.pins = load_pins()
        files = {"go.mod": f"module fixture\nrequire (\n{GOMOBILE_MODULE} {self.pins['gomobile_version']}\n)\n",
                 "LICENSE": "fixture license\n", "source.go": "package fixture\n",
                 ".gitignore": "vendor/\n*.ignored\n", ".gitattributes": "source.go filter=fixture\n",
                 "cmd/internal/build_libbox/main.go": "\n".join(f'"{tag}"' for tag in UPSTREAM_REQUIRED_TAGS)
                     + '\n"-tags-not-macos=with_low_memory"\n'}
        for name, content in files.items():
            path = self.source / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        self.git("add", ".")
        self.git("commit", "--quiet", "-m", "pinned fixture")
        self.pins["sing_box_revision"] = self.git("rev-parse", "HEAD")
        self.git("tag", "v" + self.pins["sing_box_version"])
        self.marker = self.base / "executed"

    def git(self, *args):
        return subprocess.check_output(["git", "--no-pager", "-C", str(self.source), *args],
                                       env=self.env, stderr=subprocess.PIPE).decode().strip()

    def verify(self):
        with contextlib.redirect_stdout(io.StringIO()):
            return source_checkout(self.pins, self.base)

    def payload(self, output=""):
        executable = self.base / "payload"
        executable.write_text(f"#!{sys.executable}\nfrom pathlib import Path\n"
                              f"Path({str(self.marker)!r}).touch()\nprint({output!r}, end='')\n")
        executable.chmod(0o755)
        return str(executable)

    def test_uninitialized_upstream_client_submodules_are_allowed(self):
        self.git("update-index", "--add", "--cacheinfo",
                 "160000," + self.pins["sing_box_revision"] + ",clients/apple")
        self.git("commit", "--quiet", "-m", "client submodule")
        self.pins["sing_box_revision"] = self.git("rev-parse", "HEAD")
        self.assertEqual(self.verify(), self.source)
        client = self.source / "clients/apple"
        client.mkdir(parents=True)
        self.assertEqual(self.verify(), self.source)
        (client / "unverified.go").write_text("package injected\n")
        with self.assertRaisesRegex(BuildError, "uninitialized"):
            self.verify()
        (client / "unverified.go").unlink()
        client.rmdir()
        client.symlink_to(self.base, target_is_directory=True)
        with self.assertRaises(BuildError):
            self.verify()

    def test_pristine_and_packed_caches_remain_usable(self):
        for packed in (False, True):
            with self.subTest(packed=packed):
                if packed:
                    self.git("repack", "-ad")
                (self.source / "libbox.aar").write_bytes(b"previous output")
                framework = self.source / "Libbox.xcframework"
                framework.mkdir(exist_ok=True)
                (framework / "Info.plist").write_text("previous framework")
                self.assertEqual(self.verify(), self.source)
                self.assertEqual(self.git("describe", "--tags"), "v" + self.pins["sing_box_version"])
                self.assertEqual(self.git("rev-parse", "HEAD"), self.pins["sing_box_revision"])
                self.assertEqual(self.git("remote", "get-url", "origin"), CORE_REPO)
                self.assertEqual(self.git("status", "--porcelain", "--untracked-files=no"), "")

    def test_fresh_checkout_fetches_only_the_verified_fixture_tag(self):
        bootstrap = self.base / "fresh"
        bootstrap.mkdir()
        with mock.patch("build_libbox.CORE_REPO", str(self.source)), contextlib.redirect_stdout(io.StringIO()):
            result = source_checkout(self.pins, bootstrap)
        self.assertEqual((result / "source.go").read_bytes(), (self.source / "source.go").read_bytes())
        self.assertTrue((result / ".git").is_dir())

    def test_wrong_tag_commit_is_rejected_before_checkout(self):
        bootstrap = self.base / "wrong-tag"
        bootstrap.mkdir()
        pins = {**self.pins, "sing_box_revision": "1" * 40}
        with mock.patch("build_libbox.CORE_REPO", str(self.source)), contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(BuildError, "release tag"):
                source_checkout(pins, bootstrap)
        self.assertFalse((bootstrap / "sing-box/source.go").exists())

    def test_executable_local_configuration_is_never_read_and_is_replaced(self):
        payload = self.payload()
        included = self.base / "included-config"
        included.write_text(f"[core]\n\tfsmonitor = {payload}\n\tpager = {payload}\n")
        self.git("config", "include.path", str(included))
        self.git("config", "core.hooksPath", str(self.base))
        # Demonstrate that the old status check reaches this fixture executable.
        self.git("status", "--porcelain", "--untracked-files=no")
        self.assertTrue(self.marker.exists())
        self.marker.unlink()
        self.verify()
        self.assertFalse(self.marker.exists())
        self.assertEqual(self.git("config", "core.fsmonitor"), "false")
        self.assertEqual(self.git("config", "core.hooksPath"), os.devnull)
        self.git("status", "--porcelain", "--untracked-files=no")
        self.assertFalse(self.marker.exists())

    def test_cached_monitor_cannot_run_before_a_missing_pin_is_rejected(self):
        self.git("config", "core.fsmonitor", self.payload())
        self.pins["sing_box_revision"] = "1" * 40
        with self.assertRaises(subprocess.CalledProcessError):
            self.verify()
        self.assertFalse(self.marker.exists())

    def test_assume_unchanged_and_skip_worktree_cannot_hide_modified_contents(self):
        path = self.source / "source.go"
        original = path.read_bytes()
        for flag in ("assume-unchanged", "skip-worktree"):
            with self.subTest(flag=flag):
                self.git("update-index", "--" + flag, "source.go")
                path.write_bytes(b"package changed\n")  # Same length as the fixture.
                self.assertEqual(self.git("status", "--porcelain", "--untracked-files=no"), "")
                with self.assertRaisesRegex(BuildError, "tracked modifications"):
                    self.verify()
                path.write_bytes(original)
                self.git("update-index", "--no-" + flag, "source.go")

    def test_clean_filter_cannot_conceal_modified_bytes_or_execute(self):
        path = self.source / "source.go"
        original = path.read_text()
        self.git("config", "filter.fixture.clean", self.payload(original))
        path.write_text("package changed\n")
        self.assertEqual(self.git("status", "--porcelain", "--untracked-files=no"), "")
        self.assertTrue(self.marker.exists())
        self.marker.unlink()
        with self.assertRaisesRegex(BuildError, "tracked modifications"):
            self.verify()
        self.assertFalse(self.marker.exists())

    def test_untracked_ignored_source_cannot_hide_in_git_exclusions(self):
        for name in ("unexpected.go", "vendor/hidden.go", "payload.ignored"):
            with self.subTest(name=name):
                path = self.source / name
                path.parent.mkdir(exist_ok=True)
                path.write_text("package payload\n")
                with self.assertRaisesRegex(BuildError, "unexpected files"):
                    self.verify()
                path.unlink()

    def test_modified_type_mode_or_missing_file_is_rejected(self):
        path = self.source / "source.go"
        content = path.read_bytes()
        path.chmod(0o755)
        with self.assertRaisesRegex(BuildError, "file type/mode"):
            self.verify()
        path.unlink()
        with self.assertRaisesRegex(BuildError, "missing tracked"):
            self.verify()
        external = self.base / "external.go"
        external.write_bytes(content)
        path.symlink_to(external)
        with self.assertRaisesRegex(BuildError, "file type/mode"):
            self.verify()

    def test_replaced_parent_directory_cannot_supply_tracked_source(self):
        directory = self.source / "cmd"
        outside = self.base / "outside-cmd"
        directory.rename(outside)
        directory.symlink_to(outside, target_is_directory=True)
        with self.assertRaises(BuildError):
            self.verify()

    def test_object_store_symlink_and_corruption_are_rejected(self):
        objects = self.source / ".git/objects"
        outside = self.base / "objects"
        objects.rename(outside)
        objects.symlink_to(outside, target_is_directory=True)
        with self.assertRaisesRegex(BuildError, "object store"):
            self.verify()
        objects.unlink()
        outside.rename(objects)
        commit = self.pins["sing_box_revision"]
        object_path = objects / commit[:2] / commit[2:]
        object_path.unlink()
        object_path.write_bytes(b"invalid object")
        with self.assertRaises(subprocess.CalledProcessError):
            self.verify()

    def test_poisoned_refs_and_index_do_not_override_the_pin(self):
        # Cached refs/index are not authorities; a pristine tree still has one
        # accepted identity even if its mutable metadata is corrupt.
        (self.source / ".git/HEAD").write_text("0" * 40 + "\n")
        (self.source / ".git/index").write_bytes(b"invalid index")
        self.verify()
        self.assertEqual(self.git("rev-parse", "HEAD"), self.pins["sing_box_revision"])
        self.assertEqual(self.git("status", "--porcelain", "--untracked-files=no"), "")


class PinTests(unittest.TestCase):
    def test_core_pins_and_dependency_matching_fork(self):
        self.assertEqual(pin_identity(load_pins()), {
            "sing_box_version": "1.14.4",
            "sing_box_revision": "c005f35300ce76427dc285481ec5695f5b179cd2",
            "tailscale_go_revision": "63ae404c8203317fd3c82d972e5dc8f0fcb425cb",
            "gomobile_version": "v0.1.12",
            "wgc_version": "v0.2.1-0.20261107180014-c3ca5bb852e4",
            "wgc_source_sha256": pin_identity(load_pins())["wgc_source_sha256"],
            "wgc_overlay_sha256": sha256(ROOT / "tool/wgc_overlay.py"),
        })

    def test_revision_and_checksum_lock_must_agree(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "tool").mkdir()
            pins = load_pins()
            pins["tailscale_go_revision"] = "1" * 40
            (root / "native-core.json").write_text(json.dumps(pins))
            (root / "tool/tailscale-go.json").write_text((ROOT / "tool/tailscale-go.json").read_text())
            with self.assertRaisesRegex(BuildError, "checksum lock"):
                load_pins(root)

    def test_release_cannot_change_core_or_mobile_features(self):
        pins = load_pins()
        profile = json.loads((ROOT / "tool/mobile-profile.json").read_text())
        metadata = {"schema": 1, "target": "apple", "pins": pin_identity(pins),
                    "tags": [*REQUIRED_TAGS, "with_low_memory"],
                    "tailscale_dependency": {"toolchain_revision": pins["tailscale_go_revision"]},
                    "mobile_profile": {"profile_sha256": sha256(ROOT / "tool/mobile-profile.json"),
                                       "upstream_builder_sha256": profile["upstream_builder_sha256"],
                                       "removed_tags": profile["drop_tags"]}}
        metadata["wgc"] = {"source_sha256": metadata["pins"]["wgc_source_sha256"],
                           "overlay_sha256": metadata["pins"]["wgc_overlay_sha256"], "extensions": []}
        validate_metadata(metadata, pins, "apple")
        metadata["pins"]["sing_box_revision"] = "f" * 40
        with self.assertRaises(BuildError):
            validate_metadata(metadata, pins, "apple")
        metadata["pins"] = pin_identity(pins)
        metadata["tags"].remove("with_low_memory")
        with self.assertRaises(BuildError):
            validate_metadata(metadata, pins, "apple")

    def test_mobile_profile_checks_hash_and_removes_only_optional_tags(self):
        import hashlib
        content = b'sharedTags = append(sharedTags, "with_gvisor", "with_tailscale", "with_naive_outbound", "with_usbip", "with_openvpn", "with_openconnect")\nlegacyTags := filterTags(sharedTags, "with_naive_outbound")\n'
        profile = {"upstream_builder_sha256": hashlib.sha256(content).hexdigest(),
                   "drop_tags": ["with_naive_outbound", "with_usbip", "with_openvpn", "with_openconnect"]}
        patched = mobile_profile_bytes(content, profile)
        self.assertEqual(patched, b'sharedTags = append(sharedTags, "with_gvisor", "with_tailscale")\nlegacyTags := filterTags(sharedTags, "with_naive_outbound")\n')
        with self.assertRaises(BuildError):
            mobile_profile_bytes(content + b"// modified", profile)
        profile["drop_tags"] = ["with_tailscale"]
        with self.assertRaises(BuildError):
            mobile_profile_bytes(content, profile)

    def test_sha_manifest_rejects_paths_duplicates_and_missing_hash(self):
        good = "a" * 64 + "  libbox-1.14.4-apple.tar.gz\n"
        self.assertEqual(parse_manifest(good)["libbox-1.14.4-apple.tar.gz"], "a" * 64)
        for text in (good + good, "bad  a.tar.gz", "a" * 64 + "  ../bad.tar.gz", ""):
            with self.subTest(text=text), self.assertRaises(BuildError):
                parse_manifest(text)


class SafeExtractionTests(unittest.TestCase):
    def extract(self, entries):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        archive = root / "input.tar.gz"
        with tarfile.open(archive, "w:gz") as bundle:
            for name, kind, data in entries:
                info = tarfile.TarInfo(name)
                if kind == "file":
                    content = data.encode()
                    info.size = len(content)
                    bundle.addfile(info, io.BytesIO(content))
                else:
                    info.type = tarfile.SYMTYPE if kind == "symlink" else tarfile.LNKTYPE
                    info.linkname = data
                    bundle.addfile(info)
        destination = root / "out"
        safe_extract(archive, destination)
        return destination

    def test_regular_file_and_internal_framework_symlink(self):
        out = self.extract([("Libbox/Versions/A/Libbox", "file", "binary"),
                            ("Libbox/Versions/Current", "symlink", "A"),
                            ("Libbox/Libbox", "symlink", "Versions/Current/Libbox")])
        self.assertEqual((out / "Libbox/Libbox").read_text(), "binary")

    def test_rejects_traversal_absolute_and_windows_paths(self):
        for name in ("../escape", "/absolute", "a/../../escape", "C:/escape", "a\\escape"):
            with self.subTest(name=name), self.assertRaises(BuildError):
                self.extract([(name, "file", "payload")])

    def test_rejects_escaping_links_link_writes_and_hardlinks(self):
        for entries in ([('a', 'symlink', '../escape')],
                        [('a', 'symlink', '/tmp/escape')],
                        [('a', 'symlink', 'b'), ('a/file', 'file', 'payload')],
                        [('a', 'file', 'payload'), ('b', 'hardlink', 'a')],
                        [('a', 'symlink', 'b'), ('b', 'symlink', 'a')]):
            with self.subTest(entries=entries), self.assertRaises(BuildError):
                self.extract(entries)


class InstallerTests(unittest.TestCase):
    @contextlib.contextmanager
    def isolated_installer(self, plugin):
        with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(
                install_libbox, "build_directory", side_effect=lambda path, root=plugin: build_directory(path, root)), \
                mock.patch.object(install_libbox, "project_lock", return_value=contextlib.nullcontext()), \
                mock.patch.object(install_libbox, "validate_metadata"):
            yield

    def test_missing_trust_pin_rejects_local_and_remote_before_io(self):
        with tempfile.TemporaryDirectory() as temp:
            plugin = Path(temp) / "plugin"
            for source in (["--from-dir", temp], ["--release", "v0.2.0"]):
                with self.subTest(source=source), self.isolated_installer(plugin), \
                        mock.patch.object(install_libbox, "download") as fetch, \
                        mock.patch.object(install_libbox, "install_one") as install, \
                        contextlib.redirect_stderr(io.StringIO()) as error:
                    self.assertEqual(install_libbox.main(["--target", "android", *source]), 1)
                    self.assertIn("independently trusted", error.getvalue())
                    fetch.assert_not_called()
                    install.assert_not_called()
                    self.assertFalse(plugin.exists())

    def test_trusted_manifest_installs_but_resealed_archive_and_manifest_fail(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pins, source, sums, _ = self.release_fixture(root)
            name = archive_name(pins, "android")
            manifest = source / "SHA256SUMS"
            manifest.write_text(f"{sums[name]}  {name}\n")
            trusted = sha256(manifest)
            plugin = root / "plugin"
            arguments = ["--target", "android", "--from-dir", str(source), "--manifest-sha256", trusted]
            with self.isolated_installer(plugin):
                self.assertEqual(install_libbox.main(arguments), 0)
                before = (plugin / ARTIFACTS["android"]).read_bytes()
                # A producer replaces its AAR and updates every self-supplied seal.
                payload = root / "payload"
                with zipfile.ZipFile(payload / ARTIFACTS["android"], "w") as aar:
                    aar.writestr("AndroidManifest.xml", "manifest")
                    aar.writestr("classes.jar", "changed executable classes")
                metadata_path = payload / "native-build.json"
                metadata = json.loads(metadata_path.read_text())
                metadata["artifact_sha256"] = artifact_hashes(payload / ARTIFACTS["android"])
                metadata_path.write_text(json.dumps(metadata))
                with tarfile.open(source / name, "w:gz") as archive:
                    for entry in ("Legal", "LICENSE.sing-box", "native-build.json", ARTIFACTS["android"]):
                        archive.add(payload / entry, arcname=entry)
                manifest.write_text(f"{sha256(source / name)}  {name}\n")
                with contextlib.redirect_stderr(io.StringIO()) as error:
                    self.assertEqual(install_libbox.main([*arguments, "--force"]), 1)
                    self.assertIn("does not match --manifest-sha256", error.getvalue())
                self.assertEqual((plugin / ARTIFACTS["android"]).read_bytes(), before)
                receipt = json.loads((plugin / ".build/install/installed/android.json").read_text())
                self.assertEqual(receipt["manifest_sha256"], trusted)

    def test_downloaded_manifest_is_checked_against_independent_pin(self):
        with tempfile.TemporaryDirectory() as temp:
            response = io.BytesIO(b"a" * 64 + b"  forged.tar.gz\n")
            response.url = "https://example.invalid/SHA256SUMS"
            with self.isolated_installer(Path(temp) / "plugin"), \
                    mock.patch("native_common.urllib.request.urlopen", return_value=response), \
                    mock.patch.object(install_libbox, "install_one") as install, \
                    contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(install_libbox.main(["--target", "android", "--release", "v0.2.0",
                                                      "--manifest-sha256", "0" * 64]), 1)
                install.assert_not_called()

    def test_local_archive_home_path_expands_before_resolution(self):
        output = io.StringIO()
        with mock.patch("install_libbox.sys.stdout", output):
            result = install_libbox.main(["--target", "apple", "--from-dir", "~/native-release", "--dry-run"])
        self.assertEqual(result, 0)
        plan = json.loads(output.getvalue())
        self.assertEqual(plan["manifest"], str(Path.home() / "native-release/SHA256SUMS"))

    def release_fixture(self, root, links=False, legal_file=False, omit_legal=None):
        payload = root / "payload"
        payload.mkdir()
        legal = payload / "Legal"
        if legal_file:
            legal.write_text("dependency notice\n")
        else:
            legal.mkdir()
            for name in ("LICENSE", "THIRD_PARTY_NOTICES.md", "LICENSE.wgc"):
                if name != omit_legal:
                    (legal / name).write_text("dependency notice\n")
            if links:
                for name in ("repeat", "again"):
                    (legal / name).symlink_to(".", target_is_directory=True)
        (payload / "LICENSE.sing-box").write_text("upstream notice\n")
        artifact = payload / ARTIFACTS["android"]
        artifact.parent.mkdir(parents=True)
        with zipfile.ZipFile(artifact, "w") as aar:
            aar.writestr("AndroidManifest.xml", "manifest")
            aar.writestr("classes.jar", "classes")
        metadata = {"license_sha256": sha256(payload / "LICENSE.sing-box"),
                    "legal_sha256": artifact_hashes(legal), "artifact_sha256": artifact_hashes(artifact)}
        (payload / "native-build.json").write_text(json.dumps(metadata))
        pins = load_pins()
        source, cache = root / "release", root / "cache"
        source.mkdir()
        cache.mkdir()
        name = archive_name(pins, "android")
        with tarfile.open(source / name, "w:gz") as archive:
            for entry in ("Legal", "LICENSE.sing-box", "native-build.json", ARTIFACTS["android"]):
                archive.add(payload / entry, arcname=entry)
        return pins, source, {name: sha256(source / name)}, cache

    def test_regular_legal_notices_install(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pins, source, sums, cache = self.release_fixture(root)
            plugin = root / "plugin"
            with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"):
                install_libbox.install_one("android", pins, source, sums, cache, False, "test-manifest")
            self.assertTrue((plugin / ARTIFACTS["android"]).is_file())
            self.assertEqual((plugin / ".build/legal/android/LICENSE").read_text(), "dependency notice\n")

    def test_required_notice_omissions_fail_before_replacing_core(self):
        for omitted in ("LICENSE", "THIRD_PARTY_NOTICES.md", "LICENSE.wgc"):
            with self.subTest(omitted=omitted), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                pins, source, sums, cache = self.release_fixture(root, omit_legal=omitted)
                plugin = root / "plugin"
                previous = plugin / ARTIFACTS["android"]
                previous.parent.mkdir(parents=True)
                previous.write_bytes(b"previous-core")
                notices = plugin / ".build/legal/android"
                notices.mkdir(parents=True)
                (notices / "keep").write_bytes(b"previous-notice")
                with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"):
                    with self.assertRaisesRegex(BuildError, "required license"):
                        install_libbox.install_one("android", pins, source, sums, cache, True, "test-manifest")
                self.assertEqual(previous.read_bytes(), b"previous-core")
                self.assertEqual((notices / "keep").read_bytes(), b"previous-notice")
                self.assertFalse((cache / "installed/android.json").exists())

    def test_legal_destination_cannot_escape_through_symlinked_parents(self):
        for name, victim_path in [(".build", "legal/android"), (".build/legal", "android"),
                                  (".build/legal/android", ".")]:
            with self.subTest(path=name), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                pins, source, sums, cache = self.release_fixture(root)
                plugin, outside = root / "plugin", root / "outside"
                previous = plugin / ARTIFACTS["android"]
                previous.parent.mkdir(parents=True)
                previous.write_bytes(b"previous-core")
                victim = outside / victim_path
                victim.mkdir(parents=True)
                (victim / "keep").write_bytes(b"external-data")
                link = plugin / name
                link.parent.mkdir(parents=True, exist_ok=True)
                link.symlink_to(outside, target_is_directory=True)
                with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"):
                    with self.assertRaisesRegex(BuildError, "inside project .build"):
                        install_libbox.install_one("android", pins, source, sums, cache, True, "test-manifest")
                self.assertEqual((victim / "keep").read_bytes(), b"external-data")
                self.assertEqual(previous.read_bytes(), b"previous-core")
                self.assertFalse((cache / "installed/android.json").exists())

    def test_internal_legal_destination_symlinks_are_rejected(self):
        for name, victim_path in [(".build/legal", "android"), (".build/legal/android", ".")]:
            with self.subTest(path=name), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                pins, source, sums, cache = self.release_fixture(root)
                plugin = root / "plugin"
                previous = plugin / ARTIFACTS["android"]
                previous.parent.mkdir(parents=True)
                previous.write_bytes(b"previous-core")
                target = plugin / ".build/install-cache"
                victim = target / victim_path
                victim.mkdir(parents=True)
                (victim / "keep").write_bytes(b"cache-data")
                link = plugin / name
                link.parent.mkdir(parents=True, exist_ok=True)
                link.symlink_to(target, target_is_directory=True)
                with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"):
                    with self.assertRaisesRegex(BuildError, "symlink"):
                        install_libbox.install_one("android", pins, source, sums, cache, True, "test-manifest")
                self.assertEqual((victim / "keep").read_bytes(), b"cache-data")
                self.assertEqual(previous.read_bytes(), b"previous-core")

    def test_install_failures_restore_core_notices_and_receipt(self):
        for failure in ("copy", "legal-backup", "legal-commit", "receipt-commit", "receipt-write"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                pins, source, sums, cache = self.release_fixture(root)
                plugin = root / "plugin"
                previous = plugin / ARTIFACTS["android"]
                previous.parent.mkdir(parents=True)
                previous.write_bytes(b"previous-core")
                notices = plugin / ".build/legal/android"
                notices.mkdir(parents=True)
                (notices / "old-license").write_bytes(b"previous-notice")
                receipt = cache / "installed/android.json"
                receipt.parent.mkdir()
                receipt.write_bytes(b"previous-receipt")
                original_copy, original_rename, original_write = install_libbox.shutil.copytree, Path.rename, install_libbox.write_json

                def copy(source, destination, *args, **kwargs):
                    if failure == "copy":
                        Path(destination).mkdir()
                        (Path(destination) / "partial").write_bytes(b"partial")
                        raise OSError("injected copy failure")
                    return original_copy(source, destination, *args, **kwargs)

                def rename(path, destination):
                    if ((failure == "legal-backup" and path == notices.resolve()) or
                            (failure == "legal-commit" and path.name == "next-legal") or
                            (failure == "receipt-commit" and path.name == "next-install.json")):
                        raise OSError("injected rename failure")
                    return original_rename(path, destination)

                def write(path, value):
                    if failure == "receipt-write" and path.name == "next-install.json":
                        raise OSError("injected receipt failure")
                    return original_write(path, value)

                with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"), mock.patch("install_libbox.shutil.copytree", side_effect=copy), mock.patch.object(Path, "rename", autospec=True, side_effect=rename), mock.patch("install_libbox.write_json", side_effect=write):
                    with self.assertRaises(OSError):
                        install_libbox.install_one("android", pins, source, sums, cache, True, "test-manifest")
                self.assertEqual(previous.read_bytes(), b"previous-core")
                self.assertEqual(sorted(path.name for path in notices.iterdir()), ["old-license"])
                self.assertEqual((notices / "old-license").read_bytes(), b"previous-notice")
                self.assertEqual(receipt.read_bytes(), b"previous-receipt")

    def test_failed_rollback_keeps_recovery_backups(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pins, source, sums, cache = self.release_fixture(root)
            plugin = root / "plugin"
            previous = plugin / ARTIFACTS["android"]
            previous.parent.mkdir(parents=True)
            previous.write_bytes(b"previous-core")
            original = Path.rename

            def rename(path, destination):
                if path.name in ("next-legal", "previous-0"):
                    raise OSError("injected commit and rollback failure")
                return original(path, destination)

            with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"), mock.patch.object(Path, "rename", autospec=True, side_effect=rename):
                with self.assertRaisesRegex(BuildError, "backups retained"):
                    install_libbox.install_one("android", pins, source, sums, cache, True, "test-manifest")
            backups = list(cache.glob("install-android-*/previous-0"))
            self.assertEqual(len(backups), 1)
            self.assertEqual(backups[0].read_bytes(), b"previous-core")

    def test_completed_install_survives_staging_cleanup_failure(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pins, source, sums, cache = self.release_fixture(root)
            plugin = root / "plugin"
            errors = io.StringIO()
            with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"), mock.patch("install_libbox.shutil.rmtree", side_effect=OSError("injected cleanup failure")), mock.patch("install_libbox.sys.stderr", errors):
                install_libbox.install_one("android", pins, source, sums, cache, False, "test-manifest")
            self.assertEqual(sha256(plugin / ARTIFACTS["android"]), sha256(root / "payload" / ARTIFACTS["android"]))
            self.assertEqual((plugin / ".build/legal/android/LICENSE").read_text(), "dependency notice\n")
            self.assertTrue((cache / "installed/android.json").is_file())
            self.assertIn("Installation completed", errors.getvalue())

    def test_cache_inside_notice_destination_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pins, source, sums, _ = self.release_fixture(root)
            plugin = root / "plugin"
            previous = plugin / ARTIFACTS["android"]
            previous.parent.mkdir(parents=True)
            previous.write_bytes(b"previous-core")
            cache = plugin / ".build/legal/android"
            cache.mkdir(parents=True)
            (cache / "keep").write_bytes(b"cache-data")
            with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"):
                with self.assertRaisesRegex(BuildError, "cache must be separate"):
                    install_libbox.install_one("android", pins, source, sums, cache, True, "test-manifest")
            self.assertEqual(previous.read_bytes(), b"previous-core")
            self.assertEqual((cache / "keep").read_bytes(), b"cache-data")

    def test_file_legal_payload_is_rejected_before_replacing_core(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pins, source, sums, cache = self.release_fixture(root, legal_file=True)
            plugin = root / "plugin"
            previous = plugin / ARTIFACTS["android"]
            previous.parent.mkdir(parents=True)
            previous.write_bytes(b"previous-core")
            with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"):
                with self.assertRaisesRegex(BuildError, "legal.*directory"):
                    install_libbox.install_one("android", pins, source, sums, cache, True, "test-manifest")
            self.assertEqual(previous.read_bytes(), b"previous-core")
            self.assertFalse((cache / "installed/android.json").exists())

    def test_invalid_legal_destination_preserves_previous_core(self):
        for name in (".build/legal", ".build/legal/android"):
            with self.subTest(path=name), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                pins, source, sums, cache = self.release_fixture(root)
                plugin = root / "plugin"
                previous = plugin / ARTIFACTS["android"]
                previous.parent.mkdir(parents=True)
                previous.write_bytes(b"previous-core")
                invalid = plugin / name
                invalid.parent.mkdir(parents=True)
                invalid.write_bytes(b"keep")
                with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"):
                    with self.assertRaises((BuildError, OSError)):
                        install_libbox.install_one("android", pins, source, sums, cache, True, "test-manifest")
                self.assertEqual(previous.read_bytes(), b"previous-core")
                self.assertEqual(invalid.read_bytes(), b"keep")

    def test_legal_directory_links_are_rejected_before_replacing_core(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pins, source, sums, cache = self.release_fixture(root, links=True)
            plugin = root / "plugin"
            previous = plugin / ARTIFACTS["android"]
            previous.parent.mkdir(parents=True)
            previous.write_bytes(b"previous-core")
            # Guard the old copying path so a regression cannot expand the links.
            with mock.patch.object(install_libbox, "ROOT", plugin), mock.patch.object(install_libbox, "validate_metadata"), mock.patch("install_libbox.shutil.copytree") as copying:
                with self.assertRaisesRegex(BuildError, "legal.*symlink"):
                    install_libbox.install_one("android", pins, source, sums, cache, True, "test-manifest")
                copying.assert_not_called()
            self.assertEqual(previous.read_bytes(), b"previous-core")
            self.assertFalse((cache / "installed/android.json").exists())


class DownloadTests(unittest.TestCase):
    def test_gateway_timeout_retries_and_verifies_the_download(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            payload = b"pinned toolchain archive"
            response = io.BytesIO(payload)
            response.url = "https://example.com/archive"
            failure = urllib.error.HTTPError(response.url, 504, "Gateway Time-out", None, None)
            with mock.patch("native_common.urllib.request.urlopen", side_effect=[failure, response]) as request, mock.patch("time.sleep") as sleep:
                path, metadata = download(response.url, root, hashlib.sha256(payload).hexdigest())
            self.assertEqual(request.call_count, 2)
            sleep.assert_called_once_with(1)
            self.assertEqual(path.read_bytes(), payload)
            self.assertEqual(metadata["sha256"], hashlib.sha256(payload).hexdigest())
            self.assertEqual(len(list(root.iterdir())), 1)

    def test_http_retry_is_bounded_and_does_not_retry_permanent_errors(self):
        for status, attempts in ((403, 1), (404, 1), (504, 3)):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as temp:
                failures = [urllib.error.HTTPError("https://example.com/archive", status, "HTTP failure", None, None) for _ in range(attempts)]
                with mock.patch("native_common.urllib.request.urlopen", side_effect=failures) as request, mock.patch("time.sleep") as sleep:
                    with self.assertRaises(urllib.error.HTTPError):
                        download("https://example.com/archive", Path(temp))
                self.assertEqual(request.call_count, attempts)
                self.assertEqual(sleep.call_count, attempts - 1)
                self.assertFalse(list(Path(temp).iterdir()))

    def test_checksum_failures_do_not_retry_or_leave_partial_downloads(self):
        with tempfile.TemporaryDirectory() as temp:
            response = io.BytesIO(b"unexpected bytes")
            response.url = "https://example.com/archive"
            with mock.patch("native_common.urllib.request.urlopen", return_value=response) as request, mock.patch("time.sleep") as sleep:
                with self.assertRaisesRegex(BuildError, "SHA-256"):
                    download(response.url, Path(temp), "0" * 64)
            self.assertEqual(request.call_count, 1)
            sleep.assert_not_called()
            self.assertFalse(list(Path(temp).iterdir()))


class ToolchainDownloadTests(unittest.TestCase):
    HOSTS = {"darwin-amd64": 470759042, "darwin-arm64": 470759043,
             "linux-amd64": 470759104, "linux-arm64": 470759044}

    def fixture(self, root, key):
        pins = load_pins()
        lock = json.loads((ROOT / "tool/tailscale-go.json").read_text())
        self.assertEqual(lock["asset_ids"], self.HOSTS)
        self.assertEqual(set(lock["sha256"]), set(self.HOSTS))
        payload = io.BytesIO()
        with tarfile.open(fileobj=payload, mode="w:gz") as archive:
            for name, content in (("go/bin/go", b"verified executable"),
                                  ("go/VERSION", (lock["version"] + "\n").encode())):
                info = tarfile.TarInfo(name)
                info.size, info.mode = len(content), 0o755
                archive.addfile(info, io.BytesIO(content))
        payload = payload.getvalue()
        lock["sha256"][key] = hashlib.sha256(payload).hexdigest()
        (root / "tool").mkdir()
        (root / "tool/tailscale-go.json").write_text(json.dumps(lock))
        bootstrap, cache = root / "bootstrap", root / "cache"
        bootstrap.mkdir()
        url = f"https://github.com/tailscale/go/releases/download/build-{pins['tailscale_go_revision']}/{key}.tar.gz"
        api = f"https://api.github.com/repos/tailscale/go/releases/assets/{self.HOSTS[key]}"
        return pins, lock, payload, bootstrap, cache, url, api

    def response(self, payload, url="https://release-assets.githubusercontent.com/archive?secret=not-for-provenance"):
        response = io.BytesIO(payload)
        response.url = url
        return response

    def failures(self, url, status=504, count=3):
        return [urllib.error.HTTPError(url, status, "HTTP failure", None, io.BytesIO())
                for _ in range(count)]

    def test_smoke_reextracts_pinned_archive_before_executing_a_forged_cache(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            key = "linux-amd64"
            pins, lock, payload, _, _, _, _ = self.fixture(root, key)
            bootstrap, cache = root / ".build/bootstrap", root / ".build/cache"
            goroot = bootstrap / f"tailscale-go-{key}"
            go = goroot / "bin/go"
            go.parent.mkdir(parents=True)
            go.write_bytes(b"edited compiler")
            go.chmod(0o755)
            # Forge precisely the fields trusted by the former smoke script.
            receipt = {"host": key, "revision": lock["revision"], "version": lock["version"],
                       "sha256": lock["sha256"][key], "binary_sha256": sha256(go)}
            (bootstrap / "toolchain-provenance.json").write_text(json.dumps(receipt))
            archives = cache / "archives"
            archives.mkdir(parents=True)
            archive = archives / f"tailscale-go-{pins['tailscale_go_revision']}-{key}.tar.gz"
            archive.write_bytes(payload)
            identity = {"GOROOT": str(goroot), "GOTOOLCHAIN": "local", "GOVERSION": lock["version"]}
            script = (ROOT / "tool/run_android_wgc_smoke.sh").read_text()
            compiler = script.split("<<'PY'\n", 1)[1].split("\nPY\n", 1)[0]
            commands = []

            def compiled(command, **kwargs):
                self.assertEqual(Path(command[0]).read_bytes(), b"verified executable")
                commands.append(command)

            with mock.patch("build_libbox.ROOT", root), mock.patch("build_libbox.host_key", return_value=key), \
                    mock.patch("build_libbox.run", return_value=json.dumps(identity)), \
                    mock.patch("native_common.urllib.request.urlopen", side_effect=AssertionError("No network")), \
                    mock.patch("subprocess.run", side_effect=compiled), \
                    mock.patch.object(sys, "argv", ["-", str(root), str(root / "wgc.test"), "/fixture/clang"]):
                exec(compile(compiler, "smoke-compiler-fixture", "exec"), {})
            self.assertEqual(len(commands), 1)
            self.assertEqual(commands[0][1:3], ["test", "-c"])
            self.assertEqual(go.read_bytes(), b"verified executable")

    def test_offline_bootstrap_requires_the_archive_not_a_receipt(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pins, _, _, bootstrap, cache, _, _ = self.fixture(root, "linux-amd64")
            (bootstrap / "toolchain-provenance.json").write_text("{}")
            with mock.patch("build_libbox.ROOT", root), mock.patch("build_libbox.host_key", return_value="linux-amd64"), \
                    mock.patch("build_libbox.run") as run, \
                    mock.patch("native_common.urllib.request.urlopen") as network:
                with self.assertRaisesRegex(BuildError, "archive is missing"):
                    bootstrap_go(pins, bootstrap, cache, offline=True)
                run.assert_not_called()
                network.assert_not_called()

    def test_primary_and_exhausted_gateway_fallback_keep_verified_identity_and_source(self):
        for key in self.HOSTS:
            for fallback in (False, True):
                with self.subTest(host=key, fallback=fallback), tempfile.TemporaryDirectory() as temp:
                    root = Path(temp)
                    pins, lock, payload, bootstrap, cache, url, api = self.fixture(root, key)
                    failures = self.failures(url) if fallback else []
                    responses = [*failures, self.response(payload)]
                    identity = {"GOROOT": str(bootstrap / f"tailscale-go-{key}"),
                                "GOTOOLCHAIN": "local", "GOVERSION": lock["version"]}
                    with mock.patch("build_libbox.ROOT", root), mock.patch("build_libbox.host_key", return_value=key), mock.patch("build_libbox.run", return_value=json.dumps(identity)) as run, mock.patch("native_common.urllib.request.urlopen", side_effect=responses) as request, mock.patch("time.sleep"):
                        go, env, provenance = bootstrap_go(pins, bootstrap, cache)
                        self.assertEqual(request.call_count, 4 if fallback else 1)
                        requests = [call.args[0] for call in request.call_args_list]
                        self.assertEqual([req.full_url for req in requests], [url] * (3 if fallback else 1) + ([api] if fallback else []))
                        self.assertEqual(requests[-1].get_header("Accept"), "application/octet-stream" if fallback else None)
                        self.assertEqual(provenance["url"], api if fallback else url)
                        self.assertEqual(provenance["sha256"], lock["sha256"][key])
                        self.assertEqual(provenance["binary_sha256"], sha256(go))
                        self.assertEqual(env["GOTOOLCHAIN"], "local")
                        self.assertEqual(run.call_args.args[0][0], go)
                        self.assertNotIn("secret", json.dumps(provenance))
                        self.assertTrue(all(error.fp.closed for error in failures))
                        # Cached archives retain the original route and still verify/run identity.
                        _, _, cached = bootstrap_go(pins, bootstrap, cache)
                        self.assertEqual(cached["url"], provenance["url"])
                        self.assertTrue(cached["cached"])
                        self.assertEqual(request.call_count, len(responses))
                        self.assertEqual(run.call_count, 2)

    def test_failed_fallback_is_bounded_and_leaves_no_archive_or_partial_files(self):
        for failure in ("504", "404", "hash", "size", "http-redirect"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                pins, lock, payload, bootstrap, cache, url, api = self.fixture(root, "linux-amd64")
                if failure in ("504", "404"):
                    tail = self.failures(api, int(failure), 3 if failure == "504" else 1)
                    expected_error = urllib.error.HTTPError
                else:
                    tail = [self.response(b"altered" if failure == "hash" else payload,
                                          "http://unsafe.example/archive" if failure == "http-redirect" else api)]
                    expected_error = BuildError
                responses = [*self.failures(url), *tail]
                from native_common import _download_once
                # Exercise the same size limit on both routes without a multi-GB fixture.
                def limited_once(url, directory, expected, max_bytes, headers):
                    return _download_once(url, directory, expected,
                                          1 if failure == "size" else max_bytes, headers)
                with mock.patch("build_libbox.ROOT", root), mock.patch("build_libbox.host_key", return_value="linux-amd64"), mock.patch("build_libbox.run") as run, mock.patch("native_common.urllib.request.urlopen", side_effect=responses) as request, mock.patch("native_common._download_once", side_effect=limited_once), mock.patch("time.sleep"):
                    with self.assertRaises(expected_error):
                        bootstrap_go(pins, bootstrap, cache)
                self.assertEqual(request.call_count, len(responses))
                self.assertFalse(list((bootstrap / "downloads").iterdir()))
                self.assertFalse(list((cache / "archives").iterdir()))
                run.assert_not_called()

    def test_primary_permanent_http_and_integrity_failures_never_fall_back(self):
        for failure in ("403", "404", "hash", "cache"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                pins, lock, payload, bootstrap, cache, url, api = self.fixture(root, "darwin-arm64")
                if failure == "cache":
                    archives = cache / "archives"
                    archives.mkdir(parents=True)
                    (archives / f"tailscale-go-{pins['tailscale_go_revision']}-darwin-arm64.tar.gz").write_bytes(b"corrupt cache")
                responses = self.failures(url, int(failure), 1) if failure.isdigit() else [self.response(b"altered")]
                with mock.patch("build_libbox.ROOT", root), mock.patch("build_libbox.host_key", return_value="darwin-arm64"), mock.patch("build_libbox.run") as run, mock.patch("native_common.urllib.request.urlopen", side_effect=responses) as request, mock.patch("time.sleep") as sleep:
                    with self.assertRaises(urllib.error.HTTPError if failure.isdigit() else BuildError):
                        bootstrap_go(pins, bootstrap, cache)
                self.assertEqual(request.call_count, 0 if failure == "cache" else 1)
                self.assertTrue(all(call.args[0].full_url == url for call in request.call_args_list))
                sleep.assert_not_called()
                run.assert_not_called()
                self.assertFalse(list((bootstrap / "downloads").glob("*")))


class SmokeEmulatorTests(unittest.TestCase):
    def exercise(self, mode):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        base = Path(temporary.name)
        sdk = base / "sdk"
        sdk.mkdir()
        (base / ".build/logs").mkdir(parents=True)
        # This independent fixture process represents someone else's emulator.
        foreign = subprocess.Popen([sys.executable, "-I", "-c", "import time; time.sleep(60)"])
        self.addCleanup(lambda: foreign.wait(timeout=5))
        self.addCleanup(foreign.terminate)
        stub = f"#!{sys.executable} -I\n" + r'''
import json, os, signal, sys, time
from pathlib import Path
base = Path(os.environ["SMOKE_FIXTURE"])
mode = os.environ["SMOKE_MODE"]
name = Path(sys.argv[0]).name
args = sys.argv[1:]
if name == "avdmanager":
    sys.stdin.read()
    sys.exit(0)
if name == "emulator":
    token = next((arg.split("=", 1)[1] for arg in args if arg.startswith("qemu.sbf_smoke=")), "")
    (base / "launched.json").write_text(json.dumps({"token": token, "pid": os.getpid()}))
    if mode == "collision":
        sys.exit(1)
    def stop(*_):
        (base / "terminated").touch()
        sys.exit(0)
    signal.signal(signal.SIGTERM, stop)
    while True:
        time.sleep(0.02)
with (base / "adb-calls.jsonl").open("a") as stream:
    stream.write(json.dumps(args) + "\n")
if args == ["devices"]:
    print("List of devices attached")
    if mode == "existing":
        print("emulator-5554\tdevice")
    sys.exit(0)
args = args[2:]  # -s emulator-5554
if args == ["emu", "kill"]:
    os.kill(int(os.environ["FOREIGN_PID"]), signal.SIGTERM)
    sys.exit(0)
if args[:2] == ["shell", "getprop"]:
    if args[2] == "qemu.sbf_smoke":
        path = base / "launched.json"
        print("foreign" if mode in ("existing", "collision") or (base / "replaced").exists()
              else json.loads(path.read_text())["token"] if path.exists() else "")
    elif args[2] == "sys.boot_completed":
        print("1")
    else:
        print("x86_64")
elif args[0] == "push" and mode == "replace":
    (base / "replaced").touch()
elif args[0] == "shell" and "-test.run" in args[-1]:
    print("--- PASS: TestAndroidUDPProtectionSmoke (0.00s)")
'''
        for name in ("adb", "avdmanager", "emulator"):
            path = sdk / name
            path.write_text(stub)
            path.chmod(0o755)
        timeout = sdk / "timeout"
        timeout.write_text('#!/bin/sh\nshift 3\nexec "$@"\n')
        timeout.chmod(0o755)
        script = (ROOT / "tool/run_android_wgc_smoke.sh").read_text()
        # Run the actual launch/boot/cleanup section with only SDK/KVM and Go
        # prerequisites replaced; no real ADB server or emulator is contacted.
        runtime = script[script.index('export ANDROID_HOME="$sdk"'):]
        variables = {"root": str(base), "sdk": str(sdk), "adb": str(sdk / "adb"),
                     "emulator": str(sdk / "emulator"), "avdmanager": str(sdk / "avdmanager"),
                     "logs": str(base / ".build/logs"), "binary": str(base / "wgc.test")}
        harness = base / "smoke.sh"
        harness.write_text("set -euo pipefail\n" + "\n".join(
            f"{key}={shlex.quote(value)}" for key, value in variables.items()) + "\n" + runtime)
        environment = dict(os.environ, SMOKE_FIXTURE=str(base), SMOKE_MODE=mode,
                           FOREIGN_PID=str(foreign.pid), PATH=str(sdk) + os.pathsep + os.environ["PATH"])
        result = subprocess.run(["bash", str(harness)], env=environment, cwd=base,
                                text=True, capture_output=True, timeout=20)
        calls = [json.loads(line) for line in (base / "adb-calls.jsonl").read_text().splitlines()]
        self.assertIsNone(foreign.poll(), result.stdout + result.stderr)
        self.assertFalse(any(call[-2:] == ["emu", "kill"] for call in calls))
        self.assertFalse(list((base / ".build").glob("android-wgc-run.*")))
        return base, result, calls

    def test_existing_emulator_is_not_launched_over_or_stopped(self):
        base, result, calls = self.exercise("existing")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("existing device is untouched", result.stderr)
        self.assertFalse((base / "launched.json").exists())
        self.assertEqual(calls, [["devices"]])

    def test_collision_after_preflight_never_writes_to_or_kills_foreign_emulator(self):
        _, result, calls = self.exercise("collision")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any("push" in call or "chmod" in call or "rm" in call for call in calls))

    def test_owned_emulator_runs_smoke_and_only_its_process_is_stopped(self):
        base, result, calls = self.exercise("own")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((base / "terminated").exists())
        self.assertTrue(any("push" in call for call in calls))
        self.assertIn("--- PASS: TestAndroidUDPProtectionSmoke", result.stdout)

    def test_serial_replacement_stops_device_writes_and_preserves_foreign_process(self):
        base, result, calls = self.exercise("replace")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((base / "terminated").exists())
        self.assertFalse(any("chmod" in call or "rm" in call for call in calls))


class ToolchainTests(unittest.TestCase):
    def test_environment_always_selects_fork_and_project_caches(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            fork = root / "fork"
            inherited = {"GOROOT": "/stock-go", "GOTOOLCHAIN": "auto", "GOFLAGS": "-overlay=bad.json",
                         "GOENV": "malicious", "GOWORK": "workspace", "GOOS": "windows",
                         "GOPROXY": "https://untrusted.invalid", "CGO_CFLAGS": "unsafe", "PATH": "/usr/bin"}
            env = go_environment(fork, root / "cache", inherited)
            self.assertEqual(env["GOROOT"], str(fork))
            self.assertEqual(env["GOTOOLCHAIN"], "local")
            self.assertEqual(env["GOENV"], "off")
            self.assertEqual(env["GOWORK"], "off")
            self.assertEqual(env["PATH"].split(os.pathsep)[0], str(fork / "bin"))
            self.assertNotIn("GOOS", env)
            self.assertNotIn("CGO_CFLAGS", env)
            self.assertEqual(env["GOSUMDB"], "sum.golang.org")
            self.assertTrue(Path(env["GOMODCACHE"]).is_relative_to(root / "cache"))
            self.assertTrue(Path(env["TEST_TELEMETRY_DIR"]).is_relative_to(root / "cache"))
            self.assertEqual((Path(env["TEST_TELEMETRY_DIR"]) / "mode").read_text(), "off\n")

    def test_private_module_rules_survive_pinned_toolchain_environment(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            private = {
                "GOPRIVATE": "git.example.com/team/*,github.com/private-team/*",
                "GONOPROXY": "git.example.com/team/*",
                "GONOSUMDB": "git.example.com/team/*,github.com/private-team/*",
                "GOPROXY": "https://modules.example.com,direct",
                "GOROOT": "/wrong-toolchain", "GOTOOLCHAIN": "auto",
                "GOSUMDB": "off", "GOFLAGS": "-overlay=unexpected.json",
            }
            env = go_environment(root / "verified-fork", root / "cache", private)
            for name in ("GOPRIVATE", "GONOPROXY", "GONOSUMDB", "GOPROXY"):
                self.assertEqual(env[name], private[name])
            self.assertEqual(env["GOROOT"], str(root / "verified-fork"))
            self.assertEqual(env["GOTOOLCHAIN"], "local")
            self.assertEqual(env["GOSUMDB"], "sum.golang.org")
            self.assertNotIn("overlay", env["GOFLAGS"])

    def test_goprivate_uses_normal_go_defaults_without_forced_scope_overrides(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            env = go_environment(root / "fork", root / "cache", {
                "GOPRIVATE": "git.example.com/team/*",
            })
            self.assertEqual(env["GOPRIVATE"], "git.example.com/team/*")
            self.assertNotIn("GONOPROXY", env)
            self.assertNotIn("GONOSUMDB", env)
            self.assertEqual(env["GOPROXY"], "https://proxy.golang.org,direct")
            explicit = go_environment(root / "fork", root / "cache", {
                "GOPRIVATE": "git.example.com/team/*", "GONOPROXY": "none",
            })
            self.assertEqual(explicit["GONOPROXY"], "none")

    def test_resolved_tailscale_upgrade_is_verified_and_recorded(self):
        pins = load_pins()
        for compatible in (True, False):
            with self.subTest(compatible=compatible), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                (root / "go.toolchain.rev").write_text(pins["tailscale_go_revision"] if compatible else "1" * 40)
                (root / "go.toolchain.next.rev").write_text("2" * 40)
                selected = {"Path": "github.com/sagernet/tailscale", "Version": "v1.99.0",
                            "Dir": str(root), "Sum": "h1:resolved", "GoModSum": "h1:resolved-mod"}
                with mock.patch("build_libbox.network_run", return_value=json.dumps(selected)) as download:
                    if not compatible:
                        with self.assertRaisesRegex(BuildError, "runtime requirement"):
                            resolved_tailscale_dependency(Path("verified-go"), {}, root, pins)
                    else:
                        identity = resolved_tailscale_dependency(Path("verified-go"), {}, root, pins)
                        self.assertEqual(identity["version"], selected["Version"])
                        self.assertEqual(identity["sum"], selected["Sum"])
                        self.assertEqual(identity["go_mod_sum"], selected["GoModSum"])
                        self.assertEqual(identity["toolchain_revision"], pins["tailscale_go_revision"])
                    self.assertIn("github.com/sagernet/tailscale", download.call_args.args[0])

    def test_runtime_toolchain_contract_rejects_a_different_fork(self):
        actual = "63ae404c8203317fd3c82d972e5dc8f0fcb425cb"
        verify_dependency_toolchain(actual, actual + "\n", "1" * 40)
        verify_dependency_toolchain(actual, "1" * 40, actual)
        with self.assertRaisesRegex(BuildError, "runtime requirement"):
            verify_dependency_toolchain("7ef7b403648ac3a9481a6e88e8b894eff50b0167", actual, actual)

    def test_corrupt_toolchain_cache_is_never_executed(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            bootstrap = root / "bootstrap"
            bootstrap.mkdir()
            cache = root / "cache"
            (cache / "archives").mkdir(parents=True)
            pins = load_pins()
            archive = cache / "archives" / f"tailscale-go-{pins['tailscale_go_revision']}-darwin-arm64.tar.gz"
            archive.write_bytes(b"not a verified release")
            with mock.patch("build_libbox.host_key", return_value="darwin-arm64"), mock.patch("build_libbox.run") as run:
                with self.assertRaisesRegex(BuildError, "Cached Tailscale Go archive"):
                    bootstrap_go(pins, bootstrap, cache)
                run.assert_not_called()

    def test_bootstrap_cache_paths_cannot_escape_build(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.assertEqual(build_directory(root / ".build/cache", root), root.resolve() / ".build/cache")
            with self.assertRaises(BuildError):
                build_directory(root / "elsewhere", root)
            (root / ".build").symlink_to(root / "outside")
            with self.assertRaises(BuildError):
                build_directory(root / ".build/cache", root)


if __name__ == "__main__":
    unittest.main()
