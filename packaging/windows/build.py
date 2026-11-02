#!/usr/bin/env python3
"""Build one native Windows desktop EXE; invoke as python -I packaging/windows/build.py.

Requires a matching native Windows runner, Go, Visual Studio's C++ tools, and
Windows SDK. Downloaded tools stay in a private temporary build directory. Inno
is bootstrapped in its official /PORTABLE=1 mode, without a host installation.
The standalone CLI and release inventory are deliberately outside this script.
"""
import argparse
import base64
import ctypes
from ctypes import wintypes
import gzip
import hashlib
import json
import math
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import struct
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit
from urllib.request import HTTPSHandler, HTTPRedirectHandler, Request, build_opener
import xml.etree.ElementTree as ET
import zipfile

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent
MACHINES = {"amd64": 0x8664, "arm64": 0xAA64}
NATIVE_NAMES = {"amd64": "x64", "arm64": "arm64"}
PAYLOAD = ("TailSplit.exe", "tailsplit-core.exe", "TailSplit.Window.dll",
           "config.example.yaml", "LICENSE", "THIRD_PARTY_NOTICES.md", "build.json")
MAX_DOWNLOAD = 768 * 1024 * 1024
MAX_EXTRACT = 2 * 1024 * 1024 * 1024

class BuildError(RuntimeError):
    pass


def file_digest(path, algorithm):
    digest = hashlib.new(algorithm)
    with Path(path).open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            digest.update(chunk)
    return digest


def sha256(path):
    return file_digest(path, "sha256").hexdigest()


def identity(arch, version, commit, build_number=0):
    if arch not in MACHINES:
        raise BuildError("Architecture must be amd64 or arm64")
    if not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", version):
        raise BuildError("Version must be major.minor.patch without a prefix")
    if not re.fullmatch(r"[0-9a-fA-F]{40}", commit):
        raise BuildError("Commit must contain exactly 40 hexadecimal characters")
    if any(int(part) > 65535 for part in version.split(".")) or not 0 <= build_number <= 65535:
        raise BuildError("Windows version components and build number must be 0..65535")
    return {"schema_version": 1, "project": "tailsplit", "os": "windows", "arch": arch,
            "version": version, "commit": commit.lower(), "dirty": False}


def artifact_name(info):
    return f"TailSplit_{info['version']}_windows_{info['arch']}_setup.exe"


def native_arch():
    if sys.platform != "win32":
        raise BuildError("Windows desktop packaging requires a native Windows runner")
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.GetCurrentProcess.restype = wintypes.HANDLE
    kernel.IsWow64Process2.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.USHORT),
                                     ctypes.POINTER(wintypes.USHORT)]
    kernel.IsWow64Process2.restype = wintypes.BOOL
    process, native = wintypes.USHORT(), wintypes.USHORT()
    if not kernel.IsWow64Process2(kernel.GetCurrentProcess(), ctypes.byref(process), ctypes.byref(native)):
        raise BuildError("Cannot determine the native Windows architecture")
    for arch, machine in MACHINES.items():
        if machine == native.value:
            return arch
    raise BuildError("Only native x64 and ARM64 Windows are supported")


def pe_machine(path):
    with Path(path).open("rb") as stream:
        dos = stream.read(64)
        if len(dos) != 64 or dos[:2] != b"MZ":
            raise BuildError(f"Not a Windows PE file: {Path(path).name}")
        offset = struct.unpack_from("<I", dos, 60)[0]
        if offset < 64 or offset > 16 * 1024 * 1024:
            raise BuildError("Invalid PE header offset")
        stream.seek(offset)
        header = stream.read(6)
        if len(header) != 6 or header[:4] != b"PE\0\0":
            raise BuildError("Invalid PE signature")
        return struct.unpack_from("<H", header, 4)[0]


def require_machine(path, arch):
    if pe_machine(path) != MACHINES[arch]:
        raise BuildError(f"{Path(path).name} is not a native {arch} PE")


