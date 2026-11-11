# SPDX-License-Identifier: GPL-3.0-or-later
"""Execute provider lifecycle and DNS refresh code against inert Swift dependencies."""
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
IOS = ROOT / "ios"

NETWORK = r'''
import Foundation
public enum NEProviderStopReason { case userInitiated }
public class NEVPNProtocol: NSObject {}
public class NETunnelProviderProtocol: NEVPNProtocol { public var providerConfiguration: [String: Any]? }
public class NEPacketTunnelNetworkSettings: NSObject {}
open class NEPacketTunnelProvider: NSObject {
    public var protocolConfiguration: NEVPNProtocol = NETunnelProviderProtocol()
    public var reasserting = false
    open func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {}
    open func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {}
    open func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {}
    open func sleep(completionHandler: @escaping () -> Void) {}
    open func wake() {}
    public func setTunnelNetworkSettings(_ value: NEPacketTunnelNetworkSettings?, completionHandler: @escaping (Error?) -> Void) { completionHandler(nil) }
    public func cancelTunnelWithError(_ error: Error?) {}
}
'''

LIBBOX = r'''
import Foundation
public class LibboxOverrideOptions: NSObject {}
public final class LibboxCommandServer: NSObject {
    public let started = DispatchSemaphore(value: 0)
    public let cancelled = DispatchSemaphore(value: 0)
    public let release = DispatchSemaphore(value: 0)
    private let finished = DispatchGroup()
    private let lock = NSLock()
    private var closed = false
    public override init() { super.init(); finished.enter() }
    public func startOrReloadService(_ config: String, options: LibboxOverrideOptions) throws {
        started.signal()
        release.wait() // Deliberately ignores cancellation until the test releases it.
        finished.leave()
    }
    public func close() {
        lock.lock()
        if !closed { closed = true; cancelled.signal() }
        lock.unlock()
        finished.wait() // Mirrors the Go binding: cancellation first, cleanup after Start.
    }
    public func closeService() throws { close() }
    public func pause() {}
    public func wake() {}
}
public enum TestCore { public static var server = LibboxCommandServer() }
public func LibboxNewCommandServer(_ handler: AnyObject, _ platform: AnyObject, _ error: inout NSError?) -> LibboxCommandServer? { TestCore.server }
public func LibboxVersion() -> String { "test" }
'''

RUNTIME = r'''
import Foundation
import NetworkExtension
final class TunnelStorage {
    static let lock = NSLock()
    static var states: [String] = []
    init(appGroup: String) throws {}
    func currentSessionID() -> String { "session" }
    func loadConfiguration() throws -> TunnelConfiguration { TunnelConfiguration(version: 1, sessionID: "session", config: "{}", name: "test", coreFingerprint: nil) }
    func writeStatus(_ status: TunnelStatus) throws { Self.lock.lock(); Self.states.append(status.state); Self.lock.unlock() }
    static func snapshot() -> [String] { lock.lock(); defer { lock.unlock() }; return states }
}
enum CoreCapabilities { static func load() throws -> CoreCapabilities { .test }; case test }
enum ConfigPreflight { static func check(_ config: String, capabilities: CoreCapabilities, requireTUN: Bool) throws {} }
enum NativeCore {
    static func verify(_ capabilities: CoreCapabilities) throws {}
    static func verifySession(_ fingerprint: String?, capabilities: CoreCapabilities) throws {}
    static func setup(storage: TunnelStorage) throws {}
    static func check(_ config: String) throws {}
}
final class ApplePlatformInterface: NSObject {
    weak var provider: SingBoxPacketTunnelProvider?
    let lock = NSLock()
    let settingsQueue = DispatchQueue(label: "test-settings")
    var networkSettings: NEPacketTunnelNetworkSettings? = NEPacketTunnelNetworkSettings()
    var applied: [Bool] = []
    var hasTunnel: Bool { true }
    static let resetLock = NSLock()
    static var resets = 0
    init(provider: SingBoxPacketTunnelProvider) { self.provider = provider }
    func apply(_ settings: NEPacketTunnelNetworkSettings?, to provider: SingBoxPacketTunnelProvider) throws { applied.append(settings != nil) }
    func reset() { Self.resetLock.lock(); Self.resets += 1; Self.resetLock.unlock() }
    static func resetCount() -> Int { resetLock.lock(); defer { resetLock.unlock() }; return resets }
    // CLEAR_DNS_IMPLEMENTATION
}
'''

