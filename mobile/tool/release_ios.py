#!/usr/bin/env python3
"""Sign a transferred unsigned archive on a fresh runner, verify it, and optionally upload."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import secrets
import shutil
import stat
import subprocess
import sys
import tempfile
import unicodedata
from xml.parsers.expat import ExpatError
import zipfile

MOBILE = Path(__file__).resolve().parents[1]
TEAM = "BGL4WMWD3W"
AGE_SECRET = "TAILSPLIT_MOBILE_RELEASE_AGE_KEY"
CREDENTIALS_FILE = MOBILE / "credentials/apple-distribution.json.sops"


# Tools come from the fresh signing venv or macOS, never the artifact or checkout.
TOOLS = {name: f"/usr/bin/{name}" for name in ("security", "codesign", "xcodebuild")}
TOOLS.update({name: str(Path(sys.executable).parent / name) for name in ("keychain", "app-store-connect", "sops")})
TOOLS["python3"] = sys.executable
MAX_ZIP_BYTES = 8 * 1024 ** 3
CORE_RECEIPT = "native-core-receipt.json"
CAPABILITIES = "sing_box_flutter_capabilities.bundle/core-capabilities.json"
CAPABILITY_RESOURCES = (CAPABILITIES, f"PlugIns/PacketTunnel.appex/{CAPABILITIES}")


def core_receipt(path):
    """Read only the receipt's identity and Apple digest; never follow its paths."""
    try:
        receipt = json.loads(path.read_bytes())
        digest = receipt["artifacts"]["apple"]["capabilities_sha256"]
        if (receipt["schema"] != 1 or not isinstance(receipt["version"], str) or not receipt["version"]
                or not re.fullmatch(r"[0-9a-f]{64}", receipt["fingerprint"])
                or not isinstance(receipt["features"], dict)
                or not re.fullmatch(r"[0-9a-f]{64}", digest)):
            raise ValueError()
    except (OSError, ValueError, KeyError, TypeError):
        raise ValueError("Missing or invalid Apple native core receipt") from None
    return receipt


def verify_capabilities(read_resource, receipt):
    """Require both runtime lookup paths to contain the exact receipt manifest."""
    expected = receipt["artifacts"]["apple"]["capabilities_sha256"]
    hashes = {}
    for relative in CAPABILITY_RESOURCES:
        try:
            data = read_resource(relative)
        except (OSError, KeyError):
            raise ValueError(f"Missing packaged core capabilities: {relative}") from None
        digest = hashlib.sha256(data).hexdigest()
        if digest != expected:
            raise ValueError(f"Packaged core capabilities SHA-256 differs from receipt: {relative}")
        try:
            manifest = json.loads(data)
            if not isinstance(manifest, dict) or any(
                    manifest.get(key) != receipt[key] for key in ("schema", "version", "fingerprint", "features")):
                raise ValueError()
        except (ValueError, KeyError, TypeError):
            raise ValueError(f"Packaged core capabilities identity differs from receipt: {relative}") from None
        hashes[relative] = digest
    return {"capabilities_sha256": expected, "resources": hashes,
            "version": receipt["version"], "fingerprint": receipt["fingerprint"]}


def verify_packaged_capabilities(package, receipt):
    """Shared unsigned archive / IPA gate. IPA reads fixed paths without extracting."""
    if package.suffix == ".xcarchive":
        app = package / "Products/Applications/Runner.app"
        return verify_capabilities(lambda relative: (app / relative).read_bytes(), receipt)
    if package.suffix == ".ipa":
        with zipfile.ZipFile(package) as archive:
            # Reject duplicate runtime resources rather than let ZIP ordering choose.
            for relative in CAPABILITY_RESOURCES:
                if archive.namelist().count(f"Payload/Runner.app/{relative}") > 1:
                    raise ValueError("Duplicate packaged core capabilities")
            return verify_capabilities(lambda relative: archive.read(f"Payload/Runner.app/{relative}"), receipt)
    raise ValueError("Capabilities verification requires an .xcarchive or .ipa")


