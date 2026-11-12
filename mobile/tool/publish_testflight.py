#!/usr/bin/env python3
"""Upload a verified IPA and confirm distribution to its internal TestFlight group."""
import argparse
import http.client
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

MOBILE = Path(__file__).resolve().parents[1]
API_ROOT = "https://api.appstoreconnect.apple.com"
API_ENV = ("APP_STORE_CONNECT_ISSUER_ID", "APP_STORE_CONNECT_KEY_IDENTIFIER", "APP_STORE_CONNECT_PRIVATE_KEY")
HTTP_TIMEOUT = 30
PUBLISH_TIMEOUT = 7200
READBACK_ATTEMPTS = 6
READBACK_DELAY = 10
READY_STATES = {"READY_FOR_BETA_TESTING", "IN_BETA_TESTING"}


class PublicationError(ValueError):
    """A nonsecret error suitable for CI output."""


class BuildNotReady(PublicationError):
    """A readback condition that may resolve after Apple's eventual consistency delay."""


def identity(channel):
    if channel not in ("beta", "app-store"):
        raise PublicationError("Choose the beta or app-store release channel")
    expected = {
        "bundle_id": "dev.idy.tailsplit.beta" if channel == "beta" else "dev.idy.tailsplit",
        "app_store_id": "6833766631" if channel == "beta" else "6833766298",
        "testflight_group": "Internal Testing",
        "testflight_group_id": ("4be1c696-606b-4ae9-b507-9a385956c6ca" if channel == "beta"
                                else "ee8bc770-f372-4c74-8700-f7035c844fc4"),
        "app_store": channel == "app-store",
    }
    try:
        value = json.loads((MOBILE / "release-identity.json").read_text(encoding="utf-8"))
        selected = value["channels"][channel]
        matches = (all(selected.get(key) == item for key, item in expected.items())
                   and selected.get("app_store") is expected["app_store"])
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        raise PublicationError("Cannot read the trusted TestFlight channel identity") from None
    if not matches:
        raise PublicationError("TestFlight publication requires the exact TailSplit channel identity")
    return expected


def sanitized_env():
    """Only the ASC key and the signing helper's base allowlist reach the CLI."""
    env = {key: os.environ[key] for key in ("HOME", "RUNNER_TEMP", "TMPDIR", "LANG", "DEVELOPER_DIR")
           if key in os.environ}
    env["PATH"] = f"{Path(sys.executable).parent}:/usr/bin:/bin:/usr/sbin:/sbin"
    for key in API_ENV:
        value = os.environ.get(key, "")
        if not value.strip():
            raise PublicationError(f"Configure {key} before publishing")
        env[key] = value
    env["APP_STORE_CONNECT_DISABLE_JWT_CACHE"] = "true"
    return env


def authorization():
    """Keep JWT creation lazy: build/test runners do not need PyJWT installed."""
    values = {key: os.environ.get(key, "") for key in API_ENV}
    if any(not value.strip() for value in values.values()):
        raise PublicationError("App Store Connect authentication requires all three API credentials")
    try:
        import jwt

        now = int(time.time())
        token = jwt.encode(
            {"iss": values[API_ENV[0]], "iat": now, "exp": now + 600, "aud": "appstoreconnect-v1"},
            values[API_ENV[2]], algorithm="ES256", headers={"kid": values[API_ENV[1]], "typ": "JWT"},
        )
        if not isinstance(token, str) or not token:
            raise ValueError
    except Exception:
        # PyJWT/crypto exceptions can include key material; never forward their text.
        raise PublicationError("Cannot create App Store Connect authentication token; check the signing venv and API key") from None
    return token


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, new_url):
        # The two fixed ASC endpoints must not forward their bearer token elsewhere.
        return None


