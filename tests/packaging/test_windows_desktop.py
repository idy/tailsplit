#!/usr/bin/env python3
"""Portable unit tests for the Windows desktop packager (no native tools run)."""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import struct
import tempfile
import unittest
from unittest.mock import patch

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
    def test_vendor_json_handles_release_metadata_and_utf8_bom(self):
        expected = {"tag_name": "is-7_1_1", "assets": []}
        for bom in (b"", b"\xef\xbb\xbf"):
            def download(_url, destination):
                destination.write_bytes(bom + json.dumps(expected).encode("utf-8"))
                return destination
            with self.subTest(bom=bom), tempfile.TemporaryDirectory() as temporary, \
                    patch.object(packager, "fetch", side_effect=download):
                result = packager.fetch_json("https://api.github.com/releases/test", Path(temporary), "metadata.json")
                self.assertEqual(result, expected)

    def test_only_official_https_dependency_hosts(self):
        allowed = ["https://api.github.com/repos/jrsoftware/issrc/releases/test",
                   "https://github.com/jrsoftware/issrc/releases/download/asset.exe",
                   "https://release-assets.githubusercontent.com/path",
                   "https://go.microsoft.com/fwlink/?linkid=2099616",
                   "https://msedge.sf.dl.delivery.mp.microsoft.com/installer.exe"]
        denied = ["http://api.github.com/a", "https://api.nuget.org/a", "https://globalcdn.nuget.org/a",
                  "https://microsoft.com.attacker.example/a", "https://user:secret@api.github.com/a",
                  "file:///C:/installer.exe", "https://github.com:444/a", "https://attacker.example/a"]
        for url in allowed:
            packager.check_url(url)
        for url in denied:
            with self.subTest(url=url), self.assertRaises(packager.BuildError):
                packager.check_url(url)

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
        def invoke(command, **kwargs):
            events.append("invoke")
            self.assertIn("/PORTABLE=1", command)
            self.assertIn("/CURRENTUSER", command)
            self.assertNotIn("GH_TOKEN", kwargs["env"])
            self.assertNotIn("GITHUB_TOKEN", kwargs["env"])
            portable = Path(next(value.removeprefix("/DIR=") for value in command if value.startswith("/DIR=")))
            self.assertEqual(list(portable.iterdir()), [])
            (portable / "ISCC.exe").write_bytes(b"compiler")
        with tempfile.TemporaryDirectory() as temporary, \
                patch.dict(os.environ, {"GH_TOKEN": "test-token", "GITHUB_TOKEN": "other-test-token"}), \
                patch.object(packager, "fetch_json", return_value=release), \
                patch.object(packager, "fetch", side_effect=download), \
                patch.object(packager, "verify_authenticode", side_effect=lambda *args: events.append("verify")), \
                patch.object(packager.subprocess, "run", side_effect=invoke):
            compiler = packager.prepare_inno(Path(temporary), config)
            self.assertEqual(compiler, Path(temporary) / "inno/portable/ISCC.exe")
            self.assertEqual(events, ["verify", "invoke"])

    def test_runtime_uses_browser_minimum_instead_of_updater_wrapper_version(self):
        config = json.loads((ROOT / "packaging/windows/toolchain.json").read_text())
        for size in (1024, 12 * 1024 * 1024):
            def download(_url, path):
                with path.open("wb") as output:
                    output.truncate(size)
                return path
            with self.subTest(size=size), tempfile.TemporaryDirectory() as temporary, \
                    patch.object(packager, "fetch", side_effect=download), \
                    patch.object(packager, "verify_authenticode") as verify:
                if size < 10 * 1024 * 1024:
                    with self.assertRaisesRegex(packager.BuildError, "bootstrapper"):
                        packager.prepare_runtime(Path(temporary), "amd64", config)
                else:
                    path, minimum = packager.prepare_runtime(Path(temporary), "amd64", config)
                    self.assertEqual(minimum, config["webview2_runtime"]["minimum_version"])
                    self.assertEqual(path.stat().st_size, size)
                verify.assert_called_once()

    def test_runtime_picks_matching_official_url_and_checks_signature(self):
        config = json.loads((ROOT / "packaging/windows/toolchain.json").read_text())
        with tempfile.TemporaryDirectory() as temporary, patch.object(packager, "fetch") as fetch, \
                patch.object(packager, "verify_authenticode", side_effect=packager.BuildError("unsigned")) as verify:
            fetch.side_effect = lambda url, path: path
            with self.assertRaisesRegex(packager.BuildError, "unsigned"):
                packager.prepare_runtime(Path(temporary), "arm64", config)
            self.assertEqual(fetch.call_args.args[0], config["webview2_runtime"]["arm64"])
            self.assertTrue(fetch.call_args.args[1].name.endswith("ARM64.exe"))
            self.assertEqual(verify.call_args.args[1], "Microsoft Corporation")

    def test_authenticode_checks_chain_publisher_and_version_without_logging_environment(self):
        with patch.dict(os.environ, {"PSModulePath": "incompatible-core-modules"}), \
                patch.object(packager, "powershell", return_value=Path("powershell.exe")), \
                patch.object(packager.subprocess, "run") as run:
            packager.verify_authenticode(Path("installer.exe"), "Pyrsys B.V.", "7.1.1")
        self.assertNotIn("PSMODULEPATH", {key.upper() for key in run.call_args.kwargs["env"]})
        command = run.call_args.args[0]
        script = base64.b64decode(command[-1]).decode("utf-16-le")
        self.assertIn("$s.Status -ne 'Valid'", script)
        self.assertIn("'Pyrsys B.V.'", script)
        self.assertIn(r"7\.1\.1", script)
        self.assertNotIn("Get-ChildItem Env", script)

    def test_toolchain_pins_official_stable_versions(self):
        config = json.loads((ROOT / "packaging/windows/toolchain.json").read_text())
        self.assertEqual(config["inno"]["version"], "7.1.1")
        self.assertEqual(set(config), {"schema_version", "inno", "webview2_runtime"})
        self.assertEqual(config["webview2_runtime"]["publisher"], "Microsoft Corporation")


