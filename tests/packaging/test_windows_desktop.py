#!/usr/bin/env python3
"""Portable unit tests for the Windows desktop packager (no native tools run)."""
import argparse
import base64
import gzip
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import struct
import tempfile
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("windows_desktop_build", ROOT / "packaging/windows/build.py")
packager = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(packager)
COMMIT = "0123456789abcdef0123456789abcdef01234567"


def write_pe(path, arch="amd64"):
    data = bytearray(160)
    data[:2] = b"MZ"
    struct.pack_into("<I", data, 60, 128)
    data[128:132] = b"PE\0\0"
    struct.pack_into("<H", data, 132, packager.MACHINES[arch])
    Path(path).write_bytes(data)


class IdentityTests(unittest.TestCase):
    def test_artifact_identity_is_desktop_exe_only(self):
        result = packager.identity("arm64", "1.2.3", COMMIT.upper(), 14)
        self.assertEqual(result["commit"], COMMIT)
        self.assertIs(result["dirty"], False)
        self.assertEqual(packager.artifact_name(result), "TailSplit_1.2.3_windows_arm64_setup.exe")

    def test_rejects_unusable_versions_and_commits(self):
        cases = [("amd64", version, COMMIT, 0) for version in
                 ("v1.2.3", "1.2", "1.2.3-beta", "01.2.3", "1.2.65536", "1.2.3 /Dfoo")]
        cases += [("x86", "1.2.3", COMMIT, 0), ("arm64", "1.2.3", "f" * 39, 0),
                  ("arm64", "1.2.3", "g" * 40, 0), ("amd64", "1.2.3", COMMIT, -1),
                  ("amd64", "1.2.3", COMMIT, 65536)]
        for values in cases:
            with self.subTest(values=values), self.assertRaises(packager.BuildError):
                packager.identity(*values)

    def test_wrong_native_runner_fails_before_tool_execution(self):
        args = argparse.Namespace(arch="arm64", version="1.2.3", commit=COMMIT, build_number=0,
                                  core=Path("missing.exe"), notices=Path("missing.md"), directory=Path("unused"))
        with patch.object(packager, "native_arch", return_value="amd64"), patch.object(packager.subprocess, "run") as run:
            with self.assertRaisesRegex(packager.BuildError, "native architecture"):
                packager.build(args)
            run.assert_not_called()

    @unittest.skipIf(os.name == "nt", "Non-Windows guard is tested only off Windows")
    def test_macos_and_linux_never_run_downloaded_executables(self):
        with self.assertRaisesRegex(packager.BuildError, "Windows runner"):
            packager.native_arch()