def apple_get(path, query=None):
    url = API_ROOT + path
    if query:
        url += "?" + urllib.parse.urlencode(query)
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {authorization()}",
                                                 "Accept": "application/json"}, method="GET")
    # Ignore inherited proxy settings, and never follow response-provided pagination URLs.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        with opener.open(request, timeout=HTTP_TIMEOUT) as response:
            value = json.load(response)
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
        raise PublicationError(f"App Store Connect read failed (HTTP {status}); no upload/distribution success was verified") from None
    except (OSError, ValueError, http.client.HTTPException):
        raise PublicationError("Cannot read a valid App Store Connect response") from None
    if not isinstance(value, dict):
        raise PublicationError("App Store Connect returned an invalid resource response")
    return value


def attributes(resource):
    value = resource.get("attributes", {})
    if not isinstance(value, dict):
        raise PublicationError("App Store Connect returned invalid resource attributes")
    return value


def related(resource, name):
    relationships = resource.get("relationships", {})
    if not isinstance(relationships, dict) or not isinstance(relationships.get(name, {}), dict):
        raise PublicationError("App Store Connect returned invalid resource relationships")
    return relationships.get(name, {}).get("data")


def is_resource(value, kind, identifier=None):
    return (isinstance(value, dict) and value.get("type") == kind
            and isinstance(value.get("id"), str) and bool(value["id"])
            and (identifier is None or value["id"] == identifier))


def included_resources(payload):
    included = payload.get("included", [])
    if not isinstance(included, list):
        raise PublicationError("App Store Connect returned invalid included resources")
    result = {}
    for resource in included:
        if (not isinstance(resource, dict) or not isinstance(resource.get("type"), str)
                or not is_resource(resource, resource["type"])):
            raise PublicationError("App Store Connect returned an invalid included resource")
        key = (resource["type"], resource["id"])
        if not isinstance(key[0], str) or key in result:
            raise PublicationError("App Store Connect returned ambiguous included resources")
        result[key] = resource
    return result


def verify_group(config):
    payload = apple_get(f"/v1/betaGroups/{config['testflight_group_id']}", {"include": "app"})
    group = payload.get("data")
    if not is_resource(group, "betaGroups", config["testflight_group_id"]):
        raise PublicationError("The expected internal TestFlight group is missing or has a different ID")
    attrs = attributes(group)
    if attrs.get("name") != config["testflight_group"] or attrs.get("isInternalGroup") is not True:
        raise PublicationError("The TestFlight group must have the expected name and be internal")
    if not is_resource(related(group, "app"), "apps", config["app_store_id"]):
        raise PublicationError("The internal TestFlight group belongs to a different app")
    for (kind, identifier), app in included_resources(payload).items():
        if kind == "apps" and (identifier != config["app_store_id"]
                               or attributes(app).get("bundleId") != config["bundle_id"]):
            raise PublicationError("The internal TestFlight group's app bundle does not match the channel")