class NoticesTests(unittest.TestCase):
    def test_staging_preserves_original_generated_notices(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            notices = directory / "notices.md"
            original = b"\xef\xbb\xbfOriginal Go, Wintun and MyGo license notices\r\n"
            notices.write_bytes(original)
            core = directory / "core.exe"
            core.write_bytes(b"core")
            stage = directory / "payload"
            stage.mkdir()
            packager.stage_metadata(stage, core, notices, packager.identity("amd64", "1.2.3", COMMIT))
            self.assertEqual((stage / "THIRD_PARTY_NOTICES.md").read_bytes(), original)

    def test_staging_rejects_missing_or_empty_dependency_notices(self):
        for content in (None, b"", b" \r\n\t"):
            with self.subTest(content=content), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                notices = directory / "notices.md"
                if content is not None:
                    notices.write_bytes(content)
                stage = directory / "payload"
                stage.mkdir()
                with self.assertRaises((FileNotFoundError, packager.BuildError)):
                    packager.stage_metadata(stage, directory / "core.exe", notices,
                                            packager.identity("amd64", "1.2.3", COMMIT))
                self.assertEqual(list(stage.iterdir()), [])


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
        self.assertEqual([size for size, _ in images], [16, 20, 24, 32, 40, 48, 64, 256])
        for size, data in images:
            self.assertEqual(struct.unpack_from("<IiiHH", data), (40, size, size * 2, 1, 32))
            self.assertGreater(len(data), size * size * 4)
            # Transparent corners and antialiased edges at every native size.
            alpha = data[40:40 + size * size * 4][3::4]
            self.assertEqual(alpha[0], 0)
            self.assertTrue(any(0 < value < 255 for value in alpha))
        # Both tail prongs are white on the one-color tile, which shows through the
        # fork between them and through the eye. DIB rows are bottom-up and pixels
        # are BGRA.
        def pixel(size, data, column, row):
            offset = 40 + ((size - 1 - row) * size + column) * 4
            return tuple(data[offset:offset + 4])
        size, data = images[-1]
        scale = (size - 2 * size * .03) / 64
        def grid(x, y):
            return pixel(size, data, int(size / 2 + (x - 32) * scale), int(size / 2 + (y - 32) * scale))
        white, background = (255, 255, 255, 255), (30, 28, 28, 255)
        self.assertEqual((grid(22, 44.8), grid(22, 52.6)), (white, white))
        self.assertEqual((grid(16, 48), grid(*packager.ICON_EYE[:2]), grid(32, 4)), (background,) * 3)
        # Smaller icons keep the head solid because the eye's hole would only blur.
        self.assertEqual(pixel(*images[2], 18, 6), white)

    def test_build_child_environment_does_not_forward_signing_credentials(self):
        with patch.dict(os.environ, {"GITHUB_TOKEN": "do-not-print", "SOPS_AGE_KEY": "do-not-print",
                                     "CL": "/wrong-target", "GOFLAGS": "-race", "PSModulePath": "PowerShell-7-only-modules", "PATH": "system-tools"}, clear=True):
            environment = packager.clean_environment()
        self.assertEqual(environment, {"PATH": "system-tools"})


class InstallerContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.script = (ROOT / "packaging/windows/setup.iss").read_text(encoding="utf-8")

    def test_installer_keeps_identity_and_ships_only_the_two_native_executables(self):
        expected = ("TailSplit.exe", "tailsplit-core.exe", "config.example.yaml", "LICENSE",
                    "THIRD_PARTY_NOTICES.md", "build.json")
        files = self.script.split("[Files]", 1)[1].split("[Icons]", 1)[0]
        self.assertEqual(tuple(re.findall(r'Source: "\{#StageDir\}\\([^"\n]+)"', files)), expected)
        self.assertEqual(packager.PAYLOAD, expected)
        self.assertIn("AppId={{8ADFCBB5-3CF0-4E72-B173-706395019F21}", self.script)
        self.assertIn(r"DefaultDirName={autopf}\TailSplit", self.script)
        self.assertIn("ServiceName = 'TailSplitNetworkService';", self.script)
        quit_gui = self.script.split("procedure QuitDesktopGUI;", 1)[1].split("procedure RemoveObsoleteWindowDLL;", 1)[0]
        self.assertIn("Exec(GUI, '--quit', FixedAppDir, SW_HIDE, ewWaitUntilTerminated", quit_gui)
        self.assertIn("(ExitCode <> 0)", quit_gui)

    def test_recovery_journal_indices_stay_compatible_with_pre_mygo_installs(self):
        expected = ["TailSplit.exe", "tailsplit-core.exe", "TailSplit.Window.dll", "config.example.yaml",
                    "LICENSE", "THIRD_PARTY_NOTICES.md", "build.json", "unins000.exe", "unins000.dat", "unins000.msg"]
        assignments = re.findall(r"PayloadNames\[(\d+)\] := '([^']+)';", self.script)
        self.assertEqual(assignments, [(str(index), name) for index, name in enumerate(expected)])
        self.assertIn(f"PayloadCount = {len(expected)};", self.script)
        for array in ("PayloadNames", "PreviousFiles"):
            self.assertRegex(self.script, rf"{array}: array\[0\.\.{len(expected) - 1}\]")
        # The old DLL is still covered by snapshot creation, interrupted-install
        # journal loading and failed-install recovery, even though it is retired.
        for start, end in (("procedure CreateSnapshot;", "procedure LoadSnapshot;"),
                           ("procedure LoadSnapshot;", "procedure RecoverPrevious;"),
                           ("procedure RecoverPrevious;", "function InitializeSetup:")):
            block = self.script.split(start, 1)[1].split(end, 1)[0]
            self.assertIn("for Index := 0 to PayloadCount - 1 do begin", block)
            self.assertIn("PayloadNames[Index]", block)

    def test_legacy_dll_cleanup_is_transactional_and_does_not_delete_state(self):
        prepare = self.script.split("function PrepareToInstall(", 1)[1].split("procedure CurStepChanged(", 1)[0]
        operations = ["EnsureWebViewRuntime;", "QuitDesktopGUI;", "CreateSnapshot;",
                      "RunCore('windows-stop-service');", "StopFixedService;", "RemoveObsoleteWindowDLL;"]
        offsets = [prepare.index(operation) for operation in operations]
        self.assertEqual(offsets, sorted(offsets))
        self.assertIn("if SnapshotActive then begin", prepare)
        self.assertIn("RecoverPrevious;", prepare)
        cleanup = self.script.split("procedure RemoveObsoleteWindowDLL;", 1)[1].split("function ServiceHandle(", 1)[0]
        self.assertIn(r"LegacyDLL := FixedAppDir + '\TailSplit.Window.dll';", cleanup)
        self.assertIn("if FileExists(LegacyDLL) then", cleanup)
        self.assertIn("if not DeleteFile(LegacyDLL) then", cleanup)
        self.assertIn("RaiseException(", cleanup)
        self.assertNotIn("DelTree", cleanup)
        self.assertNotIn("[InstallDelete]", self.script)
        self.assertNotIn("[UninstallDelete]", re.sub(r"(?m)^;.*$", "", self.script))
        recovery = self.script.split("procedure RecoverPrevious;", 1)[1].split("function InitializeSetup:", 1)[0]
        self.assertIn(r"FileCopy(BackupDir + '\' + PayloadNames[Index], FixedAppDir + '\' + PayloadNames[Index], False)", recovery)
        self.assertIn("if PreviousService then begin", recovery)
        self.assertIn("StartPreviousService;", recovery)


class NativeUIContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.script = (ROOT / "tests/packaging/windows_desktop_ui.ps1").read_text(encoding="utf-8")

    def test_mygo_window_discovery_requires_the_launched_pid_and_executable(self):
        self.assertIn("$windowClasses = [string[]]@('MyGoWindow', ('TailSplit.Window.' + $accountSID))", self.script)
        discovery = self.script.split("public static IntPtr FindAppWindow(", 1)[1].split("public static int Integrity(", 1)[0]
        self.assertIn("OpenProcess(0x1000,false,pid)", discovery)
        self.assertIn("QueryFullProcessImageName(process,0,image,ref size)", discovery)
        self.assertIn("String.Equals(image.ToString(),System.IO.Path.GetFullPath(app),StringComparison.OrdinalIgnoreCase)", discovery)
        self.assertLess(discovery.index("QueryFullProcessImageName"), discovery.index("EnumWindows(visit"))
        self.assertIn("GetWindowThreadProcessId(window,out owner)", discovery)
        self.assertIn("if(owner!=pid)return true;", discovery)
        self.assertIn("String.Equals(name.ToString(),klass,StringComparison.Ordinal)", discovery)
        self.assertIn("finally {CloseHandle(process);}", discovery)
        self.assertNotIn("::FindWindow(", self.script)
        self.assertEqual(self.script.count("::FindAppWindow($guiProcess,$Application,$windowClasses)"), 2)
        self.assertIn("GetProp($handle,'TailSplit.WebView2.StartupStage')", self.script)
        self.assertIn("GetProp($window,'TailSplit.WebView2.Ready')", self.script)
        self.assertIn("GetProp($hiddenWindow,'TailSplit.WebView2.StartupStage')", self.script)

    def test_connect_action_is_distinct_from_navigation_with_legacy_fallback(self):
        html = (ROOT / "internal/configweb/assets/index.html").read_text(encoding="utf-8")
        translations = (ROOT / "internal/configweb/assets/i18n.js").read_text(encoding="utf-8")
        self.assertRegex(html, r'<button\b(?=[^>]*\bid="desktop-connect")(?=[^>]*\baria-label="连接 TailSplit")[^>]*>')
        self.assertIn("'连接 TailSplit': 'Connect TailSplit'", translations)
        self.assertIn("'连接页面': 'Connection'", translations)
        self.assertIn("'连接页面': '连接'", translations)
        self.assertIn("'连接 TailSplit'='Connect TailSplit'", self.script)
        self.assertIn("'连接页面'=@('出口节点','Exit nodes','连接','Connection')", self.script)
        self.assertIn("'连接'='Connect'", self.script)
        self.assertIn("$pageNavigation = [bool]((Find-Button $automation '连接 TailSplit') -or (Find-Button $automation '设置'))", self.script)
        self.assertIn("$connectName = if ($pageNavigation) { '连接 TailSplit' } else { '连接' }", self.script)
        # Both the normal connection and the connection left for Setup must use
        # the action name, even when the Chinese sidebar is also named 连接.
        self.assertEqual(self.script.count("Invoke-Button $automation $connectName"), 2)
        self.assertNotIn("Invoke-Button $automation '连接'", self.script)
        initial = self.script.split("$initialControls = if ($pageNavigation)", 1)[1].split("foreach ($name in $initialControls)", 1)[0]
        current, legacy = initial.split("} else {", 1)
        self.assertIn("$deviceAction", current)
        self.assertIn("$deviceAction = if (Find-Button $automation '添加出口') { '添加出口' } else { '登录 Tailscale' }", self.script)
        self.assertIn("'日志'", current)
        self.assertIn("'设置'", current)
        for hidden in ("'断开'", "'退出登录…'", "'保存并应用'"):
            self.assertNotIn(hidden, current)
        for required in ("'断开'", "'登录 Tailnet…'", "'退出设备登录'", "'查看日志'", "'保存并应用'"):
            self.assertIn(required, legacy)

    def test_refresh_follows_node_navigation_and_keeps_legacy_fallback(self):
        html = (ROOT / "internal/configweb/assets/index.html").read_text(encoding="utf-8")
        translations = (ROOT / "internal/configweb/assets/i18n.js").read_text(encoding="utf-8")
        # The first destination is now Exit Nodes and the header owns Refresh.
        # Older upgrade fixtures may still expose a dedicated node page.
        self.assertNotRegex(html, r'data-page="nodes"')
        header = html.split('<header class="titlebar"', 1)[1].split('</header>', 1)[0]
        self.assertRegex(header, r'<button\b[^>]*data-desktop-action="refresh-exits"[^>]*aria-label="刷新"')
        self.assertIn("'出口节点列表': 'Exit nodes'", translations)
        self.assertIn("'出口节点列表': '出口节点'", translations)
        self.assertIn("'出口节点页面'=@('出口节点','Exit nodes')", self.script)
        controls = self.script.split("foreach ($name in $initialControls)", 1)[1].split("$stage = 'checking initial", 1)[0]
        self.assertLess(controls.index("Invoke-Button $automation '出口节点页面'"), controls.index("Find-Button $automation '刷新'"))
        self.assertIn("elseif (-not (Find-Button $automation '刷新'))", controls)
        self.assertIn("Invoke-Button $automation '连接页面'", controls)

    def test_config_save_navigates_before_editing_and_checks_disk(self):
        save = self.script.split("$stage = 'saving configuration from native window'", 1)[1].split("$stage = 'connecting from native window'", 1)[0]
        self.assertRegex(save, r"(?s)if \(\$pageNavigation\) \{\s*Invoke-Button \$automation '设置'\s*Invoke-Button \$automation '网络'\s*\}")
        self.assertLess(save.index("'网络'"), save.index("Find-Control $automation '代理监听地址'"))
        self.assertIn("[System.Windows.Automation.ValuePattern]::Pattern", save)
        self.assertLess(save.index("$listenPattern.SetValue($savedListen)"), save.index("Invoke-Button $automation '保存并应用'"))
        self.assertIn("Get-Content -LiteralPath $Config -Raw).Contains($savedListen)", save)
        self.assertIn("-not $button.Current.IsEnabled", save)
        self.assertIn("if ($pageNavigation) { Invoke-Button $automation '连接页面' }", save)

    def test_log_workflow_checks_bounded_content_and_refresh_in_both_layouts(self):
        logs = self.script.split("$stage = 'reading and refreshing bounded native logs'", 1)[1].split("$stage = 'disconnecting from native window'", 1)[0]
        self.assertIn("if ($pageNavigation) { Invoke-Button $automation '日志' }", logs)
        self.assertIn("else { Invoke-Button $automation '查看日志' }", logs)
        self.assertIn("if ($pageNavigation) { Invoke-Button $automation '连接页面' }", logs)
        self.assertIn("else { Invoke-Button $automation '关闭' }", logs)
        self.assertIn("Assert-BoundedLogs $logText $oldLogMarker $latestLogMarker", logs)
        self.assertIn("Assert-BoundedLogs $logText $oldLogMarker $refreshedLogMarker", logs)
        self.assertLess(logs.index("Add-FixtureLog ($refreshedLogMarker"), logs.index("Invoke-Button $automation '刷新日志'"))
        self.assertIn("$text.Contains($refreshedLogMarker)", logs)
        bounds = self.script.split("function Assert-BoundedLogs", 1)[1].split("function Wait-Connected", 1)[0]
        self.assertIn("-not $Text.Contains($LatestMarker)", bounds)
        self.assertIn("$Text.Contains($OldMarker)", bounds)
        self.assertIn("$Text.Length -gt ((128 -shl 10) + 8192)", bounds)


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
                notices.write_bytes(b"Original Go, Wintun and MyGo notices\r\n")
                runtime = directory / "runtime.exe"
                runtime.write_bytes(b"Microsoft-signed runtime (mock)")
                args = argparse.Namespace(arch=arch, version="1.2.3", commit=COMMIT, build_number=7,
                                          core=core, notices=notices, directory=directory / "assets")
                observed = {}
                def execute(command, **kwargs):
                    self.assertEqual(kwargs["cwd"], source)
                    if command[0] == "go":
                        self.assertEqual(kwargs["env"]["CGO_ENABLED"], "0")
                        self.assertEqual(kwargs["env"]["GOOS"], "windows")
                        self.assertEqual(kwargs["env"]["GOARCH"], arch)
                        self.assertEqual(kwargs["env"]["GOWORK"], "off")
                        self.assertEqual(Path(command[command.index("-o") + 1]).name, "TailSplit.exe")
                        write_pe(command[command.index("-o") + 1], arch)
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
                        patch.object(packager, "prepare_inno", return_value="ISCC.exe"), \
                        patch.object(packager, "prepare_runtime", return_value=(runtime, "156.0.4314.50")), \
                        patch.object(packager, "write_resources", side_effect=resources) as resource_writer, \
                        patch.object(packager.subprocess, "run", side_effect=execute) as run:
                    result = packager.build(args)
                self.assertEqual([call.args[0][0] for call in run.call_args_list], ["go", "ISCC.exe"])
                resource_writer.assert_called_once()
                self.assertEqual(resource_writer.call_args.args[3], 7)
                self.assertEqual(list(result["assets"]), [f"TailSplit_1.2.3_windows_{arch}_setup.exe"])
                self.assertEqual(len(list(args.directory.iterdir())), 1)
                self.assertEqual(observed["metadata"], packager.identity(arch, "1.2.3", COMMIT))
                self.assertEqual(observed["notices"], notices.read_bytes())
                self.assertEqual(observed["RuntimeSHA256"], hashlib.sha256(runtime.read_bytes()).hexdigest())
                self.assertEqual(observed["RuntimeVersion"], "156.0.4314.50")
                self.assertEqual(observed["AppFileVersion"], "1.2.3.7")
                artifact = next(iter(result["assets"].values()))
                self.assertEqual(artifact["sha256"], hashlib.sha256(b"installer").hexdigest())
                self.assertEqual(artifact["size"], len(b"installer"))


if __name__ == "__main__":
    unittest.main()