class OfficialRedirects(HTTPRedirectHandler):
    """Keep executable downloads on the original vendors' HTTPS infrastructure."""
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        check_url(newurl)
        return super().redirect_request(request, fp, code, msg, headers, newurl)


def check_url(url):
    parsed = urlsplit(url)
    host = (parsed.hostname or "").lower()
    exact = {"api.github.com", "github.com", "objects.githubusercontent.com",
             "release-assets.githubusercontent.com", "api.nuget.org", "globalcdn.nuget.org"}
    microsoft = host == "microsoft.com" or host.endswith(".microsoft.com")
    if (parsed.scheme != "https" or parsed.username or parsed.password or
            parsed.port not in (None, 443) or (host not in exact and not microsoft)):
        raise BuildError("Dependency URL is outside official HTTPS download hosts")


def fetch(url, destination):
    """Create a new file; never reuse an old executable or unpack into its directory."""
    check_url(url)
    destination = Path(destination)
    opener = build_opener(HTTPSHandler(), OfficialRedirects())
    request = Request(url, headers={"User-Agent": "TailSplit-Windows-Packager/1", "Accept": "*/*"})
    total = 0
    with opener.open(request, timeout=120) as response, destination.open("xb") as output:
        check_url(response.geturl())
        while data := response.read(1024 * 1024):
            total += len(data)
            if total > MAX_DOWNLOAD:
                raise BuildError("Dependency download exceeded its size limit")
            output.write(data)
    if not total:
        raise BuildError("Dependency download was empty")
    return destination


def fetch_json(url, directory, name):
    path = fetch(url, Path(directory) / name)
    limit = 8 * 1024 * 1024
    with path.open("rb") as source:
        compressed = source.read(2) == b"\x1f\x8b"
        source.seek(0)
        if compressed:
            # NuGet's registration5-gz-semver2 endpoint can serve gzip even
            # without an Accept-Encoding request. urllib does not decode it.
            with gzip.GzipFile(fileobj=source) as decoded:
                data = decoded.read(limit + 1)
        else:
            data = source.read(limit + 1)
    if len(data) > limit:
        raise BuildError("Dependency metadata exceeded its size limit")
    return json.loads(data.decode("utf-8-sig"))


def archive_name(name):
    """Validate ZIP paths using Windows rules even when unit tested on Unix."""
    if not name or "\\" in name or "\x00" in name:
        raise BuildError("Unsafe path in SDK archive")
    path = PurePosixPath(name)
    parts = name.rstrip("/").split("/")
    reserved = {"CON", "PRN", "AUX", "NUL"} | {f"{kind}{n}" for kind in ("COM", "LPT") for n in range(1, 10)}
    if path.is_absolute() or any(part in ("", ".", "..") or any(char in part for char in ':<>"|?*') or
                                any(ord(char) < 32 for char in part) or part[-1:] in (".", " ") or
                                part.split(".")[0].upper() in reserved for part in parts):
        raise BuildError("Unsafe path in SDK archive")
    return path


def extract_sdk(package, destination):
    destination = Path(destination)
    destination.mkdir()  # must be a new empty directory
    seen, total = set(), 0
    with zipfile.ZipFile(package) as archive:
        entries = archive.infolist()
        if len(entries) > 10000:
            raise BuildError("Too many files in SDK archive")
        # Validate the entire archive before writing any member.
        for item in entries:
            path = archive_name(item.filename)
            key = str(path).casefold()
            mode = item.external_attr >> 16
            if key in seen or stat.S_ISLNK(mode) or (mode and stat.S_IFMT(mode) not in (0, stat.S_IFREG, stat.S_IFDIR)):
                raise BuildError("Duplicate or special file in SDK archive")
            seen.add(key)
            total += item.file_size
            if item.flag_bits & 1 or total > MAX_EXTRACT:
                raise BuildError("Encrypted or oversized SDK archive")
        for item in entries:
            target = destination.joinpath(*archive_name(item.filename).parts)
            if item.is_dir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with archive.open(item) as source, target.open("xb") as output:
                    shutil.copyfileobj(source, output)
    return destination


