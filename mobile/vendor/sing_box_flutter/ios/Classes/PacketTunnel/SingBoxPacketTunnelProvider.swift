// SPDX-License-Identifier: GPL-3.0-or-later
// Uses the SagerNet sing-box-for-apple ExtensionProvider lifecycle as a reference.
// Copyright (C) 2022 nekohasekai <contact-sagernet@sekai.icu>.
import Foundation
import Libbox
import NetworkExtension

/// Subclass in the host's Packet Tunnel extension and select that subclass as its principal class.
/// This pod has no Flutter dependency. Only this extension process starts the sing-box service.
open class SingBoxPacketTunnelProvider: NEPacketTunnelProvider {
    private let coreQueue = DispatchQueue(label: "dev.idy.sing_box_flutter.packet-tunnel")
    private let cancellationQueue = DispatchQueue(label: "dev.idy.sing_box_flutter.packet-tunnel-cancel")
    private let coreLock = NSLock()
    private var stopRequested = false
    private var storage: TunnelStorage?
    private var sessionID: String?
    private var commandServer: LibboxCommandServer?
    private var platform: ApplePlatformInterface?
    private var status = TunnelStatus("disconnected")
    private var transitioning = false

    override open func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        coreQueue.async {
            guard self.commandServer == nil, !self.transitioning, !self.isStopRequested else { completionHandler(TunnelFailure.busy); return }
            self.transitioning = true
            do {
                guard let configuration = self.protocolConfiguration as? NETunnelProviderProtocol,
                      let group = configuration.providerConfiguration?["appGroup"] as? String else {
                    throw TunnelFailure(code: "missing_app_group", message: "The VPN configuration does not contain an App Group identifier.")
                }
                let storage = try TunnelStorage(appGroup: group)
                self.storage = storage
                self.sessionID = storage.currentSessionID()
                // Loading migrates sessions saved before session.json existed. Associate
                // that ID before any check can fail, so the app accepts the error status.
                let saved = try storage.loadConfiguration()
                self.sessionID = saved.sessionID
                let capabilities = try CoreCapabilities.load()
                try NativeCore.verify(capabilities)
                try NativeCore.verifySession(saved.coreFingerprint, capabilities: capabilities)
                if let requestedSession = options?["sessionID"] as? String, requestedSession != saved.sessionID {
                    throw TunnelFailure(code: "stale_configuration", message: "The requested VPN configuration was replaced before startup.")
                }
                self.setStatus("connecting")
                try ConfigPreflight.check(saved.config, capabilities: capabilities, requireTUN: true)
                try NativeCore.setup(storage: storage)
                try NativeCore.check(saved.config)
                let platform = ApplePlatformInterface(provider: self)
                self.platform = platform
                var error: NSError?
                guard let server = LibboxNewCommandServer(platform, platform, &error), error == nil else {
                    throw TunnelFailure(code: "core_create_failed", message: "libbox could not create the tunnel service.")
                }
                self.coreLock.lock()
                let stopped = self.stopRequested
                if !stopped { self.commandServer = server }
                self.coreLock.unlock()
                guard !stopped else {
                    server.close()
                    throw TunnelFailure(code: "start_cancelled", message: "VPN startup was cancelled.")
                }
                // StartOrReloadService owns the core independently of Start's RPC listener.
                // App communication uses NE provider messages, so no Unix/TCP command
                // listener is opened (App Group paths can also exceed Unix socket limits).
                try server.startOrReloadService(saved.config, options: LibboxOverrideOptions())
                // A routing-only / proxy-only config must not masquerade as a packet tunnel.
                guard platform.hasTunnel else {
                    throw TunnelFailure(code: "missing_tun", message: "The configuration must start a TUN inbound on iOS.")
                }
                self.coreLock.lock()
                let cancelled = self.stopRequested
                if !cancelled { self.setStatus("connected") }
                self.coreLock.unlock()
                guard !cancelled else { throw TunnelFailure(code: "start_cancelled", message: "VPN startup was cancelled.") }
                self.transitioning = false
                completionHandler(nil)
            } catch {
                let failure = error as? TunnelFailure ?? TunnelFailure(code: "core_start_failed", message: "sing-box could not start the tunnel. Check the configuration and extension capabilities.")
                if self.isStopRequested { self.setStatus("disconnecting") }
                else { self.setStatus("error", message: failure.message) }
                self.closeCore()
                self.setTunnelNetworkSettings(nil) { _ in
                    self.coreQueue.async {
                        self.transitioning = false
                        completionHandler(failure)
                    }
                }
            }
        }
    }

    override open func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        interruptStartup()
        coreQueue.async {
            // Preserve an actionable startup failure when the OS follows it with stopTunnel.
            self.transitioning = true
            let preserveError = self.status.state == "error"
            if !preserveError { self.setStatus("disconnecting") }
            let closeFailed = self.closeCore()
            self.setTunnelNetworkSettings(nil) { _ in
                self.coreQueue.async {
                    if !preserveError {
                        self.setStatus(closeFailed ? "error" : "disconnected", message: closeFailed ? "sing-box reported an error while stopping the tunnel." : nil)
                    }
                    self.transitioning = false
                    // A later OS start may reuse this provider, but only after
                    // the old startup and resource teardown have both finished.
                    self.coreLock.lock()
                    self.stopRequested = false
                    self.coreLock.unlock()
                    completionHandler()
                }
            }
        }
    }

    /// Small, versioned messages are used only for control/status, never configuration or raw logs.
    override open func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        coreQueue.async {
            guard messageData.count <= 4096,
                  let request = try? JSONSerialization.jsonObject(with: messageData) as? [String: Any],
                  request["version"] as? Int == 1,
                  let command = request["command"] as? String else {
                completionHandler?(self.messageError("invalid_message"))
                return
            }
            switch command {
            case "getStatus": completionHandler?(try? JSONEncoder().encode(self.status))
            case "getVersion":
                completionHandler?(try? JSONSerialization.data(withJSONObject: ["version": LibboxVersion()]))
            default: completionHandler?(self.messageError("unsupported"))
            }
        }
    }

    override open func sleep(completionHandler: @escaping () -> Void) {
        coreQueue.async {
            self.commandServer?.pause()
            completionHandler()
        }
    }

    override open func wake() {
        coreQueue.async { self.commandServer?.wake() }
    }

    func stopFromCore() {
        // A libbox callback may be holding Go service locks. Never synchronously re-enter it.
        interruptStartup()
        coreQueue.async {
            self.closeCore()
            self.setStatus("disconnected")
            self.cancelTunnelWithError(nil)
        }
    }

    func failFromPlatform() {
        interruptStartup()
        coreQueue.async {
            let failure = TunnelFailure(code: "network_settings_failed", message: "The system could not restore the tunnel's network settings.")
            self.setStatus("error", message: failure.message)
            self.closeCore()
            self.cancelTunnelWithError(failure)
        }
    }

    var isStopRequested: Bool {
        coreLock.lock(); defer { coreLock.unlock() }
        return stopRequested
    }

    private func interruptStartup() {
        coreLock.lock(); defer { coreLock.unlock() }
        guard !stopRequested else { return }
        stopRequested = true
        #if SINGBOX_SELECTABLE_CORE
        // This binding cancels its context before waiting for Start to return.
        // It never closes engine resources under Start. Legacy Close has no such
        // guarantee and must remain serialized. OS teardown still waits below.
        if let server = commandServer {
            cancellationQueue.async { server.close() }
        }
        #endif
    }

    @discardableResult private func closeCore() -> Bool {
        coreLock.lock()
        let server = commandServer
        commandServer = nil
        let cancelled = stopRequested
        coreLock.unlock()
        var failed = false
        if let server {
            if !cancelled {
                do { try server.closeService() } catch { failed = true }
            }
            server.close()
        }
        platform?.reset()
        platform = nil
        reasserting = false
        return failed
    }

    private func setStatus(_ state: String, message: String? = nil) {
        status = TunnelStatus(state, message: message, sessionID: sessionID)
        try? storage?.writeStatus(status)
    }

    private func messageError(_ code: String) -> Data? {
        try? JSONSerialization.data(withJSONObject: ["error": code])
    }
}
