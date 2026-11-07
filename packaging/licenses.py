#!/usr/bin/env python3
"""Collect the license notices of modules compiled for a release platform."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
# LICENSES/go.txt is an unmodified copy from this official Go release tag.
GO_LICENSE_SOURCE = "https://raw.githubusercontent.com/golang/go/go1.26.0/LICENSE"
LICENSE_KINDS = ("license", "licence", "copying")
NOTICE_NAME = re.compile(
    r"^(license|licence|copying|notice|authors|patents|copyright)(?:$|[._-])",
    re.IGNORECASE,
)


def command(*args, env=None):
    return subprocess.check_output(args, cwd=ROOT, env=env, text=True, encoding="utf-8")


def read_notice(path):
    text = path.read_text(encoding="utf-8", errors="replace")
    if not text.strip():
        raise ValueError(f"Empty upstream license notice: {path}")
    return text


def go_license(goroot):
    installed = goroot / "LICENSE"
    try:
        return read_notice(installed)
    except FileNotFoundError:
        # Some packaged toolchains omit GOROOT/LICENSE. Releases must also work
        # offline, so use the upstream notice in this repository, not a download.
        vendored = ROOT / "LICENSES/go.txt"
        try:
            text = read_notice(vendored)
        except FileNotFoundError as error:
            raise ValueError(
                f"No Go license notice found at {installed} or {vendored}"
            ) from error
        return f"Source: {GO_LICENSE_SOURCE} (vendored as LICENSES/go.txt).\n\n{text}"


def notice_files(directory):
    # license_test.go and similar source files are not legal notices.
    return {path for path in directory.iterdir()
            if path.is_file() and path.suffix.lower() != ".go"
            and NOTICE_NAME.match(path.name)}


def module_notices(name, module, package_dirs):
    directory = Path(module["Dir"])
    notices = notice_files(directory)
    # Supplementary attribution alone must not hide a missing module license.
    if not any(NOTICE_NAME.match(path.name).group(1).lower() in LICENSE_KINDS
               for path in notices):
        raise ValueError(f"No upstream license notice found for {name} ({directory})")
    directories = {directory}
    for package_dir in package_dirs:
        package_dir.relative_to(directory)
        while package_dir != directory:
            directories.add(package_dir)
            package_dir = package_dir.parent
    # Include notices in linked package directories and their ancestors. Avoid
    # unrelated platform code, examples and tests elsewhere in the module.
    for package_dir in directories - {directory}:
        notices.update(notice_files(package_dir))
    return sorted(notices, key=lambda path: path.relative_to(directory).as_posix())


def collect(goos, goarch, output):
    environment = dict(os.environ, GOWORK="off", GOOS=goos, GOARCH=goarch,
                       CGO_ENABLED="1" if goos == "darwin" else "0")
    go = environment.get("GO", "go")
    stream = command(go, "list", "-mod=readonly", "-deps", "-json", "-tags", "with_gvisor", "./cmd/...", env=environment)
    decoder, modules, package_dirs = json.JSONDecoder(), {}, {}
    while stream.strip():
        package, end = decoder.raw_decode(stream.lstrip())
        stream = stream.lstrip()[end:]
        module = package.get("Module")
        if module and not module.get("Main"):
            module = module.get("Replace", module)
            name = module["Path"]
            modules[name] = module
            package_dirs.setdefault(name, set()).add(Path(package["Dir"]))
    goroot = Path(command(go, "env", "GOROOT", env=environment).strip())
    sections = ["# Third-party notices", "", (ROOT / "THIRD_PARTY_NOTICES.md").read_text(encoding="utf-8"),
                "## Go standard library", "", go_license(goroot)]
    for name, module in sorted(modules.items()):
        directory = Path(module["Dir"])
        notices = module_notices(name, module, package_dirs[name])
        sections += ["", f"## {name} {module.get('Version', '')}", ""]
        for path in notices:
            sections += [f"### {path.relative_to(directory).as_posix()}", "", read_notice(path), ""]
    if goos == "windows":
        sections += ["## Bundled Wintun driver", "", read_notice(ROOT / "LICENSES/wintun.txt")]
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text("\n".join(sections).rstrip() + "\n", encoding="utf-8")
    print(f"Collected notices for {len(modules)} modules: {output}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--os", choices=("linux", "darwin", "windows"), required=True)
    parser.add_argument("--arch", choices=("amd64", "arm64"), required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    collect(args.os, args.arch, args.output)


if __name__ == "__main__":
    main()