def sdk_license(sdk, version):
    manifests = list(Path(sdk).glob("*.nuspec"))
    if len(manifests) != 1:
        raise BuildError("WebView2 SDK must contain exactly one NuGet manifest")
    tree = ET.parse(manifests[0])
    values = {node.tag.rsplit("}", 1)[-1]: node for node in tree.iter()}
    if (values.get("id") is None or values["id"].text != "Microsoft.Web.WebView2" or
            values.get("version") is None or values["version"].text != version):
        raise BuildError("WebView2 SDK manifest does not match the pinned package")
    license_node = values.get("license")
    if license_node is None or license_node.get("type") != "file" or not license_node.text:
        raise BuildError("WebView2 SDK is missing its original file license")
    relative = archive_name(license_node.text)
    license_path = Path(sdk).joinpath(*relative.parts)
    if not license_path.is_file() or not license_path.read_bytes().strip():
        raise BuildError("WebView2 SDK license file is absent or empty")
    return license_path


def append_notices(source, destination, license_path, version):
    original, license_bytes = Path(source).read_bytes(), Path(license_path).read_bytes()
    if not original.strip() or not license_bytes.strip():
        raise BuildError("Both existing third-party notices and the original SDK license are required")
    # Keep the SDK's original bytes, including its BOM and line endings.
    heading = (f"\n\n## Microsoft.Web.WebView2 {version}\n\n"
               "Source: https://www.nuget.org/packages/Microsoft.Web.WebView2/" + version +
               "\nThe original WebView2 SDK license follows.\n\n").encode("utf-8")
    Path(destination).write_bytes(original + heading + license_bytes)


def powershell():
    root = os.environ.get("SystemRoot")
    if not root:
        raise BuildError("Windows system directory is unavailable")
    path = Path(root) / "System32/WindowsPowerShell/v1.0/powershell.exe"
    if not path.is_file():
        raise BuildError("Windows PowerShell is required for Authenticode verification")
    return path


def ps_literal(value):
    return "'" + str(value).replace("'", "''") + "'"


def verify_authenticode(path, publisher, version=None):
    """Fail closed on unsigned, invalid-chain, or different-publisher packages."""
    script = ("$ErrorActionPreference='Stop'; $p=" + ps_literal(Path(path).resolve()) + "; "
              "$s=Get-AuthenticodeSignature -LiteralPath $p; "
              "if($s.Status -ne 'Valid' -or !$s.SignerCertificate){throw 'Invalid Authenticode signature'}; "
              "$n=$s.SignerCertificate.GetNameInfo([System.Security.Cryptography.X509Certificates.X509NameType]::SimpleName,$false); "
              "if($n -cne " + ps_literal(publisher) + "){throw 'Unexpected package publisher'}; ")
    if version:
        script += ("$v=[Diagnostics.FileVersionInfo]::GetVersionInfo($p).ProductVersion; "
                   "if($v -notmatch '^" + re.escape(version) + r"(?:\.0)?(?:\s.*)?$'" +
                   "){throw 'Unexpected tool version'}; ")
    encoded = base64.b64encode(script.encode("utf-16-le")).decode("ascii")
    subprocess.run([str(powershell()), "-NoLogo", "-NoProfile", "-NonInteractive",
                    "-EncodedCommand", encoded], cwd=ROOT, check=True, stdout=subprocess.DEVNULL)