SMOKE = r'''
import Foundation
import NetworkExtension
import Libbox
@main struct RuntimeSmoke {
    static func wait(_ signal: DispatchSemaphore) { precondition(signal.wait(timeout: .now() + 3) == .success, "timed out") }
    static func main() {
        let provider = SingBoxPacketTunnelProvider()
        let config = NETunnelProviderProtocol()
        config.providerConfiguration = ["appGroup": "test"]
        provider.protocolConfiguration = config
        let startDone = DispatchSemaphore(value: 0)
        let stopDone = DispatchSemaphore(value: 0)
        provider.startTunnel(options: nil) { error in precondition(error != nil, "retired startup succeeded"); startDone.signal() }
        wait(TestCore.server.started)
        provider.stopTunnel(with: .userInitiated) { stopDone.signal() }
        // This must arrive while the lifecycle queue is still blocked in Start.
        wait(TestCore.server.cancelled)
        precondition(provider.isStopRequested)
        precondition(stopDone.wait(timeout: .now() + 0.05) == .timedOut, "stop confirmed under blocked startup")
        precondition(ApplePlatformInterface.resetCount() == 0, "platform closed under startup")
        TestCore.server.release.signal()
        wait(startDone)
        wait(stopDone)
        precondition(!TunnelStorage.snapshot().contains("connected"), "late startup was advertised connected")
        precondition(TunnelStorage.snapshot().last == "disconnected")
        precondition(ApplePlatformInterface.resetCount() == 1)
        precondition(!provider.isStopRequested, "confirmed stop blocked future OS starts")
        // Reuse the same provider only after confirmed cleanup; the old queued
        // Close owns its captured server and cannot cancel this replacement.
        TestCore.server = LibboxCommandServer()
        provider.startTunnel(options: nil) { error in precondition(error != nil); startDone.signal() }
        wait(TestCore.server.started)
        provider.stopTunnel(with: .userInitiated) { stopDone.signal() }
        wait(TestCore.server.cancelled)
        TestCore.server.release.signal()
        wait(startDone)
        wait(stopDone)
        precondition(ApplePlatformInterface.resetCount() == 2)
        precondition(!TunnelStorage.snapshot().contains("connected"))
        let dns = ApplePlatformInterface(provider: provider)
        dns.clearDNSCache()
        dns.settingsQueue.sync {}
        precondition(dns.applied == [true], "DNS refresh temporarily removed tunnel settings")
        print("Packet tunnel cancellation and route-preserving DNS refresh passed")
    }
}
'''


@unittest.skipUnless(platform.system() == "Darwin" and shutil.which("xcrun"), "macOS Swift compiler required")
class PacketTunnelRuntimeTest(unittest.TestCase):
    def test_blocked_startup_and_dns_refresh(self):
        # Compile the complete provider. The large platform adapter needs real NE
        # interfaces, so execute its unchanged DNS method with an inert apply hook.
        platform_source = (IOS / "Classes/PacketTunnel/ApplePlatformInterface.swift").read_text()
        clear_dns = platform_source.split("    func clearDNSCache() {", 1)[1].split("    func reset()", 1)[0]
        clear_dns = "    func clearDNSCache() {" + clear_dns
        with tempfile.TemporaryDirectory(prefix="packet-tunnel-runtime-") as directory:
            build = Path(directory)
            for module, code in (("NetworkExtension", NETWORK), ("Libbox", LIBBOX)):
                source = build / f"{module}.swift"
                source.write_text(code)
                subprocess.run(["xcrun", "swiftc", "-swift-version", "5", "-emit-library", "-emit-module",
                                "-module-name", module, "-emit-module-path", str(build / f"{module}.swiftmodule"),
                                str(source), "-o", str(build / f"lib{module}.dylib")], check=True, timeout=60)
            runtime = build / "Runtime.swift"
            runtime.write_text(RUNTIME.replace("    // CLEAR_DNS_IMPLEMENTATION", clear_dns))
            smoke = build / "Smoke.swift"
            smoke.write_text(SMOKE)
            executable = build / "runtime-smoke"
            subprocess.run(["xcrun", "swiftc", "-swift-version", "5", "-D", "SINGBOX_SELECTABLE_CORE",
                            "-I", str(build), "-L", str(build), "-lNetworkExtension", "-lLibbox",
                            "-Xlinker", "-rpath", "-Xlinker", str(build),
                            str(IOS / "Classes/Shared/SharedTypes.swift"),
                            str(IOS / "Classes/PacketTunnel/SingBoxPacketTunnelProvider.swift"),
                            str(runtime), str(smoke), "-o", str(executable)], check=True, timeout=60)
            subprocess.run([str(executable)], check=True, timeout=15)


if __name__ == "__main__":
    unittest.main()