def sanitized_env():
    """Limit incidental credential exposure; separate runners provide isolation."""
    env = {key: os.environ[key] for key in ("HOME", "RUNNER_TEMP", "TMPDIR", "LANG", "DEVELOPER_DIR")
           if key in os.environ}
    env["PATH"] = f"{Path(sys.executable).parent}:/usr/bin:/bin:/usr/sbin:/sbin"
    return env


def run(*args, env=None, capture=False, check=True):
    return subprocess.run((TOOLS[args[0]], *args[1:]), cwd=os.environ["RUNNER_TEMP"],
                          env=sanitized_env() if env is None else env, check=check,
                          stdout=subprocess.PIPE if capture else None,
                          stderr=subprocess.PIPE if capture else None).stdout


def extract_zip(source, destination, allowed_roots):
    """Copy regular ZIP members into a NEW directory, without honoring links or metadata.

    Validate the entire manifest first, including case/Unicode aliases on macOS.
    No extractall/ditto/unzip, executable hooks, special files, or outside paths.
    Apply this to both the outer Actions ZIP and the inner archive (and final IPA).
    """
    if not stat.S_ISREG(source.lstat().st_mode):
        raise ValueError("Expected a regular ZIP file")
    with zipfile.ZipFile(source) as archive:
        members = archive.infolist()
        if len(members) > 100_000 or sum(item.file_size for item in members) > MAX_ZIP_BYTES:
            raise ValueError("ZIP exceeds release size limits")
        seen, aliases, kinds = set(), {}, {}
        for item in members:
            name = item.filename.rstrip("/") if item.is_dir() else item.filename
            parts = name.split("/")
            mode = item.external_attr >> 16
            kind = stat.S_IFMT(mode)
            if (item.orig_filename != item.filename or not name
                    or item.filename != name + ("/" if item.is_dir() else "")
                    or any(part in ("", ".", "..") for part in parts)
                    or any(c in name for c in ("\\", ":"))
                    or any(ord(c) < 32 or ord(c) == 127 for c in name)
                    or parts[0] not in allowed_roots or name in seen
                    or item.flag_bits & 1
                    or kind not in (0, stat.S_IFDIR if item.is_dir() else stat.S_IFREG)):
                raise ValueError("Unsafe or duplicate ZIP member")
            seen.add(name)
            for index in range(1, len(parts) + 1):
                prefix = "/".join(parts[:index])
                canonical = unicodedata.normalize("NFD", prefix).casefold()
                is_directory = index < len(parts) or item.is_dir()
                if aliases.setdefault(canonical, prefix) != prefix or kinds.setdefault(prefix, is_directory) != is_directory:
                    raise ValueError("Aliased or conflicting ZIP paths")
        destination.mkdir(mode=0o700)  # Must not already exist, even as a symlink.
        for item in members:
            target = destination.joinpath(*item.filename.rstrip("/").split("/"))
            if item.is_dir():
                target.mkdir(mode=0o700, parents=True, exist_ok=True)
            else:
                target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                with archive.open(item) as incoming, target.open("xb") as outgoing:
                    shutil.copyfileobj(incoming, outgoing)
                target.chmod(0o700 if (item.external_attr >> 16) & 0o111 else 0o600)


