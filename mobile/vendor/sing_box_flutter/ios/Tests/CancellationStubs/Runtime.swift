// SPDX-License-Identifier: GPL-3.0-or-later
// Test-only in-memory storage and preflight. Never accesses App Groups or keys.
import Foundation

final class TunnelStorage {
    static var configurationWrites = 0
    static var statusWrites: [TunnelStatus] = []
    static var savedStatus: TunnelStatus?
    static var sessionID: String? = "fixture"
    let appGroup: String
    init(appGroup: String) throws { self.appGroup = appGroup }
    func saveConfiguration(_ config: String, name: String, coreFingerprint: String) throws -> TunnelConfiguration {
        Self.configurationWrites += 1
        let sessionID = "fixture-\(Self.configurationWrites)"
        Self.sessionID = sessionID
        return TunnelConfiguration(version: 1, sessionID: sessionID, config: config, name: name, coreFingerprint: coreFingerprint)
    }
    func writeStatus(_ status: TunnelStatus) throws { Self.statusWrites.append(status); Self.savedStatus = status }
    func readStatus() -> TunnelStatus? { Self.savedStatus }
    func currentSessionID() -> String? { Self.sessionID }
}
struct CoreCapabilities {
    let version = "fixture-core"
    let fingerprint = "fixture-fingerprint"
    static var failure: TunnelFailure?
    static func load() throws -> CoreCapabilities {
        if let failure { throw failure }
        return CoreCapabilities()
    }
}
enum ConfigPreflight {
    static func check(_ config: String, capabilities: CoreCapabilities, requireTUN: Bool = false) throws {
        if config == "invalid" { throw TunnelFailure.invalidConfig }
    }
}
