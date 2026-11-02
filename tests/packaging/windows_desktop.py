#!/usr/bin/env python3
"""Exercise the real installed desktop and SYSTEM service in disposable Windows CI."""
import argparse
import ctypes
from ctypes import wintypes
import hashlib
import json
import os
from pathlib import Path
import platform
import queue
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time

ROOT = Path(__file__).resolve().parents[2]


def run(*args, timeout=120, check=True):
    environment = {key: value for key, value in os.environ.items() if key.upper() != "PSMODULEPATH"}
    result = subprocess.run(list(map(str, args)), cwd=ROOT, env=environment, capture_output=True, text=True,
                            timeout=timeout, check=False)
    if check and result.returncode:
        # This fixture never authenticates a Tailnet. Do not copy arbitrary
        # service event streams or private state into CI diagnostics.
        raise RuntimeError(f"{Path(str(args[0])).name} failed ({result.returncode}): " +
                           (result.stderr or result.stdout)[-4000:])
    return result


def powershell(code):
    return run("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", code).stdout.strip()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def architecture(path, arch):
    with path.open("rb") as stream:
        if stream.read(2) != b"MZ":
            raise RuntimeError("Not a Windows PE: " + path.name)
        stream.seek(0x3c)
        offset = struct.unpack("<I", stream.read(4))[0]
        stream.seek(offset)
        signature, machine = struct.unpack("<4sH", stream.read(6))
    if signature != b"PE\0\0" or machine != {"amd64": 0x8664, "arm64": 0xaa64}[arch]:
        raise RuntimeError("Wrong native architecture: " + path.name)


def private_directory(path, sid):
    path.mkdir(parents=True, exist_ok=True)
    # Every fixture file inherits the same real Windows policy as user state.
    descriptor = wintypes.LPVOID()
    advapi = ctypes.WinDLL("advapi32", use_last_error=True)
    advapi.ConvertStringSecurityDescriptorToSecurityDescriptorW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, ctypes.POINTER(wintypes.LPVOID), ctypes.POINTER(wintypes.DWORD)]
    advapi.GetSecurityDescriptorDacl.argtypes = [wintypes.LPVOID, ctypes.POINTER(wintypes.BOOL), ctypes.POINTER(wintypes.LPVOID), ctypes.POINTER(wintypes.BOOL)]
    advapi.GetSecurityDescriptorOwner.argtypes = [wintypes.LPVOID, ctypes.POINTER(wintypes.LPVOID), ctypes.POINTER(wintypes.BOOL)]
    advapi.SetNamedSecurityInfoW.argtypes = [wintypes.LPWSTR, wintypes.DWORD, wintypes.DWORD, wintypes.LPVOID, wintypes.LPVOID, wintypes.LPVOID, wintypes.LPVOID]
    sddl = f"O:{sid}D:P(A;OICI;FA;;;{sid})(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
    if not advapi.ConvertStringSecurityDescriptorToSecurityDescriptorW(sddl, 1, ctypes.byref(descriptor), None):
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        owner, dacl = wintypes.LPVOID(), wintypes.LPVOID()
        present, defaulted = wintypes.BOOL(), wintypes.BOOL()
        if not advapi.GetSecurityDescriptorOwner(descriptor, ctypes.byref(owner), ctypes.byref(defaulted)):
            raise ctypes.WinError(ctypes.get_last_error())
        if not advapi.GetSecurityDescriptorDacl(descriptor, ctypes.byref(present), ctypes.byref(dacl), ctypes.byref(defaulted)):
            raise ctypes.WinError(ctypes.get_last_error())
        result = advapi.SetNamedSecurityInfoW(str(path), 1, 0x80000005, owner, None, dacl, None)
        if result:
            raise ctypes.WinError(result)
    finally:
        ctypes.windll.kernel32.LocalFree(descriptor)


def routes():
    return powershell("Get-NetRoute | Where-Object { $_.DestinationPrefix -in @('0.0.0.0/0','0.0.0.0/1','128.0.0.0/1','::/0','::/1','8000::/1') } | Sort-Object InterfaceIndex,DestinationPrefix,NextHop | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric | ConvertTo-Json -Compress")


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def broker(core, config):
    process = subprocess.Popen([str(core), "app-run", "--events", "--parent-pid", str(os.getpid()), "-c", str(config)],
                               cwd=ROOT, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                               text=True, creationflags=subprocess.CREATE_NO_WINDOW)
    records = queue.Queue()
    def read():
        for line in process.stdout:
            try:
                records.put(json.loads(line))
            except json.JSONDecodeError:
                pass
        records.put(None)
    threading.Thread(target=read, daemon=True).start()
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        try:
            record = records.get(timeout=1)
        except queue.Empty:
            continue
        if record is None:
            raise RuntimeError("Installed service broker exited before opening the direct-only fixture")
        if record.get("event") == "error":
            raise RuntimeError("Installed service rejected the fixture: " + record.get("message", "unknown error"))
        if record.get("event") == "ready":
            return process
    process.kill()
    raise RuntimeError("Installed service did not become ready")