def verify_build(payload, config, build_number, version, *, resume=False):
    """Resume may accept an absent build or group, but never uncertain build metadata."""
    builds = payload.get("data")
    if not isinstance(builds, list):
        raise PublicationError("App Store Connect returned an invalid builds response")
    links = payload.get("links", {})
    if not isinstance(links, dict) or links.get("next"):
        raise PublicationError("Build verification requires a complete, unpaginated response")
    included = included_resources(payload)
    matches = []
    for build in builds:
        if not is_resource(build, "builds"):
            raise PublicationError("App Store Connect returned an invalid build resource")
        number = attributes(build).get("version")
        if resume and (not isinstance(number, str) or number != build_number):
            raise PublicationError("Existing upload query returned a missing or wrong build number; refusing another upload")
        if number != build_number:
            continue
        prerelease = related(build, "preReleaseVersion")
        prerelease = included.get(("preReleaseVersions", prerelease["id"])) if is_resource(prerelease, "preReleaseVersions") else None
        if prerelease is None:
            if resume:
                raise PublicationError("Existing upload has missing version metadata; refusing another upload")
            continue
        attrs = attributes(prerelease)
        if resume and any(not isinstance(attrs.get(key), str) or not attrs[key] for key in ("version", "platform")):
            raise PublicationError("Existing upload has malformed version or platform metadata; refusing another upload")
        # ASC's app filter is authoritative when an app linkage is not expanded.
        for resource in (build, prerelease):
            app = related(resource, "app")
            if app is not None and not is_resource(app, "apps", config["app_store_id"]):
                raise PublicationError("The matching TestFlight build belongs to a different app")
        if attrs.get("version") != version or attrs.get("platform") != "IOS":
            continue
        matches.append(build)
    if not matches:
        if resume:
            if builds:
                raise PublicationError("Existing uploads do not match the exact iOS application version; refusing another upload")
            return None
        raise BuildNotReady("The exact iOS build number and application version were not found in App Store Connect")
    if len(matches) != 1:
        raise PublicationError("Multiple builds match the iOS build number and application version")
    build = matches[0]
    attrs = attributes(build)
    if attrs.get("processingState") in ("FAILED", "INVALID"):
        raise PublicationError("Apple rejected processing of the uploaded build")
    if attrs.get("expired") is True:
        raise PublicationError("The uploaded TestFlight build is expired")
    detail = related(build, "buildBetaDetail")
    detail = included.get(("buildBetaDetails", detail["id"])) if is_resource(detail, "buildBetaDetails") else None
    state = attributes(detail).get("internalBuildState") if detail else None
    if state in ("MISSING_EXPORT_COMPLIANCE", "IN_EXPORT_COMPLIANCE_REVIEW"):
        raise PublicationError("Export compliance blocks internal TestFlight readiness; resolve it in App Store Connect")
    if state in ("PROCESSING_EXCEPTION", "EXPIRED"):
        raise PublicationError("The build's internal TestFlight processing failed or expired")
    if (attrs.get("processingState") != "VALID" or attrs.get("expired") is not False
            or not isinstance(state, str) or state not in READY_STATES):
        raise BuildNotReady("The uploaded build is not yet valid and ready for internal TestFlight testing; retry after Apple finishes processing")
    groups = related(build, "betaGroups")
    assigned = (isinstance(groups, list)
                and any(is_resource(group, "betaGroups", config["testflight_group_id"]) for group in groups))
    if not resume and not assigned:
        raise BuildNotReady("The uploaded build is not assigned to the expected internal TestFlight group")
    verified = {"build_id": build["id"], "processing_state": "VALID", "internal_build_state": state}
    return {**verified, "group_assigned": assigned} if resume else verified


def fetch_builds(config, build_number):
    return apple_get("/v1/builds", {"filter[app]": config["app_store_id"], "filter[version]": build_number,
                                  "include": "preReleaseVersion,betaGroups,buildBetaDetail", "limit": "200"})


def readback(config, build_number, version, expected_build_id=None):
    for attempt in range(READBACK_ATTEMPTS):
        try:
            verified = verify_build(fetch_builds(config, build_number), config, build_number, version)
            if expected_build_id is not None and verified["build_id"] != expected_build_id:
                raise PublicationError("Final TestFlight readback identifies a different build than the existing upload")
            return verified
        except BuildNotReady:
            if attempt == READBACK_ATTEMPTS - 1:
                raise
            time.sleep(READBACK_DELAY)