def prepare_sdk(work, toolchain):
    config = toolchain["webview2_sdk"]
    directory = Path(work) / "webview2-sdk"
    directory.mkdir()
    registration = fetch_json(config["registration_url"], directory, "registration.json")
    entry = registration.get("catalogEntry")
    if isinstance(entry, str):
        entry = fetch_json(entry, directory, "catalog.json")
    if not isinstance(entry, dict) or entry.get("id", "").lower() != config["package"].lower() or entry.get("version") != config["version"]:
        raise BuildError("NuGet catalog does not match the pinned WebView2 SDK")
    if entry.get("packageHashAlgorithm", "").upper() != "SHA512":
        raise BuildError("NuGet catalog did not supply a SHA-512 package hash")
    try:
        expected = base64.b64decode(entry["packageHash"], validate=True)
    except (KeyError, ValueError) as error:
        raise BuildError("NuGet catalog supplied an invalid package hash") from error
    if len(expected) != 64:
        raise BuildError("NuGet catalog supplied an invalid SHA-512 digest")
    package = fetch(config["package_url"], directory / "sdk.nupkg")
    actual = file_digest(package, "sha512").digest()
    if actual != expected:
        raise BuildError("WebView2 SDK package failed the official NuGet SHA-512 check")
    sdk = extract_sdk(package, directory / "extracted")
    return sdk, sdk_license(sdk, config["version"])


def prepare_inno(work, toolchain):
    config = toolchain["inno"]
    directory = Path(work) / "inno"
    directory.mkdir()
    release = fetch_json(config["release_api"], directory, "release.json")
    assets = [asset for asset in release.get("assets", []) if asset.get("name") == config["asset"]]
    if release.get("tag_name") != config["tag"] or release.get("prerelease") or len(assets) != 1:
        raise BuildError("Official Inno release does not match the pinned toolchain")
    asset = assets[0]
    expected = asset.get("digest", "")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", expected):
        raise BuildError("Official Inno release asset lacks its SHA-256 digest")
    installer = fetch(asset["browser_download_url"], directory / config["asset"])
    if sha256(installer) != expected.removeprefix("sha256:"):
        raise BuildError("Inno Setup download failed the official release SHA-256 check")
    verify_authenticode(installer, config["publisher"], config["version"])
    compiler_dir = directory / "portable"
    compiler_dir.mkdir()
    # Inno's official portable mode disables uninstall registration, shortcuts,
    # and file associations. This is a temporary tool, not a host installation.
    subprocess.run([str(installer), "/PORTABLE=1", "/CURRENTUSER", "/VERYSILENT", "/SUPPRESSMSGBOXES",
                    "/NORESTART", "/SP-", "/NOICONS", "/TASKS=", "/DIR=" + str(compiler_dir)],
                   cwd=ROOT, check=True, stdout=subprocess.DEVNULL)
    compiler = compiler_dir / "ISCC.exe"
    if not compiler.is_file():
        raise BuildError("Pinned portable Inno compiler was not extracted")
    return compiler


def executable_version(path):
    script = ("$ErrorActionPreference='Stop'; $v=[Diagnostics.FileVersionInfo]::GetVersionInfo(" +
              ps_literal(Path(path).resolve()) + "); "
              "'{0}.{1}.{2}.{3}' -f $v.FileMajorPart,$v.FileMinorPart,$v.FileBuildPart,$v.FilePrivatePart")
    encoded = base64.b64encode(script.encode("utf-16-le")).decode("ascii")
    version = subprocess.check_output([str(powershell()), "-NoLogo", "-NoProfile", "-NonInteractive",
                                       "-EncodedCommand", encoded], cwd=ROOT, text=True).strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+", version):
        raise BuildError("Runtime installer lacks its numeric Windows file version")
    # Evergreen standalone installers carry the browser's version, not the
    # Evergreen updater's 1.x version. Reject a bootstrapper or wrong product.
    if int(version.split(".")[0]) < 120:
        raise BuildError("Expected a current Evergreen standalone runtime installer")
    return version


def prepare_runtime(work, arch, toolchain):
    directory = Path(work) / "webview2-runtime"
    directory.mkdir()
    filename = "MicrosoftEdgeWebView2RuntimeInstaller" + {"amd64": "X64", "arm64": "ARM64"}[arch] + ".exe"
    runtime = fetch(toolchain["webview2_runtime"][arch], directory / filename)
    verify_authenticode(runtime, toolchain["webview2_runtime"]["publisher"])
    return runtime, executable_version(runtime)