class PETests(unittest.TestCase):
    def test_rejects_x64_payload_in_arm64_package(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "core.exe"
            write_pe(path)
            packager.require_machine(path, "amd64")
            with self.assertRaisesRegex(packager.BuildError, "native arm64"):
                packager.require_machine(path, "arm64")
            write_pe(path, "arm64")
            packager.require_machine(path, "arm64")

    def test_malformed_pe_headers(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "core.exe"
            for content in (b"", b"MZ", bytes(64), b"MZ" + bytes(58) + struct.pack("<I", 0xFFFFFFFF)):
                path.write_bytes(content)
                with self.subTest(size=len(content)), self.assertRaises(packager.BuildError):
                    packager.pe_machine(path)


class DependencyTests(unittest.TestCase):
    def test_vendor_json_handles_plain_and_gzip_registration_metadata(self):
        expected = {"catalogEntry": {"version": "1.0.4314.35"}}
        for compressed in (False, True):
            data = json.dumps(expected).encode("utf-8")
            if compressed:
                data = gzip.compress(data)
            def download(_url, destination):
                destination.write_bytes(data)
                return destination
            with self.subTest(compressed=compressed), tempfile.TemporaryDirectory() as temporary, \
                    patch.object(packager, "fetch", side_effect=download):
                result = packager.fetch_json("https://api.nuget.org/registration.json", Path(temporary), "metadata.json")
                self.assertEqual(result, expected)

    def test_only_official_https_dependency_hosts(self):
        allowed = ["https://api.nuget.org/v3-flatcontainer/package.nupkg",
                   "https://github.com/jrsoftware/issrc/releases/download/asset.exe",
                   "https://release-assets.githubusercontent.com/path",
                   "https://go.microsoft.com/fwlink/?linkid=2099616",
                   "https://msedge.sf.dl.delivery.mp.microsoft.com/installer.exe"]
        denied = ["http://api.nuget.org/a", "https://nuget.org.attacker.example/a",
                  "https://microsoft.com.attacker.example/a", "https://user:secret@api.nuget.org/a",
                  "file:///C:/installer.exe", "https://github.com:444/a", "https://attacker.example/a"]
        for url in allowed:
            packager.check_url(url)
        for url in denied:
            with self.subTest(url=url), self.assertRaises(packager.BuildError):
                packager.check_url(url)

    def test_nuget_hash_failure_prevents_extraction(self):
        config = {"webview2_sdk": {"version": "1.0.4314.35", "package": "Microsoft.Web.WebView2",
                                  "registration_url": "https://api.nuget.org/registration.json",
                                  "package_url": "https://api.nuget.org/sdk.nupkg"}}
        entry = {"catalogEntry": {"id": "Microsoft.Web.WebView2", "version": "1.0.4314.35",
                                  "packageHashAlgorithm": "SHA512",
                                  "packageHash": base64.b64encode(bytes(64)).decode()}}
        def download(_url, destination):
            destination.write_bytes(b"tampered")
            return destination
        with tempfile.TemporaryDirectory() as temporary, \
                patch.object(packager, "fetch_json", return_value=entry), \
                patch.object(packager, "fetch", side_effect=download), \
                patch.object(packager, "extract_sdk") as extract:
            with self.assertRaisesRegex(packager.BuildError, "SHA-512"):
                packager.prepare_sdk(Path(temporary), config)
            extract.assert_not_called()

    def test_inno_hash_failure_never_invokes_installer(self):
        config = {"inno": {"release_api": "https://api.github.com/releases/test", "tag": "is-7_1_1",
                           "asset": "innosetup-7.1.1-x64.exe", "publisher": "Pyrsys B.V.", "version": "7.1.1"}}
        release = {"tag_name": "is-7_1_1", "assets": [{"name": "innosetup-7.1.1-x64.exe",
                   "digest": "sha256:" + "0" * 64, "browser_download_url": "https://github.com/installer.exe"}]}
        def download(_url, destination):
            destination.write_bytes(b"tampered")
            return destination
        with tempfile.TemporaryDirectory() as temporary, \
                patch.object(packager, "fetch_json", return_value=release), \
                patch.object(packager, "fetch", side_effect=download), \
                patch.object(packager.subprocess, "run") as run:
            with self.assertRaisesRegex(packager.BuildError, "SHA-256"):
                packager.prepare_inno(Path(temporary), config)
            run.assert_not_called()

    def test_valid_hash_still_requires_authenticode_before_inno_runs(self):
        config = {"inno": {"release_api": "https://api.github.com/releases/test", "tag": "is-7_1_1",
                           "asset": "innosetup-7.1.1-x64.exe", "publisher": "Pyrsys B.V.", "version": "7.1.1"}}
        release = {"tag_name": "is-7_1_1", "assets": [{"name": "innosetup-7.1.1-x64.exe",
                   "digest": "sha256:" + hashlib.sha256(b"package").hexdigest(),
                   "browser_download_url": "https://github.com/installer.exe"}]}
        def download(_url, destination):
            destination.write_bytes(b"package")
            return destination
        with tempfile.TemporaryDirectory() as temporary, \
                patch.object(packager, "fetch_json", return_value=release), \
                patch.object(packager, "fetch", side_effect=download), \
                patch.object(packager, "verify_authenticode", side_effect=packager.BuildError("unsigned")) as verify, \
                patch.object(packager.subprocess, "run") as run:
            with self.assertRaisesRegex(packager.BuildError, "unsigned"):
                packager.prepare_inno(Path(temporary), config)
            verify.assert_called_once()
            run.assert_not_called()

    def test_verified_inno_is_bootstrapped_only_in_portable_mode(self):
        config = {"inno": {"release_api": "https://api.github.com/releases/test", "tag": "is-7_1_1",
                           "asset": "innosetup-7.1.1-x64.exe", "publisher": "Pyrsys B.V.", "version": "7.1.1"}}
        release = {"tag_name": "is-7_1_1", "assets": [{"name": "innosetup-7.1.1-x64.exe",
                   "digest": "sha256:" + hashlib.sha256(b"package").hexdigest(),
                   "browser_download_url": "https://github.com/installer.exe"}]}
        events = []
        def download(_url, destination):
            destination.write_bytes(b"package")
            return destination
        def invoke(command, **_kwargs):
            events.append("invoke")
            self.assertIn("/PORTABLE=1", command)
            self.assertIn("/CURRENTUSER", command)
            portable = Path(next(value.removeprefix("/DIR=") for value in command if value.startswith("/DIR=")))
            self.assertEqual(list(portable.iterdir()), [])
            (portable / "ISCC.exe").write_bytes(b"compiler")
        with tempfile.TemporaryDirectory() as temporary, \
                patch.object(packager, "fetch_json", return_value=release), \
                patch.object(packager, "fetch", side_effect=download), \
                patch.object(packager, "verify_authenticode", side_effect=lambda *args: events.append("verify")), \
                patch.object(packager.subprocess, "run", side_effect=invoke):
            compiler = packager.prepare_inno(Path(temporary), config)
            self.assertEqual(compiler, Path(temporary) / "inno/portable/ISCC.exe")
            self.assertEqual(events, ["verify", "invoke"])

    def test_runtime_rejects_bootstrapper_version_metadata(self):
        with patch.object(packager, "powershell", return_value=Path("powershell.exe")), \
                patch.object(packager.subprocess, "check_output", return_value="1.3.250.0\n"):
            with self.assertRaisesRegex(packager.BuildError, "standalone"):
                packager.executable_version("runtime.exe")
        with patch.object(packager, "powershell", return_value=Path("powershell.exe")), \
                patch.object(packager.subprocess, "check_output", return_value="156.0.4314.50\n"):
            self.assertEqual(packager.executable_version("runtime.exe"), "156.0.4314.50")

    def test_runtime_picks_matching_official_url_and_checks_signature(self):
        config = {"webview2_runtime": {"amd64": "https://go.microsoft.com/fwlink/?linkid=2124701",
                                      "arm64": "https://go.microsoft.com/fwlink/?linkid=2099616",
                                      "publisher": "Microsoft Corporation"}}
        with tempfile.TemporaryDirectory() as temporary, patch.object(packager, "fetch") as fetch, \
                patch.object(packager, "verify_authenticode", side_effect=packager.BuildError("unsigned")) as verify, \
                patch.object(packager, "executable_version") as version:
            fetch.side_effect = lambda url, path: path
            with self.assertRaisesRegex(packager.BuildError, "unsigned"):
                packager.prepare_runtime(Path(temporary), "arm64", config)
            self.assertEqual(fetch.call_args.args[0], config["webview2_runtime"]["arm64"])
            self.assertTrue(fetch.call_args.args[1].name.endswith("ARM64.exe"))
            self.assertEqual(verify.call_args.args[1], "Microsoft Corporation")
            version.assert_not_called()

    def test_authenticode_checks_chain_publisher_and_version_without_logging_environment(self):
        with patch.object(packager, "powershell", return_value=Path("powershell.exe")), \
                patch.object(packager.subprocess, "run") as run:
            packager.verify_authenticode(Path("installer.exe"), "Pyrsys B.V.", "7.1.1")
        command = run.call_args.args[0]
        script = base64.b64decode(command[-1]).decode("utf-16-le")
        self.assertIn("$s.Status -ne 'Valid'", script)
        self.assertIn("'Pyrsys B.V.'", script)
        self.assertIn(r"7\.1\.1", script)
        self.assertNotIn("Get-ChildItem Env", script)

    def test_toolchain_pins_official_stable_versions(self):
        config = json.loads((ROOT / "packaging/windows/toolchain.json").read_text())
        self.assertEqual(config["inno"]["version"], "7.1.1")
        self.assertEqual(config["webview2_sdk"]["version"], "1.0.4314.35")
        self.assertNotIn("prerelease", config["webview2_sdk"]["version"])


class ArchiveTests(unittest.TestCase):
    def make_archive(self, directory, entries):
        archive = directory / "sdk.nupkg"
        with zipfile.ZipFile(archive, "w") as output:
            for name, value in entries:
                output.writestr(name, value)
        return archive

    def test_extracts_into_new_directory_only(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            archive = self.make_archive(directory, [("build/native/include/WebView2.h", b"header"), ("LICENSE.txt", b"license")])
            extracted = packager.extract_sdk(archive, directory / "out")
            self.assertEqual((extracted / "LICENSE.txt").read_bytes(), b"license")
            with self.assertRaises(FileExistsError):
                packager.extract_sdk(archive, extracted)

    def test_rejects_traversal_windows_devices_ads_and_absolute_names(self):
        names = ("../outside", "/outside", "C:/outside", "\\\\server\\outside", "a/../../outside",
                 "a\\..\\outside", "a:stream", "a/CON.txt", "NUL", "LPT9.exe", "a./file", "a /file", "a//b", "./file")
        for name in names:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                archive = self.make_archive(directory, [("safe", b"first"), (name, b"malicious")])
                with self.assertRaises(packager.BuildError):
                    packager.extract_sdk(archive, directory / "out")
                self.assertEqual(list((directory / "out").iterdir()), [])
                self.assertFalse((directory / "outside").exists())

    def test_rejects_symlink_and_case_insensitive_duplicate_before_writing(self):
        for special in (True, False):
            with self.subTest(special=special), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                archive = directory / "sdk.nupkg"
                with zipfile.ZipFile(archive, "w") as output:
                    output.writestr("safe", b"first")
                    if special:
                        entry = zipfile.ZipInfo("link")
                        entry.create_system = 3
                        entry.external_attr = (stat.S_IFLNK | 0o777) << 16
                        output.writestr(entry, b"../../outside")
                    else:
                        output.writestr("SAFE", b"duplicate")
                with self.assertRaises(packager.BuildError):
                    packager.extract_sdk(archive, directory / "out")
                self.assertEqual(list((directory / "out").iterdir()), [])

    def test_extraction_size_limit(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            archive = self.make_archive(directory, [("a", b"1234"), ("b", b"5678")])
            with patch.object(packager, "MAX_EXTRACT", 7), self.assertRaisesRegex(packager.BuildError, "oversized"):
                packager.extract_sdk(archive, directory / "out")


class NoticesTests(unittest.TestCase):
    def make_sdk(self, directory, license_text=b"Original Microsoft license\r\n"):
        (directory / "Microsoft.Web.WebView2.nuspec").write_text('''<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
 <metadata><id>Microsoft.Web.WebView2</id><version>1.0.4314.35</version>
 <license type="file">LICENSE.txt</license></metadata></package>''')
        if license_text is not None:
            (directory / "LICENSE.txt").write_bytes(license_text)

    def test_original_license_bytes_required_and_preserved(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            license_bytes = b"\xef\xbb\xbfMICROSOFT SOFTWARE LICENSE TERMS\r\nOriginal text\r\n"
            self.make_sdk(directory, license_bytes)
            original = directory / "notices.md"
            original.write_bytes(b"Existing Go and Wintun notices\n")
            destination = directory / "combined.md"
            license_path = packager.sdk_license(directory, "1.0.4314.35")
            packager.append_notices(original, destination, license_path, "1.0.4314.35")
            result = destination.read_bytes()
            self.assertTrue(result.startswith(original.read_bytes()))
            self.assertTrue(result.endswith(license_bytes))
            self.assertIn(b"Microsoft.Web.WebView2 1.0.4314.35", result)

    def test_missing_empty_or_wrong_sdk_license_fails(self):
        for text in (None, b"", b"   "):
            with self.subTest(text=text), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                self.make_sdk(directory, text)
                with self.assertRaisesRegex(packager.BuildError, "license"):
                    packager.sdk_license(directory, "1.0.4314.35")
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            self.make_sdk(directory)
            with self.assertRaisesRegex(packager.BuildError, "pinned package"):
                packager.sdk_license(directory, "1.0.1111.1")

    def test_license_path_cannot_escape_sdk(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            self.make_sdk(directory)
            manifest = directory / "Microsoft.Web.WebView2.nuspec"
            manifest.write_text(manifest.read_text().replace("LICENSE.txt", "../LICENSE.txt"))
            with self.assertRaises(packager.BuildError):
                packager.sdk_license(directory, "1.0.4314.35")


class CommandAndResourceTests(unittest.TestCase):
    def test_go_gui_has_release_flags_and_no_cgo(self):
        info = packager.identity("amd64", "1.2.3", COMMIT)
        command = packager.gui_command("go", Path("TailSplit.exe"), info)
        self.assertEqual(command[-1], "./cmd/tailsplit-tray")
        self.assertIn("-trimpath", command)
        self.assertIn("-buildvcs=false", command)
        self.assertIn("with_gvisor", command)
        self.assertIn("-H=windowsgui", command[command.index("-ldflags") + 1])
        self.assertIn("-X main.commit=" + COMMIT, command[command.index("-ldflags") + 1])

    def test_native_dll_requires_matching_sdk_static_library(self):
        with tempfile.TemporaryDirectory() as temporary:
            sdk = Path(temporary)
            header = sdk / "build/native/include/WebView2.h"
            header.parent.mkdir(parents=True)
            header.write_text("header")
            library = sdk / "build/native/x64/WebView2LoaderStatic.lib"
            library.parent.mkdir(parents=True)
            library.write_bytes(b"lib")
            with self.assertRaisesRegex(packager.BuildError, "matching native"):
                packager.dll_command("cl", "window.cpp", sdk, "TailSplit.Window.dll", "arm64")
            library = sdk / "build/native/arm64/WebView2LoaderStatic.lib"
            library.parent.mkdir()
            library.write_bytes(b"lib")
            command = packager.dll_command("cl", "window.cpp", sdk, "TailSplit.Window.dll", "arm64")
            for flag in ("/std:c++17", "/MT", "/LD", "/MACHINE:ARM64", "ole32.lib", "user32.lib", "shlwapi.lib"):
                self.assertIn(flag, command)
            self.assertIn(str(library), command)
            self.assertFalse(any("native/x64" in value for value in command))

    def test_gui_manifest_is_as_invoker_and_per_monitor_dpi_aware(self):
        manifest = packager.manifest_bytes().decode()
        self.assertIn('level="asInvoker"', manifest)
        self.assertIn("PerMonitorV2", manifest)
        self.assertIn("longPathAware", manifest)
        self.assertNotIn("requireAdministrator", manifest)

    def test_native_version_resource_uses_numeric_components(self):
        info = packager.identity("arm64", "1.2.3", COMMIT)
        data = packager.version_resource(info, 42)
        self.assertEqual(struct.unpack_from("<H", data)[0], len(data))
        signature_offset = data.index(struct.pack("<I", 0xFEEF04BD))
        values = struct.unpack_from("<13I", data, signature_offset)
        self.assertEqual(values[2:4], ((1 << 16) | 2, (3 << 16) | 42))
        self.assertIn("TailSplit.exe\0".encode("utf-16-le"), data)

    def test_icon_includes_native_small_and_large_images(self):
        images = packager.icon_images()
        self.assertEqual([size for size, _ in images], [16, 32, 48, 256])
        for size, data in images:
            self.assertEqual(struct.unpack_from("<IiiHH", data), (40, size, size * 2, 1, 32))
            self.assertGreater(len(data), size * size * 4)

    def test_build_child_environment_does_not_forward_signing_credentials(self):
        with patch.dict(os.environ, {"GITHUB_TOKEN": "do-not-print", "SOPS_AGE_KEY": "do-not-print",
                                     "CL": "/wrong-target", "GOFLAGS": "-race", "PATH": "system-tools"}, clear=True):
            environment = packager.clean_environment()
        self.assertEqual(environment, {"PATH": "system-tools"})


class BuildContractTests(unittest.TestCase):
    def test_one_installer_asset_stages_distinct_gui_and_core(self):
        for arch in ("amd64", "arm64"):
            with self.subTest(arch=arch), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                source = directory / "repo"
                source.mkdir()
                (source / "config.example.yaml").write_text("example: true\n")
                (source / "LICENSE").write_text("project license\n")
                core = directory / "tailsplit.exe"
                write_pe(core, arch)
                notices = directory / "notices.md"
                notices.write_text("Go and Wintun notices\n")
                sdk = directory / "sdk"
                header = sdk / "build/native/include/WebView2.h"
                header.parent.mkdir(parents=True)
                header.write_text("header")
                library = sdk / "build/native" / packager.NATIVE_NAMES[arch] / "WebView2LoaderStatic.lib"
                library.parent.mkdir(parents=True)
                library.write_bytes(b"library")
                license_path = sdk / "LICENSE.txt"
                license_path.write_bytes(b"Original WebView2 SDK license\r\n")
                runtime = directory / "runtime.exe"
                runtime.write_bytes(b"Microsoft-signed runtime (mock)")
                args = argparse.Namespace(arch=arch, version="1.2.3", commit=COMMIT, build_number=7,
                                          core=core, notices=notices, directory=directory / "assets")
                observed = {}
                def execute(command, **kwargs):
                    self.assertEqual(kwargs["cwd"], source)
                    if command[0] == "go":
                        self.assertEqual(kwargs["env"]["CGO_ENABLED"], "0")
                        self.assertEqual(kwargs["env"]["GOARCH"], arch)
                        write_pe(command[command.index("-o") + 1], arch)
                    elif command[0] == "cl.exe":
                        target = next(value.removeprefix("/OUT:") for value in command if value.startswith("/OUT:"))
                        write_pe(target, arch)
                    elif command[0] == "ISCC.exe":
                        definitions = dict(value[2:].split("=", 1) for value in command if value.startswith("/D"))
                        observed.update(definitions)
                        stage = Path(definitions["StageDir"])
                        self.assertEqual({path.name for path in stage.iterdir()}, set(packager.PAYLOAD))
                        self.assertEqual((stage / "tailsplit-core.exe").read_bytes(), core.read_bytes())
                        observed["metadata"] = json.loads((stage / "build.json").read_text())
                        observed["notices"] = (stage / "THIRD_PARTY_NOTICES.md").read_bytes()
                        (Path(definitions["OutputDir"]) / (definitions["OutputBaseName"] + ".exe")).write_bytes(b"installer")
                    else:
                        self.fail("Unexpected tool invocation")
                def resources(_gui, icon, _info, _number):
                    icon.write_bytes(b"icon")
                with patch.object(packager, "ROOT", source), \
                        patch.object(packager, "native_arch", return_value=arch), \
                        patch.object(packager, "msvc_toolchain", side_effect=lambda _, env: ("cl.exe", env)), \
                        patch.object(packager, "prepare_sdk", return_value=(sdk, license_path)), \
                        patch.object(packager, "prepare_inno", return_value="ISCC.exe"), \
                        patch.object(packager, "prepare_runtime", return_value=(runtime, "156.0.4314.50")), \
                        patch.object(packager, "write_resources", side_effect=resources), \
                        patch.object(packager.subprocess, "run", side_effect=execute):
                    result = packager.build(args)
                self.assertEqual(list(result["assets"]), [f"TailSplit_1.2.3_windows_{arch}_setup.exe"])
                self.assertEqual(len(list(args.directory.iterdir())), 1)
                self.assertEqual(observed["metadata"], packager.identity(arch, "1.2.3", COMMIT))
                self.assertTrue(observed["notices"].endswith(license_path.read_bytes()))
                self.assertEqual(observed["RuntimeSHA256"], hashlib.sha256(runtime.read_bytes()).hexdigest())
                self.assertEqual(observed["RuntimeVersion"], "156.0.4314.50")
                self.assertEqual(observed["AppFileVersion"], "1.2.3.7")
                artifact = next(iter(result["assets"].values()))
                self.assertEqual(artifact["sha256"], hashlib.sha256(b"installer").hexdigest())
                self.assertEqual(artifact["size"], len(b"installer"))


if __name__ == "__main__":
    unittest.main()