def validate_executable(path, info):
    executable = info.get("CFBundleExecutable", "")
    if not isinstance(executable, str) or not re.fullmatch(r"[A-Za-z0-9_.-]+", executable) or executable in (".", ".."):
        raise ValueError("Unsafe bundle executable path")
    with (path / executable).open("rb") as binary:
        if binary.read(4) not in (b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf", b"\xca\xfe\xba\xbe", b"\xbe\xba\xfe\xca"):
            raise ValueError("Expected an iOS Mach-O executable")


def bundle_info(path, bundle_id, build_number):
    info = plistlib.loads((path / "Info.plist").read_bytes())
    if info.get("CFBundleIdentifier") != bundle_id or info.get("CFBundleVersion") != build_number:
        raise ValueError("Archive bundle identity or build number does not match this release")
    version = info.get("CFBundleShortVersionString", "")
    if not isinstance(version, str) or not re.fullmatch(r"[0-9]+(?:\.[0-9]+){0,2}", version):
        raise ValueError("Invalid archive application version")
    validate_executable(path, info)
    return info


def prepare_archive(artifact, work, config, build_number):
    transfer = work / "transfer"
    extract_zip(artifact, transfer, {"unsigned.xcarchive.zip", CORE_RECEIPT})
    if sorted(path.name for path in transfer.iterdir()) != sorted(["unsigned.xcarchive.zip", CORE_RECEIPT]):
        raise ValueError("Expected exactly one transferred archive ZIP and native core receipt")
    receipt = core_receipt(transfer / CORE_RECEIPT)
    unpacked = work / "unsigned"
    extract_zip(transfer / "unsigned.xcarchive.zip", unpacked, {"Runner.xcarchive"})
    archive = unpacked / "Runner.xcarchive"
    if not archive.is_dir() or {p.name for p in archive.iterdir()} - {"Info.plist", "Products", "dSYMs", "BCSymbolMaps"}:
        raise ValueError("Unexpected archive contents")
    # Do not let archive metadata choose external products, scripts or export paths.
    products = archive / "Products"
    apps = products / "Applications"
    if {p.name for p in products.iterdir()} != {"Applications"} or {p.name for p in apps.iterdir()} != {"Runner.app"}:
        raise ValueError("Expected only the Runner archive product")
    app = apps / "Runner.app"
    info = bundle_info(app, config["bundle_id"], build_number)
    plugins = app / "PlugIns"
    if {p.name for p in plugins.iterdir()} != {"PacketTunnel.appex"}:
        raise ValueError("Expected the single PacketTunnel extension")
    extension = plugins / "PacketTunnel.appex"
    bundle_info(extension, config["packet_tunnel_bundle_id"], build_number)
    for path in app.rglob("*"):
        if path.suffix in (".app", ".appex") and path != extension:
            raise ValueError("Unexpected nested application or extension")
        if path.is_dir() and path.suffix in (".framework", ".bundle"):
            embedded_info = plistlib.loads((path / "Info.plist").read_bytes())
            if "CFBundleExecutable" in embedded_info:
                validate_executable(path, embedded_info)
            elif path.suffix == ".framework":
                raise ValueError("Framework is missing its executable")
    verify_packaged_capabilities(archive, receipt)
    # Reconstruct rather than trust ApplicationPath, IconPaths, SchemeName, etc.
    metadata = {
        "ArchiveVersion": 2, "Name": "Runner", "SchemeName": "Runner",
        "CreationDate": dt.datetime.now(dt.timezone.utc).replace(tzinfo=None),
        "ApplicationProperties": {
            "ApplicationPath": "Applications/Runner.app",
            "Architectures": ["arm64"],
            "CFBundleIdentifier": config["bundle_id"],
            "CFBundleShortVersionString": info["CFBundleShortVersionString"],
            "CFBundleVersion": build_number,
        },
    }
    (archive / "Info.plist").write_bytes(plistlib.dumps(metadata))
    return archive


def identity(channel="app-store"):
    if channel not in ("beta", "app-store"):
        raise ValueError("Choose the beta or app-store release channel")
    value = json.loads((MOBILE / "release-identity.json").read_text())
    common = {"team_id": TEAM, "organization": "TERMITOC PTE. LTD.", "account_type": "organization"}
    bundle = "dev.idy.tailsplit.beta" if channel == "beta" else "dev.idy.tailsplit"
    expected = {"name": "TailSplit Beta" if channel == "beta" else "TailSplit",
                "bundle_id": bundle, "packet_tunnel_bundle_id": f"{bundle}.PacketTunnel",
                "app_group": f"group.{bundle}", "app_store": channel == "app-store"}
    delivery = {
        "beta": ("6833766631", "4be1c696-606b-4ae9-b507-9a385956c6ca"),
        "app-store": ("6833766298", "ee8bc770-f372-4c74-8700-f7035c844fc4"),
    }
    expected.update(app_store_id=delivery[channel][0], testflight_group_id=delivery[channel][1],
                    testflight_group="Internal Testing")
    selected = value.get("channels", {}).get(channel, {})
    if (any(value.get(key) != item for key, item in common.items())
            or any(selected.get(key) != item for key, item in expected.items())
            or selected.get("app_store") is not expected["app_store"]):
        raise ValueError("iOS publication requires the exact Termitoc TailSplit channel identity")
    return {**common, **selected}


def credentials(raw):
    try:
        value = json.loads(raw)
    except (TypeError, json.JSONDecodeError):
        raise ValueError("SOPS must decrypt a valid Apple credential JSON object") from None
    fields = ("issuer_id", "api_key_id", "api_private_key", "certificate_private_key")
    if not isinstance(value, dict) or any(not isinstance(value.get(k), str) or not value[k].strip() for k in fields):
        raise ValueError(f"Apple credentials require: {', '.join(fields)}")
    if value.get("team_id", TEAM) != TEAM:
        raise ValueError("The supplied Apple credentials identify a different team")
    return {key: value[key] for key in fields}


def decrypt_credentials():
    """Only SOPS receives the CI age key; plaintext stays in this process's memory."""
    lines = [line.strip() for line in os.environ.get(AGE_SECRET, "").splitlines()
             if line.strip() and not line.lstrip().startswith("#")]
    if len(lines) != 1 or not lines[0].startswith("AGE-SECRET-KEY-"):
        raise ValueError(f"Configure {AGE_SECRET} in the mobile-release environment")
    key = lines[0]
    raw = run("sops", "--decrypt", "--input-type", "json", "--output-type", "json", str(CREDENTIALS_FILE),
              env={**sanitized_env(), "SOPS_AGE_KEY": key}, capture=True)
    return credentials(raw)


def validate_entitlements(entitlements, bundle_id, app_group, source):
    if not isinstance(entitlements, dict):
        raise ValueError(f"{source} must contain an entitlement dictionary")
    if entitlements.get("com.apple.developer.team-identifier") != TEAM:
        raise ValueError(f"{source} has a different Apple team")
    if entitlements.get("application-identifier") != f"{TEAM}.{bundle_id}":
        raise ValueError(f"{source} does not match the exact application identifier")
    # Distribution profiles and signatures may omit this development-only entitlement.
    if entitlements.get("get-task-allow", False) is not False:
        raise ValueError(f"{source} must not allow debugging")
    groups = entitlements.get("com.apple.security.application-groups", [])
    if not isinstance(groups, list) or app_group not in groups:
        raise ValueError(f"{source} is missing the shared App Group")
    extensions = entitlements.get("com.apple.developer.networking.networkextension", [])
    if not isinstance(extensions, list) or "packet-tunnel-provider" not in extensions:
        raise ValueError(f"{source} is missing the Packet Tunnel capability")


def validate_profile(profile, bundle_id, app_group):
    """Reject personal-team, development, wildcard and incomplete VPN profiles."""
    if profile.get("TeamIdentifier") != [TEAM]:
        raise ValueError("Provisioning profile belongs to a different Apple team")
    validate_entitlements(profile.get("Entitlements", {}), bundle_id, app_group, "Provisioning profile")
    if profile.get("ProvisionedDevices") or profile.get("ProvisionsAllDevices"):
        raise ValueError("An App Store distribution profile is required")
    expiry = profile.get("ExpirationDate")
    if not isinstance(expiry, dt.datetime) or expiry.replace(tzinfo=dt.timezone.utc) <= dt.datetime.now(dt.timezone.utc):
        raise ValueError("Provisioning profile is expired or has no expiry")


def decode_profile(path):
    return plistlib.loads(run("security", "cms", "-D", "-i", str(path), capture=True))


def verify_bundle(path, bundle_id, app_group):
    info = plistlib.loads((path / "Info.plist").read_bytes())
    if info.get("CFBundleIdentifier") != bundle_id:
        raise ValueError("The exported app has an unexpected bundle identifier")
    validate_profile(decode_profile(path / "embedded.mobileprovision"), bundle_id, app_group)
    run("codesign", "--verify", "--deep", "--strict", str(path))
    # codesign writes metadata to stderr and the requested XML entitlements to stdout.
    signed = subprocess.run([TOOLS["codesign"], "-d", "--verbose=4", "--entitlements", "-", "--xml", str(path)],
                            cwd=os.environ["RUNNER_TEMP"], check=True, capture_output=True, env=sanitized_env())
    if f"TeamIdentifier={TEAM}".encode() not in signed.stderr.splitlines():
        raise ValueError("The exported app was not signed by Termitoc")
    try:
        entitlements = plistlib.loads(signed.stdout)
    except (ValueError, ExpatError):
        raise ValueError("The exported app has missing or invalid signed entitlements") from None
    validate_entitlements(entitlements, bundle_id, app_group, "Signed entitlements")


def sign_archive(archive, config, selected, keychain, work):
    """Give an unsigned archive explicit entitlements before Xcode re-exports it.

    Exporting a never-signed archive alone can omit its App Group/VPN grants.
    Sign nested code first, then the extension and host with identities derived
    only from the pinned channel and validated distribution profiles.
    """
    app = archive / "Products/Applications/Runner.app"
    extension = app / "PlugIns/PacketTunnel.appex"
    signing = ("codesign", "--force", "--sign", "Apple Distribution", "--keychain", str(keychain))
    nested = []
    for path in app.rglob("*"):
        if path.is_dir() and path.suffix == ".framework":
            nested.append(path)
        elif path.is_dir() and path.suffix == ".bundle":
            info = plistlib.loads((path / "Info.plist").read_bytes())
            if "CFBundleExecutable" in info:
                nested.append(path)
        elif path.is_file() and path.suffix == ".dylib":
            with path.open("rb") as binary:
                if binary.read(4) not in (b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf", b"\xca\xfe\xba\xbe", b"\xbe\xba\xfe\xca"):
                    raise ValueError("Expected a Mach-O dynamic library")
            nested.append(path)
    for path in sorted(nested, key=lambda item: len(item.parts), reverse=True):
        run(*signing, str(path))
    for path, bundle in ((extension, config["packet_tunnel_bundle_id"]), (app, config["bundle_id"])):
        profile_path = selected[bundle][1]
        profile = decode_profile(profile_path)
        entitlements = {
            "application-identifier": f"{TEAM}.{bundle}",
            "com.apple.developer.team-identifier": TEAM,
            "com.apple.security.application-groups": [config["app_group"]],
            "com.apple.developer.networking.networkextension": ["packet-tunnel-provider"],
            "get-task-allow": False,
        }
        if profile["Entitlements"].get("beta-reports-active") is True:
            entitlements["beta-reports-active"] = True
        validate_entitlements(entitlements, bundle, config["app_group"], "Archive signing entitlements")
        entitlement_path = work / f"{bundle}.entitlements.plist"
        entitlement_path.write_bytes(plistlib.dumps(entitlements))
        shutil.copyfile(profile_path, path / "embedded.mobileprovision")
        run(*signing, "--generate-entitlement-der", "--entitlements", str(entitlement_path), str(path))


def release(artifact, output, publish=False, channel="app-store", submit_for_review=False,
            build_number=None, resume_upload=False):
    config = identity(channel)
    if submit_for_review and (not publish or config["app_store"] is not True):
        raise ValueError("App Store review requires --publish and the app-store channel; beta is TestFlight-only")
    if resume_upload and not publish:
        raise ValueError("Upload recovery requires --publish")
    if build_number is None:
        build_number = os.environ.get("GITHUB_RUN_NUMBER", "")
    if not isinstance(build_number, str) or not re.fullmatch(r"[1-9][0-9]*", build_number):
        raise ValueError("A positive GitHub Actions build number is required")
    bundles = (config["bundle_id"], config["packet_tunnel_bundle_id"])
    with tempfile.TemporaryDirectory(prefix="tailsplit-ios-signing-", dir=os.environ["RUNNER_TEMP"]) as folder:
        work = Path(folder)
        archive = prepare_archive(artifact, work, config, build_number)
        receipt = core_receipt(work / "transfer" / CORE_RECEIPT)
        archive_capabilities = verify_packaged_capabilities(archive, receipt)
        # Reject invalid archives before any tool receives a decryption/signing key.
        values = decrypt_credentials()
        env = sanitized_env()
        api_env = {
            **env,
            "APP_STORE_CONNECT_ISSUER_ID": values["issuer_id"],
            "APP_STORE_CONNECT_KEY_IDENTIFIER": values["api_key_id"],
            "APP_STORE_CONNECT_PRIVATE_KEY": values["api_private_key"],
            "APP_STORE_CONNECT_DISABLE_JWT_CACHE": "true",
        }
        signing_env = {**api_env, "CERTIFICATE_PRIVATE_KEY": values["certificate_private_key"]}
        keychain_env = {**env, "KEYCHAIN_PASSWORD": secrets.token_urlsafe(32)}
        if os.environ.get("GITHUB_ACTIONS") == "true":
            for value in [*values.values(), keychain_env["KEYCHAIN_PASSWORD"]]:
                escaped = value.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
                print(f"::add-mask::{escaped}", flush=True)
        # Output is outside the artifact and must be new. Only a verified IPA is copied here.
        output.mkdir(mode=0o700)
        keychain = work / "release.keychain-db"
        profiles = work / "profiles"
        certificates = work / "certificates"
        profiles.mkdir(mode=0o700)
        certificates.mkdir(mode=0o700)
        previous = run("security", "default-keychain", "-d", "user", capture=True).decode().strip().strip('"')
        installed_profiles = []
        try:
            # Only trusted signing/export tools run on this VM; compilation already
            # finished on another VM that never had these credentials or keychain.
            run("keychain", "initialize", "--path", str(keychain), "--password", "@env:KEYCHAIN_PASSWORD", env=keychain_env)
            for bundle in bundles:
                run("app-store-connect", "fetch-signing-files", bundle,
                    "--strict-match-identifier", "--platform", "IOS", "--type", "IOS_APP_STORE",
                    "--certificates-dir", str(certificates), "--profiles-dir", str(profiles),
                    "--certificate-key", "@env:CERTIFICATE_PRIVATE_KEY", "--create", env=signing_env)
            selected = {}
            for path in sorted(profiles.glob("*.mobileprovision")):
                profile = decode_profile(path)
                application = profile.get("Entitlements", {}).get("application-identifier", "")
                bundle = application.removeprefix(f"{TEAM}.")
                if bundle not in bundles:
                    raise ValueError("Fetched a profile outside the TailSplit application")
                validate_profile(profile, bundle, config["app_group"])
                uuid = profile.get("UUID", "")
                if not isinstance(uuid, str) or not re.fullmatch(r"[A-Fa-f0-9]{8}(?:-[A-Fa-f0-9]{4}){3}-[A-Fa-f0-9]{12}", uuid):
                    raise ValueError("Provisioning profile has an invalid UUID")
                if bundle not in selected:
                    selected[bundle] = (uuid, path)
            if set(selected) != set(bundles) or len({uuid for uuid, _ in selected.values()}) != len(bundles):
                raise ValueError("Both Runner and PacketTunnel need distinct valid Termitoc App Store profiles")
            # Xcode 16+ profile location; do not invoke xcode-project or modify a project.
            profile_directory = Path.home() / "Library/Developer/Xcode/UserData/Provisioning Profiles"
            profile_directory.mkdir(parents=True, exist_ok=True)
            for uuid, path in selected.values():
                target = profile_directory / f"{uuid}.mobileprovision"
                with target.open("xb") as destination:
                    installed_profiles.append(target)
                    destination.write(path.read_bytes())
            run("keychain", "add-certificates", "--path", str(keychain),
                "--certificate", str(certificates / "*.p12"), env=keychain_env)
            sign_archive(archive, config, selected, keychain, work)
            options = work / "ExportOptions.plist"
            options.write_bytes(plistlib.dumps({
                "method": "app-store-connect", "destination": "export", "teamID": TEAM,
                "signingStyle": "manual", "signingCertificate": "Apple Distribution",
                "provisioningProfiles": {bundle: uuid for bundle, (uuid, _) in selected.items()},
                "manageAppVersionAndBuildNumber": False,
            }))
            export = work / "ipa"
            run("xcodebuild", "-exportArchive", "-archivePath", str(archive),
                "-exportPath", str(export), "-exportOptionsPlist", str(options), env=env)
            packages = list(export.glob("*.ipa"))
            if len(packages) != 1:
                raise ValueError("Expected exactly one exported IPA")
            extracted = work / "exported"
            extract_zip(packages[0], extracted, {"Payload", "SwiftSupport", "Symbols", "BCSymbolMaps", "iTunesMetadata.plist"})
            apps = list((extracted / "Payload").glob("*.app"))
            if len(apps) != 1:
                raise ValueError("Expected one application in the exported IPA")
            app = apps[0]
            verify_bundle(app, bundles[0], config["app_group"])
            extensions = list((app / "PlugIns").glob("*.appex"))
            if len(extensions) != 1:
                raise ValueError("Expected the single PacketTunnel extension")
            verify_bundle(extensions[0], bundles[1], config["app_group"])
            ipa_capabilities = verify_capabilities(lambda relative: (app / relative).read_bytes(), receipt)
            print(f"Verified IPA: {config['organization']} ({TEAM}), {channel}, Runner and PacketTunnel", flush=True)
            verified = output / "TailSplit.ipa"
            shutil.copyfile(packages[0], verified)
            (output / CORE_RECEIPT).write_text(json.dumps(receipt, indent=2) + "\n")
            (output / "core-capabilities-verification.json").write_text(json.dumps({
                "archive": archive_capabilities, "ipa": ipa_capabilities,
            }, indent=2) + "\n")
            if publish:
                metadata = plistlib.loads((archive / "Info.plist").read_bytes())["ApplicationProperties"]
                review_args = ("--submit-for-review",) if submit_for_review else ()
                recovery_args = ("--resume-upload",) if resume_upload else ()
                run("python3", "-I", "-B", str(MOBILE / "tool/publish_testflight.py"),
                    "--ipa", str(verified), "--channel", channel, "--build-number", build_number,
                    "--version", metadata["CFBundleShortVersionString"],
                    "--receipt", str(output / "testflight.json"), *review_args, *recovery_args, env=api_env)
        finally:
            for path in installed_profiles:
                path.unlink(missing_ok=True)
            if previous:
                run("security", "default-keychain", "-d", "user", "-s", previous, check=False, env=env)
            if keychain.exists():
                run("keychain", "delete", "--path", str(keychain), check=False, env=env)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--artifact", type=Path, help="Unextracted Actions artifact ZIP")
    source.add_argument("--verify-capabilities", type=Path, help="Check an unsigned .xcarchive or .ipa without signing or uploading")
    parser.add_argument("--core-receipt", type=Path, help="Native core receipt for standalone capabilities verification")
    parser.add_argument("--capabilities-report", type=Path, help="Write a standalone capabilities verification JSON report")
    parser.add_argument("--output", type=Path, help="New directory for the verified IPA")
    parser.add_argument("--channel", choices=("beta", "app-store"), help="Exact application identity to sign")
    parser.add_argument("--build-number", help="Expected archive build number (defaults to this Actions run)")
    parser.add_argument("--publish", action="store_true", help="Upload and verify distribution to the channel's internal TestFlight group")
    parser.add_argument("--submit-for-review", action="store_true", help="Submit the app-store channel to App Review (manual release after approval)")
    parser.add_argument("--resume-upload", action="store_true", help="Recover an existing exact TestFlight upload during a trusted archive retry")
    args = parser.parse_args()
    if args.verify_capabilities:
        if not args.core_receipt or args.publish or args.submit_for_review or args.resume_upload or args.output or args.channel:
            parser.error("--verify-capabilities requires --core-receipt and cannot sign or publish")
    elif not args.output or not args.channel or args.core_receipt or args.capabilities_report:
        parser.error("--artifact requires --output and --channel; standalone receipt/report flags require --verify-capabilities")
    try:
        if args.verify_capabilities:
            report = verify_packaged_capabilities(args.verify_capabilities, core_receipt(args.core_receipt))
            encoded = json.dumps(report, indent=2) + "\n"
            if args.capabilities_report:
                args.capabilities_report.write_text(encoded)
            print(encoded, end="")
            return
        release(args.artifact.absolute(), args.output.absolute(), args.publish, args.channel,
                args.submit_for_review, args.build_number, args.resume_upload)
    except (ValueError, KeyError, OSError, zipfile.BadZipFile, ExpatError, subprocess.CalledProcessError) as error:
        # Do not include private configuration payloads or command environments.
        if isinstance(error, ValueError):
            parser.exit(1, f"Release stopped: {error}\n")
        parser.exit(1, "Release stopped: archive validation, signing or publication step failed.\n")


if __name__ == "__main__":
    main()