def clean_environment():
    environment = {key: value for key, value in os.environ.items()
                   if not key.upper().startswith(("SOPS_AGE_", "AWS_", "AZURE_")) and
                   key.upper() not in {"GH_TOKEN", "GITHUB_TOKEN", "CL", "_CL_", "LINK", "_LINK_", "GOFLAGS"}}
    return environment


def msvc_toolchain(arch, environment):
    """Select target libraries explicitly; never trust an inherited x64 dev shell."""
    import winreg
    pf86 = os.environ.get("ProgramFiles(x86)", os.environ.get("ProgramFiles", ""))
    locator = Path(pf86) / "Microsoft Visual Studio/Installer/vswhere.exe"
    if not locator.is_file():
        raise BuildError("Visual Studio C++ tools and vswhere are required")
    output = subprocess.check_output([str(locator), "-latest", "-products", "*", "-requires",
                                      "Microsoft.VisualStudio.Component.VC.Tools.x86.x64" if arch == "amd64" else
                                      "Microsoft.VisualStudio.Component.VC.Tools.ARM64", "-property", "installationPath"],
                                     cwd=ROOT, env=environment, text=True).strip()
    if not output or "\n" in output:
        raise BuildError("A matching Visual Studio C++ target toolchain is required")
    vs = Path(output)
    version = (vs / "VC/Auxiliary/Build/Microsoft.VCToolsVersion.default.txt").read_text().strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise BuildError("Invalid Visual Studio tools version")
    vc = vs / "VC/Tools/MSVC" / version
    target = NATIVE_NAMES[arch]
    # ARM64 runners may contain either a native ARM64 compiler or VS's x64 cross compiler.
    hosts = ("Hostarm64", "Hostx64") if arch == "arm64" else ("Hostx64",)
    compilers = [vc / "bin" / host / target / "cl.exe" for host in hosts]
    compiler = next((path for path in compilers if path.is_file()), None)
    if compiler is None:
        raise BuildError("MSVC compiler for the selected target was not found")
    with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, r"SOFTWARE\Microsoft\Windows Kits\Installed Roots",
                        0, winreg.KEY_READ | winreg.KEY_WOW64_32KEY) as key:
        sdk = Path(winreg.QueryValueEx(key, "KitsRoot10")[0])
    versions = sorted((path.name for path in (sdk / "Include").iterdir()
                       if re.fullmatch(r"10\.\d+\.\d+\.\d+", path.name)),
                      key=lambda value: tuple(map(int, value.split("."))), reverse=True)
    sdk_version = next((value for value in versions if
                        (sdk / "Lib" / value / "um" / target / "ole32.lib").is_file() and
                        (sdk / "Lib" / value / "ucrt" / target / "ucrt.lib").is_file()), None)
    if sdk_version is None:
        raise BuildError("Matching Windows SDK target libraries are required")
    include = [vc / "include"] + [sdk / "Include" / sdk_version / part for part in ("ucrt", "shared", "um", "winrt")]
    libs = [vc / "lib" / target, sdk / "Lib" / sdk_version / "ucrt" / target,
            sdk / "Lib" / sdk_version / "um" / target]
    if any(not path.is_dir() for path in include + libs):
        raise BuildError("MSVC or Windows SDK headers/libraries are missing")
    result = dict(environment, INCLUDE=";".join(map(str, include)), LIB=";".join(map(str, libs)))
    result["PATH"] = str(compiler.parent) + os.pathsep + result.get("PATH", "")
    return compiler, result


