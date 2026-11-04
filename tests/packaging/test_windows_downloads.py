#!/usr/bin/env python3
"""GitHub authentication boundaries for Windows dependency downloads."""
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


packager = load("windows_download_packager", "packaging/windows/build.py")
fixture = load("windows_download_fixture", "tests/packaging/windows_desktop.py")
API = "https://api.github.com/repos/jrsoftware/issrc/releases/tags/is-7_1_1"


class DownloadAuthenticationTests(unittest.TestCase):
    def request(self, url, environment):
        response = io.BytesIO(b"downloaded data")
        response.geturl = lambda: url
        with tempfile.TemporaryDirectory() as temporary, \
                patch.dict(os.environ, environment, clear=True), \
                patch.object(packager, "build_opener") as opener:
            opener.return_value.open.return_value = response
            result = packager.fetch(url, Path(temporary) / "payload")
            self.assertEqual(result.read_bytes(), b"downloaded data")
            return opener.return_value.open.call_args.args[0]

    def test_github_api_uses_available_token_and_prefers_gh_token(self):
        cases = [({"GH_TOKEN": "ci-token", "GITHUB_TOKEN": "fallback-token"}, "ci-token"),
                 ({"GITHUB_TOKEN": "fallback-token"}, "fallback-token"),
                 ({"GH_TOKEN": "", "GITHUB_TOKEN": "fallback-token"}, "fallback-token")]
        for environment, expected in cases:
            with self.subTest(environment=environment):
                request = self.request(API, environment)
                self.assertEqual(request.get_header("Authorization"), "Bearer " + expected)
                self.assertNotIn("Authorization", request.headers)

    def test_local_builds_without_a_token_remain_supported(self):
        self.assertIsNone(self.request(API, {}).get_header("Authorization"))

    def test_vendor_and_asset_hosts_never_receive_github_credentials(self):
        urls = ["https://github.com/jrsoftware/issrc/releases/download/tag/setup.exe",
                "https://release-assets.githubusercontent.com/setup.exe",
                "https://objects.githubusercontent.com/setup.exe",
                "https://api.nuget.org/v3-flatcontainer/package.nupkg",
                "https://globalcdn.nuget.org/package.nupkg",
                "https://go.microsoft.com/fwlink/?linkid=2099616"]
        for url in urls:
            with self.subTest(url=url):
                self.assertIsNone(self.request(url, {"GH_TOKEN": "ci-token"}).get_header("Authorization"))

    def test_redirects_drop_authorization_including_same_host_redirects(self):
        request = self.request(API, {"GH_TOKEN": "ci-token"})
        for url in [API + "?redirected=1", "https://github.com/asset.exe",
                    "https://release-assets.githubusercontent.com/asset.exe",
                    "https://go.microsoft.com/download"]:
            with self.subTest(url=url):
                redirected = packager.OfficialRedirects().redirect_request(request, None, 302, "Found", {}, url)
                self.assertIsNone(redirected.get_header("Authorization"))
        with self.assertRaises(packager.BuildError):
            packager.OfficialRedirects().redirect_request(request, None, 302, "Found", {}, "https://example.com/asset")


class FixtureAuthenticationTests(unittest.TestCase):
    def test_installed_programs_do_not_inherit_metadata_credentials(self):
        with patch.dict(os.environ, {"GH_TOKEN": "ci-token", "GITHUB_TOKEN": "fallback-token", "PATH": "tools"}, clear=True), \
                patch.object(fixture.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as execute:
            fixture.run("TailSplit.exe", "--help")
        self.assertEqual(execute.call_args.kwargs["env"], {"PATH": "tools"})

    def test_streaming_broker_does_not_inherit_metadata_credentials(self):
        with patch.dict(os.environ, {"GH_TOKEN": "ci-token", "GITHUB_TOKEN": "fallback-token", "PATH": "tools"}, clear=True), \
                patch.object(fixture.subprocess, "Popen") as execute, \
                patch.object(fixture.subprocess, "CREATE_NO_WINDOW", 0, create=True), \
                patch.object(fixture.threading, "Thread"), \
                patch.object(fixture.queue, "Queue") as records:
            records.return_value.get.return_value = {"event": "ready"}
            fixture.broker("core.exe", "config.yaml")
        self.assertEqual(execute.call_args.kwargs["env"], {"PATH": "tools"})

    def test_fixture_packager_alone_receives_metadata_credentials(self):
        tokens = {"GH_TOKEN": "ci-token", "GITHUB_TOKEN": "fallback-token"}
        with patch.dict(os.environ, tokens, clear=True), patch.object(fixture, "run") as execute:
            fixture.fixture_installer("test", "amd64", "1.2.3", "a" * 40, "core.exe", "notices.md")
        self.assertIn(ROOT / "packaging/windows/build.py", execute.call_args.args)
        self.assertEqual(execute.call_args.kwargs["extra"], tokens)


if __name__ == "__main__":
    unittest.main()
