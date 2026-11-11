# SPDX-License-Identifier: GPL-3.0-or-later
"""Execute Kotlin network seeding and cancellation with deterministic host callbacks.

Uses the Kotlin compiler already cached by the Android build and JAVA_HOME (or
java on PATH). No SDK, JNI core, emulator, network access or VPN permission is used.
"""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
KOTLIN = ROOT / "android/src/main/kotlin/dev/idy/sing_box_flutter"

NETWORK = r'''
package android.net
import android.os.Handler
class Network(val id: Int)
class LinkProperties { val interfaceName: String? = null }
class NetworkCapabilities(private val vpn: Boolean = false) {
    fun hasCapability(value: Int) = !(vpn && value == NET_CAPABILITY_NOT_VPN)
    companion object { const val NET_CAPABILITY_INTERNET=1; const val NET_CAPABILITY_NOT_RESTRICTED=2; const val NET_CAPABILITY_NOT_VPN=3; const val NET_CAPABILITY_NOT_METERED=4 }
}
class NetworkRequest { class Builder { fun addCapability(value: Int)=this; fun build()=NetworkRequest() } }
class ConnectivityManager {
    var activeNetwork: Network? = null
    var isVPN = false
    var duringRegistration: ((NetworkCallback) -> Unit)? = null
    open class NetworkCallback {
        open fun onAvailable(network: Network) {}
        open fun onLinkPropertiesChanged(network: Network, properties: LinkProperties) {}
        open fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) {}
        open fun onLost(network: Network) {}
    }
    fun getNetworkCapabilities(network: Network) = NetworkCapabilities(isVPN)
    fun getLinkProperties(network: Network) = LinkProperties()
    fun registerBestMatchingNetworkCallback(request: NetworkRequest, callback: NetworkCallback, handler: Handler) { duringRegistration?.invoke(callback) }
    fun requestNetwork(request: NetworkRequest, callback: NetworkCallback, handler: Handler) { duringRegistration?.invoke(callback) }
    fun requestNetwork(request: NetworkRequest, callback: NetworkCallback) { duringRegistration?.invoke(callback) }
    fun unregisterNetworkCallback(callback: NetworkCallback) {}
}
'''

ANDROID_OS = r'''
package android.os
object Build { object VERSION { const val SDK_INT=35 }; object VERSION_CODES { const val S=31; const val O=26 } }
class HandlerThread(val name: String) { val looper=Any(); fun start() {}; fun quitSafely() {} }
class Handler(looper: Any) {
    // Inline callbacks intentionally reach select() before registration returns.
    fun post(task: () -> Unit) { task() }
    fun postDelayed(task: () -> Unit, delay: Long) {}
    fun removeCallbacksAndMessages(token: Any?) {}
}
'''

LIBBOX = r'''
package io.nekohasekai.libbox
interface InterfaceUpdateListener { fun updateDefaultInterface(name: String?, index: Int, expensive: Boolean, constrained: Boolean) }
'''

RUNTIME = r'''
package dev.idy.sing_box_flutter
import android.net.*
import java.util.concurrent.*
import java.util.concurrent.atomic.AtomicBoolean
class FakeServer {
    val cancelled=CountDownLatch(1)
    val release=CountDownLatch(1)
    val finished=CountDownLatch(1)
    fun close() { cancelled.countDown(); release.await(); finished.countDown() }
}
internal object NativeRuntime {
    val worker=Executors.newSingleThreadExecutor()
    val cancellation=Executors.newSingleThreadExecutor()
    var service: SingBoxVpnService? = null
    var supportsStartupCancellation=true
}
internal class SingBoxVpnService {
    val connectivity=ConnectivityManager()
    val coreLock=Any()
    var commandServer: FakeServer? = null
    val stopping=AtomicBoolean(false)
    var underlying: Array<Network>? = null
    fun <T> getSystemService(type: Class<T>): T = type.cast(connectivity)
    fun setUnderlyingNetworks(networks: Array<Network>) { underlying=networks }
    fun shutdownOnWorker(message: String) { error(message) }
    // INTERRUPT_IMPLEMENTATION
}
fun main() {
    for (lost in listOf(false, true)) {
        val service=SingBoxVpnService()
        val older=Network(1)
        val newer=Network(2)
        service.connectivity.activeNetwork=older
        service.connectivity.duringRegistration = { callback ->
            callback.onAvailable(newer)
            if (lost) callback.onLost(newer)
        }
        val monitor=DefaultNetworkMonitor(service)
        monitor.start()
        check(monitor.currentNetwork === if (lost) null else newer) { "startup seed overwrote a newer callback" }
        check(service.underlying?.toList() == if (lost) emptyList<Network>() else listOf(newer))
        monitor.close()
    }
    val vpnService=SingBoxVpnService()
    vpnService.connectivity.activeNetwork=Network(3)
    vpnService.connectivity.isVPN=true
    val vpnMonitor=DefaultNetworkMonitor(vpnService)
    vpnMonitor.start()
    check(vpnMonitor.currentNetwork == null) { "seed selected a VPN" }
    vpnMonitor.close()
    val service=SingBoxVpnService()
    val server=FakeServer()
    service.commandServer=server
    val entered=CountDownLatch(1)
    val startup=NativeRuntime.worker.submit { entered.countDown(); server.release.await() }
    check(entered.await(3, TimeUnit.SECONDS))
    service.interruptStartup()
    check(server.cancelled.await(3, TimeUnit.SECONDS)) { "cancellation queued behind startup" }
    check(service.stopping.get())
    check(!startup.isDone && server.finished.count == 1L) { "disposed startup before it returned" }
    service.interruptStartup() // repeated stop must be harmless
    server.release.countDown()
    startup.get(3, TimeUnit.SECONDS)
    check(server.finished.await(3, TimeUnit.SECONDS))
    NativeRuntime.worker.shutdownNow()
    NativeRuntime.cancellation.shutdownNow()
    println("Android network seed and independent cancellation checks passed")
}
'''


