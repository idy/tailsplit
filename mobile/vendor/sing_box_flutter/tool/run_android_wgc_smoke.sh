#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$root/.build/android-sdk}}"
adb="$sdk/platform-tools/adb"
emulator="$sdk/emulator/emulator"
avdmanager="$sdk/cmdline-tools/latest/bin/avdmanager"
cc="$sdk/ndk/28.0.13004108/toolchains/llvm/prebuilt/linux-x86_64/bin/x86_64-linux-android24-clang"
for executable in "$adb" "$emulator" "$avdmanager" "$cc"; do
  if [[ ! -x "$executable" ]]; then
    printf 'Android smoke requires installed SDK/NDK tool: %s\n' "$executable" >&2
    exit 1
  fi
done
command -v timeout >/dev/null
if [[ ! -r /dev/kvm || ! -w /dev/kvm ]]; then
  printf 'Android smoke requires read/write access to /dev/kvm.\n' >&2
  exit 1
fi

logs="$root/.build/logs"
binary="$root/.build/android-wgc-smoke/wgc.test"
mkdir -p "$logs" "$(dirname "$binary")"
unset TS_PERMIT_TOOLCHAIN_MISMATCH
# Re-extract the preceding build's archive after checking the repository pin.
# A cache-local executable hash or receipt is not a compiler trust anchor.
python3 -I -B - "$root" "$binary" "$cc" <<'PY'
from pathlib import Path
import subprocess
import sys

root, binary, cc = map(Path, sys.argv[1:])
sys.path.insert(0, str(root / "tool"))
from build_libbox import bootstrap_go
from native_common import load_pins, project_lock

with project_lock(root):
    go, env, _ = bootstrap_go(load_pins(), root / ".build/bootstrap",
                              root / ".build/cache", offline=True)
    env.update(GOOS="android", GOARCH="amd64", CGO_ENABLED="1", CC=str(cc))
    print(f"Compile Android/amd64 smoke with {go} and {cc}", flush=True)
    subprocess.run([str(go), "test", "-c", "-tags=with_gvisor", "-o", str(binary), "."],
                   cwd=root / "native/wgc", env=env, check=True, timeout=600)
PY

export ANDROID_HOME="$sdk" ANDROID_SDK_ROOT="$sdk"
run_dir="$(mktemp -d "$root/.build/android-wgc-run.XXXXXXXX")"
export ANDROID_USER_HOME="$run_dir/user"
export ANDROID_AVD_HOME="$ANDROID_USER_HOME/avd"
mkdir -p "$ANDROID_AVD_HOME"
avd=wgc-smoke
serial="emulator-5554"
launch_token="$(python3 -I -c 'import uuid; print(uuid.uuid4().hex)')"
remote=/data/local/tmp/wgc-protection-smoke.test
emulator_pid=
adb_smoke() { timeout -k 5s 15s "$adb" -s "$serial" "$@"; }
own_emulator() {
  [[ -n "$emulator_pid" ]] && kill -0 "$emulator_pid" 2>/dev/null &&
    [[ "$(adb_smoke shell getprop qemu.sbf_smoke 2>/dev/null | tr -d '\r')" == "$launch_token" ]]
}
owned_adb() {
  if ! own_emulator; then
    printf 'Android smoke serial no longer belongs to this invocation.\n' >&2
    return 1
  fi
  adb_smoke "$@"
}
cleanup() {
  status=$?
  trap - EXIT
  set +e
  if [[ -n "$emulator_pid" ]]; then
    if own_emulator; then
      adb_smoke logcat -d > "$logs/android-wgc-logcat.log" 2>&1
    fi
    # Never kill by ADB serial: a failed launch may leave another user's device
    # at that address. Only signal the process this shell actually started.
    kill "$emulator_pid" 2>/dev/null
    shutdown_deadline=$((SECONDS + 10))
    while kill -0 "$emulator_pid" 2>/dev/null && (( SECONDS < shutdown_deadline )); do
      sleep 1
    done
    kill -KILL "$emulator_pid" 2>/dev/null
    wait "$emulator_pid" 2>/dev/null
  fi
  rm -rf -- "$run_dir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# A port collision can still happen after this check; the per-launch property
# gates all device writes, and cleanup never targets a serial.
if timeout -k 5s 15s "$adb" devices | grep -q "^${serial}[[:space:]]"; then
  printf 'Android smoke requires an unused emulator-5554; the existing device is untouched.\n' >&2
  exit 1
fi
printf 'no\n' | timeout -k 5s 60s "$avdmanager" create avd --force --name "$avd" \
  --package 'system-images;android-35;default;x86_64' > "$logs/android-wgc-avd.log" 2>&1
"$emulator" -avd "$avd" -port 5554 -prop "qemu.sbf_smoke=$launch_token" -accel on -no-window -no-audio \
  -no-boot-anim -no-snapshot -wipe-data -gpu swiftshader_indirect \
  -camera-back none -camera-front none > "$logs/android-wgc-emulator.log" 2>&1 &
emulator_pid=$!

printf 'Wait up to 180 seconds for API 35 x86_64 emulator boot.\n'
boot_deadline=$((SECONDS + 180))
until own_emulator && [[ "$(owned_adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" == 1 ]]; do
  if ! kill -0 "$emulator_pid" 2>/dev/null; then
    printf 'Android emulator exited before boot; inspect android-wgc-emulator.log.\n' >&2
    exit 1
  fi
  if (( SECONDS >= boot_deadline )); then
    printf 'Android emulator boot timed out; inspect android-wgc-emulator.log.\n' >&2
    exit 1
  fi
  sleep 2
done
owned_adb shell getprop ro.product.cpu.abi
owned_adb push "$binary" "$remote"
owned_adb shell chmod 755 "$remote"
own_emulator || exit 1
timeout -k 5s 90s "$adb" -s "$serial" shell \
  "cd /data/local/tmp && $remote -test.run '^TestAndroidUDPProtectionSmoke$' -test.v -test.timeout=60s" \
  2>&1 | tee "$logs/android-wgc-runtime.log"
# Go exits successfully even when -test.run matches nothing or the test skips.
if ! grep -q '^--- PASS: TestAndroidUDPProtectionSmoke ' "$logs/android-wgc-runtime.log"; then
  printf 'Android UDP protection smoke did not report PASS.\n' >&2
  exit 1
fi