def publish(ipa, channel, build_number, version, receipt, submit_for_review=False, resume_upload=False):
    config = identity(channel)
    if submit_for_review and config["app_store"] is not True:
        raise PublicationError("App Store review requires the app-store channel; beta is TestFlight-only")
    if not isinstance(build_number, str) or not re.fullmatch(r"[1-9][0-9]*", build_number):
        raise PublicationError("A positive integer build number is required")
    if not isinstance(version, str) or not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise PublicationError("The application version must be X.Y.Z")
    ipa, receipt = Path(ipa).absolute(), Path(receipt).absolute()
    if not stat.S_ISREG(ipa.lstat().st_mode) or ipa.suffix != ".ipa":
        raise PublicationError("Expected the verified regular IPA file")
    if receipt.exists() or receipt.is_symlink() or not receipt.parent.is_dir():
        raise PublicationError("The receipt path must be new and its parent directory must exist")
    runner_temp = Path(os.environ.get("RUNNER_TEMP", ""))
    if not runner_temp.is_absolute() or not runner_temp.is_dir():
        raise PublicationError("RUNNER_TEMP must identify the trusted runner temporary directory")
    env = sanitized_env()
    verify_group(config)
    existing = (verify_build(fetch_builds(config, build_number), config, build_number, version, resume=True)
                if resume_upload else None)
    resumed = existing is not None
    target = f"{config['bundle_id']} {version} ({build_number})"
    print(f"Resuming internal TestFlight distribution for {target}" if resumed
          else f"Starting TestFlight upload for {target}", flush=True)
    # In codemagic 0.70.2, skip-package-upload still validates when enabled. Apple
    # already accepted a resumed binary; validating it again can reject duplicates.
    upload_option = "--skip-package-upload" if resumed else "--enable-package-validation"
    command = [str(Path(sys.executable).parent / "app-store-connect"), "publish", "--path", str(ipa),
               upload_option, "--beta-group", config["testflight_group"]]
    if submit_for_review:
        command += ["--app-store", "--platform", "IOS", "--release-type", "MANUAL"]
    if not (resumed and existing["group_assigned"] and not submit_for_review):
        try:
            subprocess.run(command, cwd=str(runner_temp), env=env, check=True, stdin=subprocess.DEVNULL,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=PUBLISH_TIMEOUT)
        except (OSError, subprocess.SubprocessError):
            # CLI logs and exception output may contain credentials or raw Apple errors.
            raise PublicationError("TestFlight upload/distribution command failed; no success receipt was written") from None
    print(f"Verifying internal TestFlight readiness and group membership for {target}", flush=True)
    verified = readback(config, build_number, version, existing["build_id"] if resumed else None)
    result = {"channel": channel, "app_store_id": config["app_store_id"], "bundle_id": config["bundle_id"],
              "build_number": build_number, "version": version, "platform": "IOS",
              "testflight_group_id": config["testflight_group_id"], "testflight_group": config["testflight_group"],
              "submitted_for_review": submit_for_review, "resumed_upload": resumed, **verified}
    with receipt.open("x", encoding="utf-8") as destination:
        json.dump(result, destination, indent=2, sort_keys=True)
        destination.write("\n")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ipa", type=Path, required=True, help="Already verified, signed IPA")
    parser.add_argument("--channel", choices=("beta", "app-store"), required=True)
    parser.add_argument("--build-number", required=True, help="Positive integer CFBundleVersion")
    parser.add_argument("--version", required=True, help="Exact X.Y.Z application version")
    parser.add_argument("--receipt", type=Path, required=True, help="New JSON file, written only after verified distribution")
    parser.add_argument("--submit-for-review", action="store_true", help="Explicit public App Review for app-store only (manual release)")
    parser.add_argument("--resume-upload", action="store_true", help="For a validated reused archive, resume a ready existing upload without uploading again")
    args = parser.parse_args()
    try:
        result = publish(args.ipa, args.channel, args.build_number, args.version, args.receipt,
                         args.submit_for_review, resume_upload=args.resume_upload)
    except PublicationError as error:
        parser.exit(1, f"Publication stopped: {error}\n")
    except OSError:
        parser.exit(1, "Publication stopped: cannot read the IPA or write the verified receipt.\n")
    print(f"Verified internal TestFlight distribution: {result['bundle_id']} {result['version']} ({result['build_number']})")


if __name__ == "__main__":
    main()