def cached_compiler():
    modules = Path(os.environ.get("GRADLE_USER_HOME", str(Path.home() / ".gradle"))) / "caches/modules-2/files-2.1"
    candidates = sorted((modules / "org.jetbrains.kotlin/kotlin-compiler-embeddable").glob("*/*/*.jar"),
                        key=lambda p: tuple(int(v) for v in p.parents[1].name.split(".") if v.isdigit()))
    if not candidates or not shutil.which("java"):
        return None
    compiler = candidates[-1]
    version = compiler.parents[1].name
    jars = [compiler]
    for group, artifact in (("org.jetbrains.kotlin", "kotlin-stdlib"),
                            ("org.jetbrains.kotlin", "kotlin-script-runtime"),
                            ("org.jetbrains.kotlin", "kotlin-reflect"),
                            ("org.jetbrains.kotlinx", "kotlinx-coroutines-core-jvm"),
                            ("org.jetbrains.intellij.deps", "trove4j"), ("org.jetbrains", "annotations")):
        available = sorted((modules / group / artifact).glob("*/*/*.jar"))
        preferred = [p for p in available if p.parents[1].name == version]
        if available:
            jars.append((preferred or available)[-1])
    return jars


class AndroidNetworkMonitorTest(unittest.TestCase):
    def unavailable(self, message):
        if os.environ.get("SINGBOX_REQUIRE_ANDROID_RUNTIME_TESTS") == "1":
            self.fail(message)
        self.skipTest(message)

    def test_seed_does_not_override_callback_and_stop_reaches_blocked_start(self):
        jars = cached_compiler()
        java = str(Path(os.environ["JAVA_HOME"]) / "bin/java") if os.environ.get("JAVA_HOME") else shutil.which("java")
        if jars is None or java is None:
            self.unavailable("cached Android Kotlin compiler and Java required")
        if subprocess.run([java, "-version"], capture_output=True, timeout=10).returncode:
            self.unavailable("working Java runtime required (set JAVA_HOME)")
        classpath = os.pathsep.join(str(p) for p in jars)
        production = (KOTLIN / "SingBoxVpnService.kt").read_text()
        interrupt = "    internal fun interruptStartup() {" + production.split("    internal fun interruptStartup() {", 1)[1].split("    internal fun requestStopOnWorker", 1)[0]
        with tempfile.TemporaryDirectory(prefix="android-runtime-") as directory:
            build = Path(directory)
            sources = []
            for name, code in (("Network", NETWORK), ("AndroidOS", ANDROID_OS), ("Libbox", LIBBOX),
                               ("Runtime", RUNTIME.replace("    // INTERRUPT_IMPLEMENTATION", interrupt))):
                path = build / f"{name}.kt"
                path.write_text(code)
                sources.append(str(path))
            output = build / "tests.jar"
            subprocess.run([java, "-cp", classpath, "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler",
                            "-no-stdlib", "-no-reflect", "-nowarn", "-classpath", classpath, "-jvm-target", "17",
                            "-d", str(output), *sources, str(KOTLIN / "DefaultNetworkMonitor.kt")], check=True, timeout=60)
            subprocess.run([java, "-cp", str(output) + os.pathsep + classpath,
                            "dev.idy.sing_box_flutter.RuntimeKt"], check=True, timeout=15)


if __name__ == "__main__":
    unittest.main()