def stop_broker(process, crash=False):
    if crash:
        process.kill()
    else:
        process.stdin.write("stop\n")
        process.stdin.flush()
        process.stdin.close()
    process.wait(timeout=30)
    if not crash and process.returncode != 0:
        raise RuntimeError("Service broker did not acknowledge graceful cleanup")


def install(installer, sid, log):
    return run(installer, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-",
               "/UserSID=" + sid, "/LOG=" + str(log), timeout=300)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--arch", choices=("amd64", "arm64"), required=True)
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, default=ROOT / "bin/windows-desktop-evidence")
    args = parser.parse_args()
    if sys.platform != "win32" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise RuntimeError("This test installs a service and is restricted to disposable native Windows CI")
    native = {"amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
    if native != args.arch:
        raise RuntimeError("Desktop execution needs the native target architecture")
    version = (ROOT / "VERSION").read_text().strip()
    expected_commit = run("git", "rev-parse", "HEAD").stdout.strip()
    installer = args.directory.resolve() / f"TailSplit_{version}_windows_{args.arch}_setup.exe"
    if not installer.is_file():
        raise RuntimeError("Desktop installer is missing")
    args.evidence.mkdir(parents=True, exist_ok=True)
    sid = powershell("[Security.Principal.WindowsIdentity]::GetCurrent().User.Value")
    program_files = Path(os.environ.get("ProgramW6432", os.environ["ProgramFiles"]))
    installed = program_files / "TailSplit"
    core, gui = installed / "tailsplit-core.exe", installed / "TailSplit.exe"
    before = routes()
    report = {"arch": args.arch, "version": version, "commit": expected_commit, "checks": {}}
    temporary = Path(tempfile.mkdtemp(prefix="tailsplit-desktop-native-"))
    private_directory(temporary, sid)
    state = temporary / "state"
    private_directory(state, sid)
    private_directory(state / "tailscale", sid)
    sentinel = b"isolated-ci-migration-sentinel-not-a-real-device-credential\n"
    (state / "tailscale/tailscaled.state").write_bytes(sentinel)
    config = temporary / "config.yaml"
    config.write_text("# Native desktop fixture: no Tailnet/cloud authorization.\n" +
                      f"state_dir: {json.dumps(str(state))}\nlisten: 127.0.0.1:{free_port()}\n" +
                      "dns:\n  direct: system\n  proxy: 8.8.8.8\nrules:\n  final: direct\ntun:\n  enabled: false\n", encoding="utf-8")
    process = None
    try:
        install(installer, sid, args.evidence / "install.log")
        for path in (core, gui, installed / "TailSplit.Window.dll"):
            architecture(path, args.arch)
        build = json.loads((installed / "build.json").read_text(encoding="utf-8"))
        if any(build.get(key) != value for key, value in {"version": version, "commit": expected_commit, "os": "windows", "arch": args.arch}.items()):
            raise RuntimeError("Installed desktop source metadata differs")
        if build.get("dirty") is not False:
            raise RuntimeError("Desktop was built from dirty inputs")
        service = json.loads(powershell("Get-CimInstance Win32_Service -Filter \"Name='TailSplitNetworkService'\" | Select-Object Name,State,StartName,PathName | ConvertTo-Json -Compress"))
        if service["State"] != "Running" or service["StartName"].lower() != "localsystem":
            raise RuntimeError("Installed service is not an idle SYSTEM service")
        if routes() != before:
            raise RuntimeError("Installing the desktop changed default routes")
        report["checks"].update(native_payloads=True, source_metadata=True, system_service=True, install_disconnected=True)
        process = broker(core, config)
        conflict = run(core, "run", "-c", config, check=False, timeout=20)
        if conflict.returncode == 0 or "already in use" not in conflict.stderr:
            raise RuntimeError("Legacy CLI was able to use the migrated identity concurrently")
        stop_broker(process)
        process = None
        runtime = Path(os.environ["ProgramData"]) / "TailSplit/users" / sid
        if (runtime / "tailscale/tailscaled.state").read_bytes() != sentinel or not (runtime / ".initialized").is_file():
            raise RuntimeError("Service migration did not retain the seeded identity")
        (state / "tailscale/tailscaled.state").write_bytes(b"stale-source-must-not-reimport\n")
        process = broker(core, config)
        stop_broker(process, crash=True)
        process = None
        deadline = time.monotonic() + 15
        while True:
            try:
                process = broker(core, config)
                break
            except RuntimeError:
                if time.monotonic() > deadline:
                    raise
                time.sleep(.2)
        stop_broker(process)
        process = None
        if (runtime / "tailscale/tailscaled.state").read_bytes() != sentinel:
            raise RuntimeError("Service reimported stale user-state after initialization")
        if routes() != before:
            raise RuntimeError("Service disconnect/crash did not preserve default routes")
        report["checks"].update(authenticated_broker=True, legacy_lock=True, identity_import_once=True, graceful_stop=True, broker_crash_cleanup=True)
        # This launches the real GUI as a medium-integrity user, queries its
        # actual accessibility tree, clicks controls and captures its window.
        ui = run("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-File", ROOT / "tests/packaging/windows_desktop_ui.ps1",
                 "-Application", gui, "-Config", config, "-Log", temporary / "tailsplit.log",
                 "-Screenshot", args.evidence / "desktop.png", timeout=180)
        report["checks"]["native_gui"] = json.loads(ui.stdout.strip().splitlines()[-1])
        hashes = {path.name: digest(path) for path in (core, gui, installed / "TailSplit.Window.dll")}
        install(installer, sid, args.evidence / "upgrade.log")
        if {path.name: digest(path) for path in (core, gui, installed / "TailSplit.Window.dll")} != hashes:
            raise RuntimeError("Repair changed verified payloads")
        if (runtime / "tailscale/tailscaled.state").read_bytes() != sentinel:
            raise RuntimeError("Repair discarded the device identity")
        report["checks"]["repair_retains_identity"] = True
        # Build a separate CI-only installer containing a native failing core.
        # No failure hook or test command is compiled into the shipping app.
        failed = ROOT / "bin/desktop-failure-fixture"
        failed.mkdir(parents=True, exist_ok=True)
        stub = failed / "main.go"
        stub.write_text('package main\nimport "os"\nfunc main() { os.Exit(77) }\n', encoding="utf-8")
        fake_core = failed / "tailsplit-core.exe"
        run("go", "build", "-o", fake_core, stub, timeout=180)
        failed_version = version.rsplit(".", 1)[0] + "." + str(int(version.rsplit(".", 1)[1]) + 1)
        failed_assets = failed / "assets"
        run(sys.executable, "-I", "-B", ROOT / "packaging/windows/build.py", "--arch", args.arch,
            "--version", failed_version, "--commit", expected_commit, "--core", fake_core,
            "--notices", installed / "THIRD_PARTY_NOTICES.md", "--directory", failed_assets,
            "--build-number", (ROOT / "BUILD_NUMBER").read_text().strip(), timeout=600)
        failed_installer = failed_assets / f"TailSplit_{failed_version}_windows_{args.arch}_setup.exe"
        rejected = run(failed_installer, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-",
                       "/UserSID="+sid, "/LOG="+str(args.evidence / "failed-upgrade.log"), check=False, timeout=180)
        if rejected.returncode == 0:
            raise RuntimeError("Failed service installation was reported as a successful upgrade")
        if {path.name: digest(path) for path in (core, gui, installed / "TailSplit.Window.dll")} != hashes:
            raise RuntimeError("Failed upgrade did not restore the working desktop payload")
        process = broker(core, config)
        stop_broker(process)
        process = None
        if (runtime / "tailscale/tailscaled.state").read_bytes() != sentinel:
            raise RuntimeError("Failed upgrade recovery lost the device identity")
        report["checks"]["failed_upgrade_restores_service"] = True
        uninstallers = list(installed.glob("unins*.exe"))
        if len(uninstallers) != 1:
            raise RuntimeError("Expected one supported desktop uninstall mechanism")
        run(uninstallers[0], "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/LOG="+str(args.evidence / "uninstall.log"), timeout=120)
        if core.exists() or gui.exists():
            raise RuntimeError("Uninstall left the application binaries installed")
        if (runtime / "tailscale/tailscaled.state").read_bytes() != sentinel or not config.is_file():
            raise RuntimeError("Uninstall removed saved identity/configuration")
        registered = powershell("(Get-ItemProperty 'HKLM:\\SOFTWARE\\TailSplit').DesktopUserSID")
        if registered != sid or routes() != before:
            raise RuntimeError("Uninstall lost user registration or network cleanup")
        report["checks"]["uninstall_retains_state"] = True
        report["status"] = "passed"
        (args.evidence / "desktop-verification.json").write_text(json.dumps(report, indent=2)+"\n", encoding="utf-8")
        print("Verified native Windows desktop, SYSTEM service, private state and installer:", args.arch)
    finally:
        if report.get("status") != "passed":
            report["status"] = "failed"
            (args.evidence / "desktop-verification.json").write_text(json.dumps(report, indent=2)+"\n", encoding="utf-8")
        if process is not None:
            process.kill()
            process.wait(timeout=15)
        # Keep evidence and private fixture data separate. Only explicit evidence
        # paths are uploaded by CI, never the device/runtime directories.
        if gui.exists():
            run(gui, "--quit", check=False, timeout=40)
        if core.exists():
            run(core, "windows-remove-service", check=False, timeout=45)


if __name__ == "__main__":
    main()