def dll_command(compiler, source, sdk, destination, arch):
    sdk = Path(sdk)
    library = sdk / "build/native" / NATIVE_NAMES[arch] / "WebView2LoaderStatic.lib"
    header = sdk / "build/native/include/WebView2.h"
    if not header.is_file() or not library.is_file():
        raise BuildError("WebView2 SDK is missing the matching native headers or static loader")
    return [str(compiler), "/nologo", "/std:c++17", "/MT", "/LD", "/O2", "/EHsc", "/W4",
            "/DUNICODE", "/D_UNICODE", "/DWIN32_LEAN_AND_MEAN", "/DNOMINMAX",
            "/I" + str(header.parent), str(source), "/Fo" + str(Path(destination).with_suffix(".obj")),
            "/link", "/MACHINE:" + NATIVE_NAMES[arch].upper(), "/DYNAMICBASE", "/NXCOMPAT",
            "/OUT:" + str(destination), "/IMPLIB:" + str(Path(destination).with_suffix(".lib")),
            str(library), "ole32.lib", "oleaut32.lib", "user32.lib", "shlwapi.lib", "shell32.lib",
            "advapi32.lib", "gdi32.lib", "version.lib"]


def gui_command(go, destination, info):
    flags = ("-H=windowsgui -s -w -X main.version=" + info["version"] +
             " -X main.commit=" + info["commit"])
    return [str(go), "build", "-mod=readonly", "-tags", "with_gvisor", "-trimpath", "-buildvcs=false",
            "-ldflags", flags, "-o", str(destination), "./cmd/tailsplit-tray"]


def manifest_bytes():
    return b'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
 <assemblyIdentity type="win32" name="TailSplit.Desktop" version="1.0.0.0"/>
 <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3"><security><requestedPrivileges>
  <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
 </requestedPrivileges></security></trustInfo>
 <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1"><application>
  <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
 </application></compatibility>
 <application xmlns="urn:schemas-microsoft-com:asm.v3"><windowsSettings>
  <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
  <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2,PerMonitor</dpiAwareness>
  <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
 </windowsSettings></application>
