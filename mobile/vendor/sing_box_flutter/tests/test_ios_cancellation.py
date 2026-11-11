# SPDX-License-Identifier: GPL-3.0-or-later
"""Run the actual iOS host plugin against inert Swift OS/channel stubs on macOS.

No VPN, App Group entitlement, Flutter engine, credentials or device is needed.
This checks command/callback ordering, not Apple's preference or tunnel behavior.
Run: python3 -I tests/test_ios_cancellation.py
"""
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
IOS = ROOT / "ios"
SOURCE = IOS / "sing_box_flutter/Sources/sing_box_flutter"
STUBS = IOS / "Tests/CancellationStubs"


@unittest.skipUnless(platform.system() == "Darwin" and shutil.which("xcrun"), "macOS Swift compiler required")
class IOSCancellationTest(unittest.TestCase):
    def test_native_command_cancellation(self):
        with tempfile.TemporaryDirectory(prefix="sing-box-native-cancellation-") as directory:
            build = Path(directory)
            for module in ("Flutter", "NetworkExtension"):
                subprocess.run([
                    "xcrun", "swiftc", "-swift-version", "5", "-emit-library", "-emit-module",
                    "-module-name", module, "-emit-module-path", str(build / f"{module}.swiftmodule"),
                    str(STUBS / f"{module}.swift"), "-o", str(build / f"lib{module}.dylib"),
                ], check=True, timeout=60)
            executable = build / "cancellation-smoke"
            subprocess.run([
                "xcrun", "swiftc", "-swift-version", "5", "-I", str(build), "-L", str(build),
                "-lFlutter", "-lNetworkExtension", "-Xlinker", "-rpath", "-Xlinker", str(build),
                str(SOURCE / "NativeCommandQueue.swift"), str(SOURCE / "SingBoxFlutterPlugin.swift"),
                str(IOS / "Classes/Shared/SharedTypes.swift"), str(STUBS / "Runtime.swift"),
                str(IOS / "Tests/CancellationSmoke.swift"), "-o", str(executable),
            ], check=True, timeout=60)
            # Verify the executable uses our inert module, never Apple's framework.
            linked = subprocess.run(["otool", "-L", str(executable)], check=True, text=True, capture_output=True).stdout
            self.assertNotIn("NetworkExtension.framework", linked)
            self.assertIn("libNetworkExtension.dylib", linked)
            subprocess.run([str(executable)], check=True, timeout=20)

    def test_plugin_source_glob_contains_command_helper(self):
        spec = (IOS / "sing_box_flutter.podspec").read_text()
        self.assertIn("plugin.source_files = 'sing_box_flutter/Sources/sing_box_flutter/**/*.swift'", spec)
        self.assertTrue((SOURCE / "NativeCommandQueue.swift").is_file())


if __name__ == "__main__":
    unittest.main()
