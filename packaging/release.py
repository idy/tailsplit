#!/usr/bin/env python3
"""Build, assemble and publish complete, versioned multi-platform releases."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
from urllib.parse import quote, urlparse
import zipfile

ROOT = Path(__file__).resolve().parents[1]
PLATFORMS = {(osname, arch) for osname in ("linux", "darwin", "windows") for arch in ("amd64", "arm64")}


def run(*args, env=None):
    return subprocess.check_output(list(map(str, args)), cwd=ROOT, env=env, text=True).strip()


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def metadata(tag=None, allow_dirty=False):
    module = (ROOT / "go.mod").read_text().splitlines()[0].split()[1]
    project = module.rsplit("/", 1)[-1]
    if project not in ("tailrelay", "tailsplit"):
        raise ValueError("Unknown release project")
    version = (ROOT / "VERSION").read_text().strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise ValueError("VERSION must be major.minor.patch")
    if tag is not None and tag != "v" + version:
        raise ValueError("Release tag must match the committed VERSION")
    commit = run("git", "rev-parse", "HEAD")
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("Release checkout must have a full source commit")
    if os.environ.get("GITHUB_ACTIONS") == "true" and tag:
        if run("git", "rev-parse", "refs/tags/" + tag + "^{commit}") != commit:
            raise ValueError("Release checkout must match the selected tag")
    dirty = bool(run("git", "status", "--porcelain", "--untracked-files=normal"))
    if dirty and not allow_dirty:
        raise ValueError("Commit release inputs first; --allow-dirty is for local validation only")
    repository = os.environ.get("GITHUB_REPOSITORY", "idy/" + project)
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("Invalid release repository")
    return {"schema_version": 1, "project": project, "version": version,
            "tag": "v" + version, "commit": commit, "repository": repository, "dirty": dirty}


def expected_assets(project, version):
    names = {f"{project}_{version}_{goos}_{arch}.{'zip' if goos == 'windows' else 'tar.gz'}"
             for goos, arch in PLATFORMS}
    names |= {f"{project}_{version}_linux_{arch}.deb" for arch in ("amd64", "arm64")}
    if project == "tailsplit":
        names |= {f"TailSplit_{version}_darwin_{arch}.{suffix}"
                  for arch in ("amd64", "arm64") for suffix in ("zip", "dmg", "receipt.json")}
    return names


def archive(path, files, epoch):
    if path.exists():
        raise ValueError("Refusing to replace an existing release archive")
    if path.suffix == ".zip":
        timestamp = time.gmtime(max(epoch, 315532800))[:6]
        with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as bundle:
            for name, source in files.items():
                info = zipfile.ZipInfo(name, timestamp)
                info.create_system = 3
                info.compress_type = zipfile.ZIP_DEFLATED
                info.external_attr = (0o100755 if name.endswith(".exe") else 0o100644) << 16
                bundle.writestr(info, source.read_bytes())
        return
    with path.open("wb") as output, gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=epoch) as zipped:
        with tarfile.open(fileobj=zipped, mode="w") as bundle:
            for name, source in files.items():
                data = source.read_bytes()
                info = tarfile.TarInfo(name)
                info.size, info.mtime = len(data), epoch
                info.mode = 0o755 if name in ("tailrelay", "tailsplit") else 0o644
                info.uid = info.gid = 0
                info.uname = info.gname = "root"
                bundle.addfile(info, io.BytesIO(data))


def build(args):
    identity = metadata(args.tag, args.allow_dirty)
    project, version = identity["project"], identity["version"]
    output = args.directory.resolve()
    output.mkdir(parents=True, exist_ok=True)
    work = ROOT / "bin/release/work" / (args.os + "-" + args.arch)
    work.mkdir(parents=True, exist_ok=True)
    environment = {key: value for key, value in os.environ.items()
                   if not key.startswith("SOPS_AGE_")}
    environment.update(GOWORK="off", GOOS=args.os, GOARCH=args.arch,
                       CGO_ENABLED="1" if args.os == "darwin" else "0")
    if args.arch == "amd64":
        environment["GOAMD64"] = "v1"
    else:
        environment["GOARM64"] = "v8.0"
    environment["RELEASE_SOURCE_COMMIT"] = identity["commit"]
    notices = work / "THIRD_PARTY_NOTICES.md"
    subprocess.run([sys.executable, "-I", "-B", str(ROOT / "packaging/licenses.py"),
                    "--os", args.os, "--arch", args.arch, "--output", str(notices)],
                   cwd=ROOT, env=environment, check=True)
    binary_name = project + (".exe" if args.os == "windows" else "")
    binary = work / binary_name
    flags = f"-s -w -X main.version={version} -X main.commit={identity['commit']}"
    subprocess.run([environment.get("GO", "go"), "build", "-mod=readonly", "-tags", "with_gvisor",
                    "-trimpath", "-buildvcs=false", "-ldflags", flags, "-o", str(binary), "./cmd/" + project],
                   cwd=ROOT, env=environment, check=True)
    detail = dict(identity, os=args.os, arch=args.arch)
    build_info = work / "build.json"
    build_info.write_text(json.dumps(detail, indent=2, sort_keys=True) + "\n")
    extension = "zip" if args.os == "windows" else "tar.gz"
    package = output / f"{project}_{version}_{args.os}_{args.arch}.{extension}"
    archive(package, {binary_name: binary, "LICENSE": ROOT / "LICENSE",
                      "THIRD_PARTY_NOTICES.md": notices, "build.json": build_info},
            int(run("git", "show", "-s", "--format=%ct", "HEAD")))
    files = [package]
    if args.os == "linux":
        subprocess.run(["make", "deb", "GO=" + environment.get("GO", "go"), "PYTHON=" + sys.executable],
                       cwd=ROOT, env=environment, check=True)
        source = ROOT / "bin/deb" / args.arch / f"{project}_{version}_{args.arch}.deb"
        target = output / f"{project}_{version}_linux_{args.arch}.deb"
        shutil.copyfile(source, target)
        files.append(target)
    if project == "tailsplit" and args.os == "darwin":
        native = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine())
        if sys.platform != "darwin" or native != args.arch:
            raise ValueError("macOS app packaging requires a matching native runner")
        app_output = work / "app"
        app_environment = dict(environment, THIRD_PARTY_NOTICES_FILE=str(notices),
                               VERSION=version, COMMIT=identity["commit"])
        command = ["make", "OUT=" + str(app_output), "PYTHON=" + sys.executable, "app"]
        signed = os.environ.get("TAILSPLIT_SIGNED_RELEASE") == "1"
        if signed:
            app_environment.update({key: value for key, value in os.environ.items()
                                    if key.startswith("SOPS_AGE_")})
            command = [sys.executable, "-I", "-B", "scripts/with-signing-credentials.py",
                       "--credentials", os.environ["TAILSPLIT_SIGNING_CREDENTIALS"],
                       "--notary-credentials", os.environ["TAILSPLIT_NOTARY_CREDENTIALS"],
                       "--", "make", "OUT=" + str(app_output), "PYTHON=" + sys.executable, "dist"]
        subprocess.run(command, cwd=ROOT, env=app_environment, check=True)
        receipts = []
        for path in app_output.glob("TailSplit-*.json"):
            data = json.loads(path.read_text())
            if data.get("version") == version and "artifacts" in data:
                receipts.append(data)
        if len(receipts) != 1:
            raise ValueError("Expected one verified macOS distribution receipt")
        receipt = receipts[0]
        if receipt.get("signing_mode") not in ("adhoc", "developer-id"):
            raise ValueError("Unknown app signing status")
        if signed and (receipt["signing_mode"] != "developer-id" or receipt.get("notarized") is not True):
            raise ValueError("The signed release must be Developer ID signed and notarized")
        app_assets = {}
        for suffix in ("zip", "dmg"):
            candidates = list(app_output.glob("TailSplit-*." + suffix))
            if len(candidates) != 1:
                raise ValueError("Expected exactly one macOS " + suffix)
            source = candidates[0]
            proof = receipt["artifacts"].get(source.name, {})
            if proof.get("sha256") != digest(source) or proof.get("size") != source.stat().st_size:
                raise ValueError("macOS receipt does not match distribution bytes")
            target = output / f"TailSplit_{version}_darwin_{args.arch}.{suffix}"
            shutil.copyfile(source, target)
            files.append(target)
            app_assets[target.name] = {"sha256": digest(target), "size": target.stat().st_size}
        normalized = output / f"TailSplit_{version}_darwin_{args.arch}.receipt.json"
        normalized.write_text(json.dumps(dict(detail, build=receipt["build"],
                                             signing_mode=receipt["signing_mode"], team_id=receipt.get("team_id"),
                                             notarized=receipt.get("notarized", False),
                                             helper_sha256=receipt["helper_sha256"], checks=receipt["checks"],
                                             artifacts=app_assets),
                                         indent=2, sort_keys=True) + "\n")
        files.append(normalized)
    fragment = dict(detail, assets={path.name: {"sha256": digest(path), "size": path.stat().st_size}
                                   for path in files})
    (output / f"release-metadata-{args.os}-{args.arch}.json").write_text(
        json.dumps(fragment, indent=2, sort_keys=True) + "\n")
    print("Built", identity["tag"], args.os, args.arch, "assets:", len(files))


def finalize(directory, tag):
    identity = metadata(tag)
    fragments = [json.loads(path.read_text()) for path in sorted(directory.glob("release-metadata-*.json"))]
    if {(item["os"], item["arch"]) for item in fragments} != PLATFORMS or len(fragments) != len(PLATFORMS):
        raise ValueError("A complete Linux/macOS/Windows amd64/arm64 build is required")
    assets = {}
    for item in fragments:
        if any(item.get(key) != value for key, value in identity.items()):
            raise ValueError("Platform artifacts differ from the release source")
        for name, proof in item["assets"].items():
            if name in assets or not re.fullmatch(r"[A-Za-z0-9_.-]+", name):
                raise ValueError("Duplicate or invalid release asset")
            path = directory / name
            if path.is_symlink() or not path.is_file() or path.stat().st_size != proof["size"] or digest(path) != proof["sha256"]:
                raise ValueError("Release artifact hash or size differs: " + name)
            assets[name] = proof
    if set(assets) != expected_assets(identity["project"], identity["version"]):
        raise ValueError("Release assets do not match the complete expected inventory")
    manifest = dict(identity, assets=assets)
    (directory / "release-manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    names = sorted(assets) + ["release-manifest.json"]
    (directory / "SHA256SUMS").write_text("".join(f"{digest(directory / name)}  {name}\n" for name in names))
    return manifest


def api(path, method="GET", data=None, input_file=None, accept=None, output=None):
    arguments = ["gh", "api", path, "--method", method, "-H", "X-GitHub-Api-Version: 2022-11-28"]
    if accept:
        arguments += ["-H", "Accept: " + accept]
    if data is not None:
        arguments += ["--input", "-"]
    if input_file:
        arguments += ["--input", str(input_file), "-H", "Content-Type: application/octet-stream"]
    result = subprocess.run(arguments, input=None if data is None else json.dumps(data).encode(),
                            stdout=output if output else subprocess.PIPE, stderr=subprocess.PIPE, check=True)
    return None if output else json.loads(result.stdout)


def find_release(repository, tag):
    page = 1
    while True:
        releases = api(f"repos/{repository}/releases?per_page=100&page={page}")
        matches = [release for release in releases if release["tag_name"] == tag]
        if len(matches) > 1:
            raise ValueError("Ambiguous release tag")
        if matches:
            return matches[0]
        if len(releases) < 100:
            return None
        page += 1


def publish(directory, tag):
    manifest = finalize(directory, tag)
    repository, commit = manifest["repository"], manifest["commit"]
    if api(f"repos/{repository}/commits/{quote(tag, safe='')}")["sha"] != commit:
        raise ValueError("Remote tag does not match the release source")
    marker = f"<!-- {manifest['project']}-release commit={commit} -->"
    release = find_release(repository, tag)
    if release is None:
        body = (f"Multi-platform {manifest['project']} {manifest['version']} from `{commit}`.\n\n"
                "Linux, macOS and Windows CLI archives support amd64 and arm64. Linux Debian packages are included.\n"
                "Verify SHA256SUMS before installation. Each archive includes its license and dependency notices.\n")
        if manifest["project"] == "tailsplit":
            body += "\nmacOS app ZIP/DMG signing and notarization status is recorded in each receipt JSON.\n"
        body += "\n" + marker
        release = api(f"repos/{repository}/releases", "POST", {
            "tag_name": tag, "target_commitish": commit, "draft": True, "prerelease": False,
            "name": manifest["project"] + " " + tag, "body": body, "make_latest": "false"})
    if marker not in (release.get("body") or "") or release["tag_name"] != tag:
        raise ValueError("Existing release is not owned by this source commit")
    selected = set(manifest["assets"]) | {"release-manifest.json", "SHA256SUMS"}
    existing = {asset["name"]: asset for asset in release["assets"]}
    if set(existing) - selected:
        raise ValueError("Existing release has unexpected assets")
    if not release["draft"] and set(existing) != selected:
        raise ValueError("A published release cannot be filled or overwritten")
    upload_url = release["upload_url"].split("{", 1)[0]
    if urlparse(upload_url).netloc != "uploads.github.com":
        raise ValueError("Unexpected GitHub upload host")
    for name in sorted(selected - set(existing)):
        api(upload_url + "?name=" + quote(name, safe=""), "POST", input_file=directory / name)
    release = api(f"repos/{repository}/releases/{release['id']}")
    if {asset["name"] for asset in release["assets"]} != selected:
        raise ValueError("Uploaded release inventory is incomplete")
    with tempfile.TemporaryDirectory(prefix="release-verify-") as temporary:
        for asset in release["assets"]:
            folder = Path(temporary) / asset["name"]
            folder.mkdir()
            downloaded = folder / "asset"
            with downloaded.open("wb") as output:
                api(f"repos/{repository}/releases/assets/{asset['id']}", accept="application/octet-stream", output=output)
            if digest(downloaded) != digest(directory / asset["name"]):
                raise ValueError("Existing or uploaded release bytes differ: " + asset["name"])
    if release["draft"]:
        release = api(f"repos/{repository}/releases/{release['id']}", "PATCH", {"draft": False, "make_latest": "true"})
    print("Published and verified:", release["html_url"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    builder = commands.add_parser("build")
    builder.add_argument("--os", choices=("linux", "darwin", "windows"), required=True)
    builder.add_argument("--arch", choices=("amd64", "arm64"), required=True)
    builder.add_argument("--allow-dirty", action="store_true")
    for name in ("build", "finalize", "publish"):
        target = builder if name == "build" else commands.add_parser(name)
        target.add_argument("--directory", type=Path, default=ROOT / "bin/release/assets")
        target.add_argument("--tag", required=name != "build")
    args = parser.parse_args()
    if args.command == "build":
        build(args)
    elif args.command == "finalize":
        finalize(args.directory, args.tag)
    else:
        publish(args.directory, args.tag)


if __name__ == "__main__":
    main()