</assembly>
'''


def version_resource(info, build_number):
    def wide(value):
        return (value + "\0").encode("utf-16-le")

    def block(key, value=b"", children=b"", text=False):
        data = struct.pack("<HHH", 0, len(value) // 2 if text else len(value), int(text)) + wide(key)
        data += b"\0" * (-len(data) % 4) + value
        if children:
            data += b"\0" * (-len(data) % 4) + children
        return struct.pack("<H", len(data)) + data[2:]

    def children(blocks):
        result = b""
        for value in blocks:
            result += b"\0" * (-len(result) % 4) + value
        return result

    major, minor, patch = map(int, info["version"].split("."))
    high, low = (major << 16) | minor, (patch << 16) | build_number
    fixed = struct.pack("<13I", 0xFEEF04BD, 0x10000, high, low, high, low,
                        0x3F, 0, 0x40004, 1, 0, 0, 0)
    strings = {"CompanyName": "TailSplit contributors", "FileDescription": "TailSplit desktop",
               "FileVersion": f"{info['version']}.{build_number}", "InternalName": "TailSplit",
               "LegalCopyright": "TailSplit contributors", "OriginalFilename": "TailSplit.exe",
               "ProductName": "TailSplit", "ProductVersion": info["version"]}
    table = block("040904B0", children=children([block(key, wide(value), text=True) for key, value in strings.items()]), text=True)
    string_info = block("StringFileInfo", children=table, text=True)
    var_info = block("VarFileInfo", children=block("Translation", struct.pack("<HH", 0x0409, 1200)), text=True)
    return block("VS_VERSION_INFO", fixed, children([string_info, var_info]))


def icon_images():
    """Dependency-free multiresolution icon: TailSplit's diverging route mark."""
    images = []
    for size in (16, 32, 48, 256):
        pixels = bytearray()
        for y in reversed(range(size)):
            for x in range(size):
                px, py = (x + .5) / size, (y + .5) / size
                inside = .05 < px < .95 and .05 < py < .95 and (
                    .2 <= px <= .8 or .2 <= py <= .8 or
                    math.hypot(px - (.2 if px < .5 else .8), py - (.2 if py < .5 else .8)) <= .15)
                # A trunk splitting toward two peers, rendered at each native size.
                trunk = abs(px - .5) < .065 and .44 <= py <= .8
                arms = .23 <= py <= .54 and (abs(px - (.5 - (.54 - py) * .8)) < .065 or
                                              abs(px - (.5 + (.54 - py) * .8)) < .065)
                red, green, blue, alpha = ((246, 250, 255, 255) if inside and (trunk or arms)
                                           else (30, 85, 142, 255) if inside else (0, 0, 0, 0))
                pixels.extend((blue, green, red, alpha))
        mask_stride = ((size + 31) // 32) * 4
        mask = bytearray(mask_stride * size)
        for row in range(size):
            for column in range(size):
                if not pixels[(row * size + column) * 4 + 3]:
                    mask[row * mask_stride + column // 8] |= 0x80 >> (column % 8)
        dib = struct.pack("<IiiHHIIiiII", 40, size, size * 2, 1, 32, 0, len(pixels), 0, 0, 0, 0)
        images.append((size, dib + pixels + mask))
    return images


def write_resources(executable, icon_path, info, build_number):
    images = icon_images()
    ico_header = struct.pack("<HHH", 0, 1, len(images))
    ico_entries, group_entries, image_data = b"", b"", b""
    offset = 6 + len(images) * 16
    for number, (size, data) in enumerate(images, start=1):
        entry = struct.pack("<BBBBHHI", size % 256, size % 256, 0, 0, 1, 32, len(data))
        ico_entries += entry + struct.pack("<I", offset)
        group_entries += entry + struct.pack("<H", number)
        offset += len(data)
        image_data += data
    Path(icon_path).write_bytes(ico_header + ico_entries + image_data)
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.BeginUpdateResourceW.argtypes = [wintypes.LPCWSTR, wintypes.BOOL]
    kernel.BeginUpdateResourceW.restype = wintypes.HANDLE
    kernel.UpdateResourceW.argtypes = [wintypes.HANDLE, ctypes.c_void_p, ctypes.c_void_p,
                                      wintypes.WORD, ctypes.c_void_p, wintypes.DWORD]
    kernel.UpdateResourceW.restype = wintypes.BOOL
    kernel.EndUpdateResourceW.argtypes = [wintypes.HANDLE, wintypes.BOOL]
    kernel.EndUpdateResourceW.restype = wintypes.BOOL
    handle = kernel.BeginUpdateResourceW(str(executable), False)
    if not handle:
        raise BuildError("Cannot add native Windows GUI resources")
    try:
        resources = [(3, number, data) for number, (_, data) in enumerate(images, 1)]
        resources += [(14, 1, ico_header + group_entries), (24, 1, manifest_bytes()),
                      (16, 1, version_resource(info, build_number))]
        for kind, number, data in resources:
            buffer = ctypes.create_string_buffer(data)
            if not kernel.UpdateResourceW(handle, kind, number, 0x0409, buffer, len(data)):
                raise BuildError("Cannot write a native Windows GUI resource")
    except BaseException:
        kernel.EndUpdateResourceW(handle, True)
        raise
    if not kernel.EndUpdateResourceW(handle, False):
        raise BuildError("Cannot commit native Windows GUI resources")


def stage_metadata(stage, core, notices, sdk_license_path, info, sdk_version):
    shutil.copyfile(core, Path(stage) / "tailsplit-core.exe")
    for name in ("config.example.yaml", "LICENSE"):
        shutil.copyfile(ROOT / name, Path(stage) / name)
    append_notices(notices, Path(stage) / "THIRD_PARTY_NOTICES.md", sdk_license_path, sdk_version)
    (Path(stage) / "build.json").write_text(json.dumps(info, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def build(args):
    info = identity(args.arch, args.version, args.commit, args.build_number)
    if native_arch() != args.arch:
        raise BuildError("Use a Windows runner whose native architecture matches --arch")
    core, notices = args.core.resolve(strict=True), args.notices.resolve(strict=True)
    if not core.is_file() or not notices.is_file():
        raise BuildError("--core and --notices must be existing files")
    require_machine(core, args.arch)
    output = args.directory.resolve()
    output.mkdir(parents=True, exist_ok=True)
    artifact = output / artifact_name(info)
    if artifact.exists():
        raise BuildError("Refusing to replace an existing desktop installer")
    toolchain = json.loads((HERE / "toolchain.json").read_text(encoding="utf-8"))
    environment = clean_environment()
    environment.update(GOWORK="off", GOOS="windows", GOARCH=args.arch, CGO_ENABLED="0")
    environment["GOAMD64" if args.arch == "amd64" else "GOARM64"] = "v1" if args.arch == "amd64" else "v8.0"
    compiler, compiler_environment = msvc_toolchain(args.arch, environment)
    # Never use an archive directory as cwd or as Python's import directory.
    with tempfile.TemporaryDirectory(prefix="tailsplit-windows-") as temporary:
        work = Path(temporary)
        sdk, license_path = prepare_sdk(work, toolchain)
        inno = prepare_inno(work, toolchain)
        runtime, runtime_version = prepare_runtime(work, args.arch, toolchain)
        stage = work / "payload"
        stage.mkdir()
        stage_metadata(stage, core, notices, license_path, info, toolchain["webview2_sdk"]["version"])
        gui = stage / "TailSplit.exe"
        subprocess.run(gui_command(environment.get("GO", "go"), gui, info), cwd=ROOT, env=environment, check=True)
        require_machine(gui, args.arch)
        icon = work / "TailSplit.ico"
        write_resources(gui, icon, info, args.build_number)
        require_machine(gui, args.arch)
        native_dll = stage / "TailSplit.Window.dll"
        subprocess.run(dll_command(compiler, HERE / "window.cpp", sdk, native_dll, args.arch),
                       cwd=ROOT, env=compiler_environment, check=True)
        require_machine(native_dll, args.arch)
        # Compiler intermediates never enter [Files].
        for name in PAYLOAD:
            if not (stage / name).is_file():
                raise BuildError("Desktop payload is incomplete: " + name)
        built = work / "installer"
        built.mkdir()
        definitions = {"AppVersion": info["version"], "AppFileVersion": info["version"] + "." + str(args.build_number),
                       "TargetArch": args.arch, "StageDir": str(stage), "OutputDir": str(built),
                       "OutputBaseName": artifact.stem, "IconFile": str(icon), "RuntimeFile": str(runtime),
                       "RuntimeSHA256": sha256(runtime), "RuntimeVersion": runtime_version}
        command = [str(inno), "/Qp"] + ["/D" + key + "=" + value for key, value in definitions.items()] + [str(HERE / "setup.iss")]
        subprocess.run(command, cwd=ROOT, env=environment, check=True)
        installer = built / artifact.name
        if not installer.is_file() or not installer.stat().st_size:
            raise BuildError("Inno did not produce the expected installer")
        # Exclusive creation also prevents concurrent builders replacing an asset.
        created = False
        try:
            with installer.open("rb") as source, artifact.open("xb") as destination:
                created = True
                shutil.copyfileobj(source, destination)
        except BaseException:
            if created:
                artifact.unlink(missing_ok=True)
            raise
    return dict(info, assets={artifact.name: {"path": str(artifact), "sha256": sha256(artifact),
                                            "size": artifact.stat().st_size}})


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arch", choices=tuple(MACHINES), required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--core", type=Path, required=True)
    parser.add_argument("--notices", type=Path, required=True)
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--build-number", type=int, default=0)
    args = parser.parse_args(argv)
    try:
        result = build(args)
    except (BuildError, OSError, ValueError, subprocess.SubprocessError) as error:
        # Do not dump environments, request headers, or credential-bearing URLs.
        print("Windows desktop packaging failed: " + str(error), file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
