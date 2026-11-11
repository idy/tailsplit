// SPDX-License-Identifier: GPL-3.0-or-later
// Test-only module: all system operations are manually completed; no NE API runs.
import Foundation

public enum NEVPNStatus { case invalid, disconnected, connecting, connected, reasserting, disconnecting }
public extension Notification.Name { static let NEVPNStatusDidChange = Notification.Name("StubVPNStatusDidChange") }
public class NEVPNConnection: NSObject {
    public var status: NEVPNStatus = .disconnected
    public var stopCalls = 0
    public func stopVPNTunnel() { stopCalls += 1 }
    public func report(_ status: NEVPNStatus) {
        self.status = status
        NotificationCenter.default.post(name: .NEVPNStatusDidChange, object: self)
    }
}
public final class NETunnelProviderSession: NEVPNConnection {
    public var startCalls = 0
    public var startError: Error?
    public var messages: [(Data?) -> Void] = []
    public func startVPNTunnel(options: [String: NSObject]?) throws {
        if let startError { throw startError }
        startCalls += 1
        status = .connecting
    }
    public func sendProviderMessage(_ message: Data, responseHandler: @escaping (Data?) -> Void) throws {
        messages.append(responseHandler)
    }
}
public class NEVPNProtocol: NSObject {}
public final class NETunnelProviderProtocol: NEVPNProtocol {
    public var providerBundleIdentifier: String?
    public var providerConfiguration: [String: Any]?
    public var serverAddress: String?
    public var disconnectOnSleep = true
}
public class NEOnDemandRule: NSObject {}
public final class NETunnelProviderManager: NSObject {
    public typealias Load = ([NETunnelProviderManager]?, Error?) -> Void
    public static var loads: [Load] = []
    public static var saves: [(NETunnelProviderManager, (Error?) -> Void)] = []
    public static var reloads: [(NETunnelProviderManager, (Error?) -> Void)] = []
    public var protocolConfiguration: NEVPNProtocol?
    public var localizedDescription: String?
    public var isEnabled = false
    public var isOnDemandEnabled = true
    public var onDemandRules: [NEOnDemandRule]? = [NEOnDemandRule()]
    public var connection: NEVPNConnection = NETunnelProviderSession()
    public static func loadAllFromPreferences(completionHandler: @escaping Load) { loads.append(completionHandler) }
    public func saveToPreferences(completionHandler: @escaping (Error?) -> Void) { Self.saves.append((self, completionHandler)) }
    public func loadFromPreferences(completionHandler: @escaping (Error?) -> Void) { Self.reloads.append((self, completionHandler)) }
    public static func reset() { loads = []; saves = []; reloads = [] }

    // NE reads return separate manager snapshots, with an OS-owned connection.
    public func snapshot() -> NETunnelProviderManager {
        let result = NETunnelProviderManager()
        if let source = protocolConfiguration as? NETunnelProviderProtocol {
            let tunnel = NETunnelProviderProtocol()
            tunnel.providerBundleIdentifier = source.providerBundleIdentifier
            tunnel.providerConfiguration = source.providerConfiguration
            tunnel.serverAddress = source.serverAddress
            tunnel.disconnectOnSleep = source.disconnectOnSleep
            result.protocolConfiguration = tunnel
        }
        result.localizedDescription = localizedDescription
        result.isEnabled = isEnabled
        result.isOnDemandEnabled = isOnDemandEnabled
        result.onDemandRules = onDemandRules
        result.connection = connection
        return result
    }
}
