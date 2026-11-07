#!/usr/bin/env python3
"""Verify and execute a native Windows CLI ZIP without starting a connection."""
import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import struct
import subprocess
import sys
import tempfile
import time
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def cleanup_smoke_directory(directory):
    # A just-exited executable can remain mapped or be scanned briefly on Windows.
    # Retry sharing/lock violations only, for at most five seconds.
    for attempt in range(20):
        try:
            shutil.rmtree(directory)
            return
        except OSError as error:
            if getattr(error, "winerror", None) not in (32, 33) or attempt == 19:
                raise
            time.sleep(0.25)


@contextmanager
def smoke_directory():
    directory = tempfile.mkdtemp(prefix="tailsplit-windows-smoke-")
    try:
        yield directory
    except BaseException:
        try:
            cleanup_smoke_directory(directory)
        except OSError as error:
            # Do not replace an actual archive/CLI failure with a cleanup failure.
            print(f"Warning: smoke-directory cleanup failed: {error}", file=sys.stderr)
        raise
    else:
        cleanup_smoke_directory(directory)


def verify(directory, arch):
    if sys.platform != "win32":
        raise ValueError("Windows archive execution requires a native Windows runner")
    native = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
    if native != arch:
        raise ValueError("Windows smoke test requires the matching runner architecture")
    version = (ROOT / "VERSION").read_text().strip()
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    archive = directory / f"tailsplit_{version}_windows_{arch}.zip"
    with smoke_directory() as temporary:
        work = Path(temporary)
        with zipfile.ZipFile(archive) as bundle:
            expected = {"tailsplit.exe", "LICENSE", "THIRD_PARTY_NOTICES.md", "build.json"}
            if len(bundle.infolist()) != 4 or set(bundle.namelist()) != expected:
                raise ValueError("Unexpected Windows archive inventory")
            for entry in bundle.infolist():
                if entry.is_dir() or (entry.external_attr >> 16) & 0o170000 != 0o100000:
                    raise ValueError("Windows archive must contain regular files only")
                (work / entry.filename).write_bytes(bundle.read(entry))
        info = json.loads((work / "build.json").read_text(encoding="utf-8"))
        for key, value in {"version": version, "commit": commit, "os": "windows", "arch": arch, "dirty": False}.items():
            if info.get(key) != value:
                raise ValueError("Windows build metadata mismatch: " + key)
        binary = work / "tailsplit.exe"
        header = binary.read_bytes()[:4096]
        offset = struct.unpack_from("<I", header, 0x3C)[0]
        if header[:2] != b"MZ" or header[offset:offset + 4] != b"PE\0\0":
            raise ValueError("Windows executable is not PE")
        if struct.unpack_from("<H", header, offset + 4)[0] != {"amd64": 0x8664, "arm64": 0xAA64}[arch]:
            raise ValueError("Windows executable architecture mismatch")
        notices = (work / "THIRD_PARTY_NOTICES.md").read_text(encoding="utf-8")
        if (ROOT / "LICENSES/wintun.txt").read_text(encoding="utf-8").strip() not in notices:
            raise ValueError("Missing full Wintun redistribution notice")
        home = work / "home"
        home.mkdir()
        sentinel = home / "preserved-config.txt"
        sentinel.write_text("user-owned state\n", encoding="utf-8")
        before = hashlib.sha256(sentinel.read_bytes()).hexdigest()
        environment = {key: value for key, value in os.environ.items() if not key.startswith("SOPS_AGE_")}
        environment.update(USERPROFILE=str(home), HOME=str(home),
                           LOCALAPPDATA=str(home / "AppData/Local"), APPDATA=str(home / "AppData/Roaming"),
                           HTTP_PROXY="http://127.0.0.1:1", HTTPS_PROXY="http://127.0.0.1:1", NO_PROXY="")
        def execute(*args, success=True):
            result = subprocess.run([str(binary), *args], env=environment, cwd=work,
                                    capture_output=True, text=True, timeout=45)
            if (result.returncode == 0) != success:
                raise ValueError(f"Unexpected CLI result for {args}: {result.stdout} {result.stderr}")
            return result.stdout
        output = execute("version")
        if f"tailsplit {version} (commit {commit};" not in output:
            raise ValueError("Windows executable version/commit mismatch")
        print(output.strip())
        execute("--help")
        config = work / "config.yaml"
        execute("init", "-c", str(config))
        saved = config.read_bytes()
        execute("init", "-c", str(config), success=False)
        if config.read_bytes() != saved:
            raise ValueError("init changed existing configuration")
        local_rules = work / "fixture-gfwlist.txt"
        local_rules.write_text("[AutoProxy 0.2.9]\n||fixture.example\n@@||safe.fixture.example\n", encoding="utf-8")
        source = "  source: builtin"
        text = saved.decode("utf-8")
        if text.count(source) != 1:
            raise ValueError("Example configuration has an unexpected rule source")
        config.write_bytes(text.replace(source, "  source: " + json.dumps(str(local_rules))).encode("utf-8"))
        execute("check", "-c", str(config))
        json.loads(execute("render", "-c", str(config)))
        if hashlib.sha256(sentinel.read_bytes()).hexdigest() != before:
            raise ValueError("CLI changed existing user state")
        if (home / ".local/share/letsolo/tailsplit").exists():
            raise ValueError("Offline commands created runtime device state")
    print(f"Verified native Windows {arch} ZIP, private config, offline commands and retained user state")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--arch", choices=("amd64", "arm64"), required=True)
    args = parser.parse_args()
    verify(args.directory, args.arch)


if __name__ == "__main__":
    main()
