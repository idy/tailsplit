#!/usr/bin/env python3
"""Build an App Store IPA and optionally upload it using LetSolo's Termitoc account."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import plistlib
import re
import secrets
import subprocess
import tempfile

MOBILE = Path(__file__).resolve().parents[1]
TEAM = "BGL4WMWD3W"
SECRET = "BGL4WMWD3W_APPLE_CREDENTIALS"


def run(*args, env=None, capture=False):
    return subprocess.run(args, cwd=MOBILE, env=env, check=True,
                          stdout=subprocess.PIPE if capture else None,
                          stderr=subprocess.PIPE if capture else None).stdout


def identity():
    value = json.loads((MOBILE / "release-identity.json").read_text())
    if value["team_id"] != TEAM or value["organization"] != "TERMITOC PTE. LTD.":
        raise ValueError("iOS publication requires the Termitoc organization")
    return value


def credentials(raw):
    try:
        value = json.loads(raw)
    except (TypeError, json.JSONDecodeError):
        raise ValueError(f"Configure {SECRET} with the LetSolo organization credentials") from None
    fields = ("issuer_id", "api_key_id", "api_private_key", "certificate_private_key")
    if not isinstance(value, dict) or any(not isinstance(value.get(k), str) or not value[k].strip() for k in fields):
        raise ValueError(f"{SECRET} requires: {', '.join(fields)}")
    if value.get("team_id", TEAM) != TEAM:
        raise ValueError("The supplied Apple credentials identify a different team")
    return {key: value[key] for key in fields}


def validate_profile(profile, bundle_id, app_group):
    """Reject personal-team, development, wildcard and incomplete VPN profiles."""
    entitlements = profile.get("Entitlements", {})
    if profile.get("TeamIdentifier") != [TEAM]:
        raise ValueError("Provisioning profile belongs to a different Apple team")
    if entitlements.get("com.apple.developer.team-identifier") != TEAM:
        raise ValueError("Provisioning entitlements have a different Apple team")
    if entitlements.get("application-identifier") != f"{TEAM}.{bundle_id}":
        raise ValueError("Provisioning profile does not match the exact application identifier")
    # Distribution profiles may omit this development-only entitlement.
    if entitlements.get("get-task-allow", False) is not False or profile.get("ProvisionedDevices") or profile.get("ProvisionsAllDevices"):
        raise ValueError("An App Store distribution profile is required")
    if app_group not in entitlements.get("com.apple.security.application-groups", []):
        raise ValueError("Provisioning profile is missing the shared App Group")
    if "packet-tunnel-provider" not in entitlements.get("com.apple.developer.networking.networkextension", []):
        raise ValueError("Provisioning profile is missing the Packet Tunnel capability")
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
    # codesign writes its metadata to stderr; never infer the signer from the filename.
    signed = subprocess.run(["codesign", "-d", "--verbose=4", str(path)], check=True,
                            capture_output=True, text=True).stderr
    if f"TeamIdentifier={TEAM}" not in signed.splitlines():
        raise ValueError("The exported app was not signed by Termitoc")


def release(publish):
    config = identity()
    values = credentials(os.environ.get(SECRET))
    env = os.environ.copy()
    env.pop(SECRET, None)
    env.update({
        "APP_STORE_CONNECT_ISSUER_ID": values["issuer_id"],
        "APP_STORE_CONNECT_KEY_IDENTIFIER": values["api_key_id"],
        "APP_STORE_CONNECT_PRIVATE_KEY": values["api_private_key"],
        "APP_STORE_CONNECT_DISABLE_JWT_CACHE": "true",
        "CERTIFICATE_PRIVATE_KEY": values["certificate_private_key"],
        "KEYCHAIN_PASSWORD": secrets.token_urlsafe(32),
    })
    if os.environ.get("GITHUB_ACTIONS") == "true":
        for value in [*values.values(), env["KEYCHAIN_PASSWORD"]]:
            escaped = value.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
            print(f"::add-mask::{escaped}", flush=True)
    build_number = os.environ.get("GITHUB_RUN_NUMBER", "")
    if not re.fullmatch(r"[1-9][0-9]*", build_number):
        raise ValueError("A positive GitHub Actions build number is required")
    bundles = (config["bundle_id"], config["packet_tunnel_bundle_id"])
    with tempfile.TemporaryDirectory(prefix="tailsplit-ios-signing-", dir=os.environ["RUNNER_TEMP"]) as folder:
        work = Path(folder)
        keychain = work / "release.keychain-db"
        profiles = work / "profiles"
        certificates = work / "certificates"
        profiles.mkdir(mode=0o700)
        certificates.mkdir(mode=0o700)
        previous = run("security", "default-keychain", "-d", "user", capture=True).decode().strip().strip('"')
        try:
            run("keychain", "initialize", "--path", str(keychain), "--password", "@env:KEYCHAIN_PASSWORD", env=env)
            for bundle in bundles:
                run("app-store-connect", "fetch-signing-files", bundle,
                    "--strict-match-identifier", "--platform", "IOS", "--type", "IOS_APP_STORE",
                    "--certificates-dir", str(certificates), "--profiles-dir", str(profiles),
                    "--certificate-key", "@env:CERTIFICATE_PRIVATE_KEY", "--create", env=env)
            found = set()
            for path in profiles.glob("*.mobileprovision"):
                profile = decode_profile(path)
                application = profile.get("Entitlements", {}).get("application-identifier", "")
                bundle = application.removeprefix(f"{TEAM}.")
                if bundle not in bundles:
                    raise ValueError("Fetched a profile outside the TailSplit application")
                validate_profile(profile, bundle, config["app_group"])
                found.add(bundle)
            if found != set(bundles):
                raise ValueError("Both Runner and PacketTunnel need valid Termitoc App Store profiles")
            run("keychain", "add-certificates", "--path", str(keychain),
                "--certificate", str(certificates / "*.p12"), env=env)
            options = work / "ExportOptions.plist"
            run("xcode-project", "use-profiles", "--project", "ios/Runner.xcodeproj",
                "--profile", str(profiles / "*.mobileprovision"), "--archive-method", "app-store",
                "--custom-export-options", json.dumps({"teamID": TEAM, "method": "app-store-connect"}),
                "--export-options-plist", str(options), env=env)
            run("flutter", "build", "ipa", "--release", "--no-pub",
                f"--build-number={build_number}", f"--export-options-plist={options}", env=env)
            packages = list((MOBILE / "build/ios/ipa").glob("*.ipa"))
            if len(packages) != 1:
                raise ValueError("Expected exactly one exported IPA")
            extracted = work / "exported"
            run("ditto", "-x", "-k", str(packages[0]), str(extracted))
            apps = list((extracted / "Payload").glob("*.app"))
            if len(apps) != 1:
                raise ValueError("Expected one application in the exported IPA")
            app = apps[0]
            verify_bundle(app, bundles[0], config["app_group"])
            extensions = list((app / "PlugIns").glob("*.appex"))
            if len(extensions) != 1:
                raise ValueError("Expected the single PacketTunnel extension")
            verify_bundle(extensions[0], bundles[1], config["app_group"])
            print(f"Verified IPA: {config['organization']} ({TEAM}), Runner and PacketTunnel")
            if publish:
                # Upload for internal TestFlight processing. Public App Store/beta
                # review submission remains a separate, explicit Apple action.
                run("app-store-connect", "publish", "--path", str(packages[0]),
                    "--enable-package-validation", env=env)
        finally:
            if previous:
                subprocess.run(["security", "default-keychain", "-d", "user", "-s", previous], check=False)
            if keychain.exists():
                subprocess.run(["keychain", "delete", "--path", str(keychain)], check=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--publish", action="store_true", help="Upload the verified IPA to App Store Connect")
    args = parser.parse_args()
    try:
        release(args.publish)
    except (ValueError, KeyError, subprocess.CalledProcessError) as error:
        # Do not include private configuration payloads or command environments.
        if isinstance(error, ValueError):
            parser.exit(1, f"Release stopped: {error}\n")
        parser.exit(1, "Release stopped: signing, build or publication step failed.\n")


if __name__ == "__main__":
    main()
