// SPDX-License-Identifier: GPL-3.0-or-later
import Flutter
import Foundation
import NetworkExtension

/// Owns system preferences and Flutter channels, never the running VPN service.
public final class SingBoxFlutterPlugin: NSObject, FlutterPlugin, FlutterStreamHandler {
    private typealias Command = NativeCommandQueue.Command
    private let commands = NativeCommandQueue()
    private let scheduling: NativeCommandScheduling
    private var storage: TunnelStorage?
    private var providerBundleIdentifier: String?
    private var notificationTitle = "sing-box"
    private var manager: NETunnelProviderManager?
    private var statusObserver: NSObjectProtocol?
    private var eventSink: FlutterEventSink?
    private var startRequested = false
    private var stopRequested = false
    private var status = TunnelStatus("disconnected")

    private struct PreferenceTarget: Hashable {
        let bundle: String
        let appGroup: String
    }
    private var knownTargets: Set<PreferenceTarget> = []
    private var preferenceWrites: [UUID: NETunnelProviderManager] = [:]
    private var stoppingManagers: [NETunnelProviderManager] = []
    // A timed-out OS read is not proof of shutdown. Keep starts fenced until an
    // explicit stop successfully reconciles preferences, even after its reply.
    private var stopNeedsReconciliation = false
    // Stop can establish native status before initialize adopts App Group storage.
    private var hasStopStatus = false

    public override convenience init() { self.init(scheduling: .live) }

    init(scheduling: NativeCommandScheduling) {
        self.scheduling = scheduling
        super.init()
    }

    public static func register(with registrar: FlutterPluginRegistrar) {
        let instance = SingBoxFlutterPlugin()
        let methods = FlutterMethodChannel(name: "dev.idy.sing_box_flutter/methods", binaryMessenger: registrar.messenger())
        let events = FlutterEventChannel(name: "dev.idy.sing_box_flutter/events", binaryMessenger: registrar.messenger())
        registrar.addMethodCallDelegate(instance, channel: methods)
        events.setStreamHandler(instance)
    }

    deinit {
        if let statusObserver { NotificationCenter.default.removeObserver(statusObserver) }
    }

    public func detachFromEngine(for registrar: FlutterPluginRegistrar) {
        eventSink = nil
        // The OS-owned tunnel deliberately survives engine teardown.
    }

    public func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
        // All manager state, command identities and Flutter results are main-queue confined.
        DispatchQueue.main.async {
            if call.method == "stop" {
                let cancelled = FlutterError(code: "operation_cancelled", message: "The operation was cancelled by a stop request.", details: nil)
                self.commands.interrupt(reply: result, cancelledValue: cancelled) { self.stop($0) }
            } else {
                self.commands.enqueue(reply: result) { self.execute(call, command: $0) }
            }
        }
    }

    private func finish(_ command: Command, _ value: Any? = nil) {
        commands.finish(command, value)
    }

    private func fail(_ command: Command, _ failure: TunnelFailure) {
        finish(command, FlutterError(code: failure.code, message: failure.message, details: nil))
    }

    private func execute(_ call: FlutterMethodCall, command: Command) {
        let arguments = call.arguments as? [String: Any] ?? [:]
        if call.method == "getVersion" {
            do { finish(command, try CoreCapabilities.load().version) }
            catch { fail(command, .coreMismatch) }
            return
        }
        if call.method == "initialize" {
            initialize(arguments, command: command)
            return
        }
        guard ["checkConfig", "requestPermission", "start", "getStatus"].contains(call.method) else {
            finish(command, FlutterMethodNotImplemented)
            return
        }
        if call.method == "getStatus" {
            // A cancelled initialize may never have committed storage. The stop
            // barrier still owns authoritative OS shutdown state, which callers
            // must be able to confirm before attempting a fresh initialize.
            guard storage != nil || hasStopStatus else { fail(command, .notInitialized); return }
            refreshStatus()
            queryProviderStatus(command: command)
            return
        }
        guard let storage else { fail(command, .notInitialized); return }
        switch call.method {
        case "checkConfig":
            guard let config = arguments["config"] as? String, !config.isEmpty else { fail(command, .invalidArguments); return }
            scheduling.validate {
                do {
                    try ConfigPreflight.check(config, capabilities: CoreCapabilities.load())
                    DispatchQueue.main.async { self.finish(command) }
                } catch let failure as TunnelFailure {
                    // Only fixed, credential-free codes/messages are ever constructed.
                    DispatchQueue.main.async { self.fail(command, failure) }
                } catch {
                    DispatchQueue.main.async { self.fail(command, .invalidConfig) }
                }
            }
        case "requestPermission":
            requestPermission(command: command)
        case "start":
            guard let config = arguments["config"] as? String, !config.isEmpty,
                  let name = arguments["name"] as? String, !name.isEmpty else { fail(command, .invalidArguments); return }
            guard !isActive else { fail(command, .busy); return }
            guard let manager, manager.isEnabled, let session = manager.connection as? NETunnelProviderSession else {
                fail(command, TunnelFailure(code: "permission_required", message: "Call requestPermission and allow the VPN configuration before starting."))
                return
            }
            scheduling.validate {
                do {
                    let capabilities = try CoreCapabilities.load()
                    try ConfigPreflight.check(config, capabilities: capabilities, requireTUN: true)
                    DispatchQueue.main.async {
                        guard self.commands.owns(command) else { return }
                        // The OS can start the saved VPN from Settings while validation runs.
                        guard !self.isActive else { self.fail(command, .busy); return }
                        let configuration: TunnelConfiguration
                        do { configuration = try storage.saveConfiguration(config, name: name, coreFingerprint: capabilities.fingerprint) }
                        catch { self.fail(command, .storage); return }
                        do {
                            self.startRequested = true
                            try session.startVPNTunnel(options: ["sessionID": configuration.sessionID as NSString])
                            self.emit(TunnelStatus("connecting"))
                            self.finish(command)
                        } catch {
                            self.startRequested = false
                            try? storage.writeStatus(TunnelStatus("error", message: "The system rejected the VPN start request.", sessionID: configuration.sessionID))
                            self.emit(TunnelStatus("error", message: "The system rejected the VPN start request."))
                            self.fail(command, TunnelFailure(code: "start_failed", message: "The system rejected the VPN start request. Check the extension target and VPN permission."))
                        }
                    }
                } catch let failure as TunnelFailure {
                    DispatchQueue.main.async { self.fail(command, failure) }
                } catch {
                    DispatchQueue.main.async { self.fail(command, .storage) }
                }
            }
        default:
            finish(command, FlutterMethodNotImplemented)
        }
    }

    private var isActive: Bool {
        if startRequested || stopRequested || stopNeedsReconciliation { return true }
        guard let state = manager?.connection.status else { return false }
        return ![NEVPNStatus.invalid, .disconnected].contains(state)
    }

    private func initialize(_ arguments: [String: Any], command: Command) {
        guard let group = arguments["appGroup"] as? String, !group.isEmpty,
              let bundle = arguments["providerBundleIdentifier"] as? String, !bundle.isEmpty,
              let title = arguments["notificationTitle"] as? String, !title.isEmpty else { fail(command, .invalidArguments); return }
        guard !stopNeedsReconciliation else { fail(command, .busy); return }
        if let storage, storage.appGroup == group, providerBundleIdentifier == bundle {
            notificationTitle = title
            refreshStatus()
            finish(command)
            return
        }
        guard !isActive else { fail(command, .busy); return }
        do {
            let newStorage = try TunnelStorage(appGroup: group)
            hasStopStatus = false
            // A stop can find the OS tunnel even if this initialization never replies.
            knownTargets.insert(PreferenceTarget(bundle: bundle, appGroup: group))
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                DispatchQueue.main.async {
                    guard self.commands.owns(command) else { return }
                    guard error == nil else {
                        self.fail(command, TunnelFailure(code: "preferences_failed", message: "VPN preferences could not be loaded. Check the host app's signing and capabilities."))
                        return
                    }
                    self.scheduling.validate {
                        do {
                            _ = try CoreCapabilities.load()
                            DispatchQueue.main.async {
                                guard self.commands.owns(command) else { return }
                                self.storage = newStorage
                                self.providerBundleIdentifier = bundle
                                self.notificationTitle = title
                                self.setManager(self.findManager(managers, bundle: bundle, appGroup: group))
                                self.refreshStatus()
                                self.finish(command)
                            }
                        } catch let failure as TunnelFailure {
                            DispatchQueue.main.async { self.fail(command, failure) }
                        } catch {
                            DispatchQueue.main.async { self.fail(command, .storage) }
                        }
                    }
                }
            }
        } catch let failure as TunnelFailure {
            fail(command, failure)
        } catch {
            fail(command, .storage)
        }
    }

    private func findManager(_ managers: [NETunnelProviderManager]?, bundle: String, appGroup: String) -> NETunnelProviderManager? {
        managers?.first { matches($0, target: PreferenceTarget(bundle: bundle, appGroup: appGroup)) }
    }

    private func matches(_ manager: NETunnelProviderManager, target: PreferenceTarget) -> Bool {
        guard let tunnel = manager.protocolConfiguration as? NETunnelProviderProtocol else { return false }
        return tunnel.providerBundleIdentifier == target.bundle && tunnel.providerConfiguration?["appGroup"] as? String == target.appGroup
    }

    private func requestPermission(command: Command) {
        guard let storage, let bundle = providerBundleIdentifier else { fail(command, .notInitialized); return }
        guard !stopNeedsReconciliation, !stopRequested else { fail(command, .busy); return }
        NETunnelProviderManager.loadAllFromPreferences { managers, error in
            DispatchQueue.main.async {
                guard self.commands.owns(command) else { return }
                guard error == nil else {
                    self.fail(command, TunnelFailure(code: "preferences_failed", message: "VPN preferences could not be loaded."))
                    return
                }
                self.reconcilePreferenceWrites(managers)
                let manager = self.findManager(managers, bundle: bundle, appGroup: storage.appGroup) ?? NETunnelProviderManager()
                if manager.isEnabled, manager.protocolConfiguration != nil {
                    self.setManager(manager)
                    self.refreshStatus()
                    self.finish(command, true)
                    return
                }
                guard !self.isActive else { self.fail(command, .busy); return }
                let target = PreferenceTarget(bundle: bundle, appGroup: storage.appGroup)
                guard !self.preferenceWrites.values.contains(where: { self.matches($0, target: target) }) else {
                    // Neither completion nor a persisted transaction was observed.
                    // NE cannot distinguish a pending save from a lost rejection
                    // callback (or an externally removed profile). A second save
                    // could create a duplicate or race the first. This fence only
                    // affects conflicting writes; stop and read/validation work.
                    self.fail(command, TunnelFailure(code: "preferences_pending", message: "A previous VPN preference save has not been confirmed. Resolve the system permission request and retry."))
                    return
                }
                let writeID = UUID()
                let tunnel = NETunnelProviderProtocol()
                tunnel.providerBundleIdentifier = bundle
                tunnel.providerConfiguration = ["appGroup": storage.appGroup, "schemaVersion": 1, "nativePreferenceWriteID": writeID.uuidString]
                tunnel.serverAddress = "sing-box"
                tunnel.disconnectOnSleep = false
                manager.protocolConfiguration = tunnel
                manager.localizedDescription = self.notificationTitle
                manager.isEnabled = true
                // A submitted NE save cannot be cancelled. Make it passive: even
                // if it commits after stop, it cannot establish an on-demand VPN.
                // The only app start is the separately guarded start command.
                manager.isOnDemandEnabled = false
                manager.onDemandRules = nil
                self.preferenceWrites[writeID] = manager
                manager.saveToPreferences { error in
                    DispatchQueue.main.async {
                        // Ledger settlement is the sole effect allowed for a
                        // retired save callback. It cannot change the live manager.
                        self.preferenceWrites.removeValue(forKey: writeID)
                        guard self.commands.owns(command) else { return }
                        if let error {
                            if self.isConsentCancellation(error as NSError) { self.finish(command, false) }
                            else { self.fail(command, TunnelFailure(code: "permission_failed", message: "The VPN configuration could not be saved. Check both targets' signing and Network Extension entitlements.")) }
                            return
                        }
                        // Keep this manager private until reload finishes. A late
                        // loadFromPreferences may mutate its receiver inside NE.
                        manager.loadFromPreferences { error in
                            DispatchQueue.main.async {
                                guard self.commands.owns(command) else { return }
                                guard error == nil else {
                                    self.fail(command, TunnelFailure(code: "preferences_failed", message: "The saved VPN configuration could not be reloaded."))
                                    return
                                }
                                self.setManager(manager)
                                self.refreshStatus()
                                self.finish(command, manager.isEnabled)
                            }
                        }
                    }
                }
            }
        }
    }

    private func reconcilePreferenceWrites(_ managers: [NETunnelProviderManager]?) {
        for manager in managers ?? [] {
            guard let tunnel = manager.protocolConfiguration as? NETunnelProviderProtocol,
                  let rawID = tunnel.providerConfiguration?["nativePreferenceWriteID"] as? String,
                  let id = UUID(uuidString: rawID), let pending = preferenceWrites[id],
                  let target = knownTargets.first(where: { matches(pending, target: $0) }),
                  matches(manager, target: target) else { continue }
            // A fresh read proves the save committed even if its callback was lost.
            preferenceWrites.removeValue(forKey: id)
        }
    }

    /// Acknowledges cancellation + an OS stop request, not tunnel disconnection.
    /// Submitted permission saves are passive and never start the tunnel, so stop
    /// can reconcile independently of their callbacks. Their transaction ledger
    /// still prevents overlapping writes until commit or completion is observed.
    /// System Settings retains independent ownership of the saved VPN.
    private func stop(_ command: Command) {
        guard !knownTargets.isEmpty || manager != nil else { fail(command, .notInitialized); return }
        hasStopStatus = true
        stopNeedsReconciliation = true
        stopRequested = true
        startRequested = false
        if let manager { rememberStopping(manager) }
        for manager in preferenceWrites.values { rememberStopping(manager) }
        for manager in stoppingManagers { manager.connection.stopVPNTunnel() }
        refreshStatus()
        scheduling.after(5) { [weak self] in
            guard let self, self.commands.owns(command) else { return }
            self.failStop(command)
        }
        NETunnelProviderManager.loadAllFromPreferences { managers, error in
            DispatchQueue.main.async {
                guard self.commands.owns(command) else { return }
                guard error == nil else { self.failStop(command); return }
                self.reconcilePreferenceWrites(managers)
                for manager in managers ?? [] where self.knownTargets.contains(where: { self.matches(manager, target: $0) }) {
                    self.rememberStopping(manager)
                }
                if let storage = self.storage, let bundle = self.providerBundleIdentifier {
                    self.setManager(self.findManager(managers, bundle: bundle, appGroup: storage.appGroup))
                }
                for manager in self.stoppingManagers { manager.connection.stopVPNTunnel() }
                self.stopNeedsReconciliation = false
                self.refreshStatus()
                self.finish(command)
            }
        }
    }

    private func failStop(_ command: Command) {
        // Keep both the shutdown intent and reconciliation fence on failure.
        // A stale callback or a disconnected old manager cannot clear either.
        fail(command, TunnelFailure(code: "stop_unconfirmed", message: "VPN cancellation could not be confirmed. Retry stop before starting again."))
    }

    private func rememberStopping(_ manager: NETunnelProviderManager) {
        if !stoppingManagers.contains(where: { $0 === manager }) { stoppingManagers.append(manager) }
        observeStatus()
    }

    private func isConsentCancellation(_ error: NSError) -> Bool {
        if error.domain == NSCocoaErrorDomain && error.code == NSUserCancelledError { return true }
        // NEConfiguration reports denied installation, including dismissal of system consent, as code 10.
        if error.domain == "NEConfigurationErrorDomain" && error.code == 10 { return true }
        if let underlying = error.userInfo[NSUnderlyingErrorKey] as? NSError { return isConsentCancellation(underlying) }
        return false
    }

    private func setManager(_ manager: NETunnelProviderManager?) {
        self.manager = manager
        observeStatus()
    }

    private func observeStatus() {
        guard statusObserver == nil else { return }
        statusObserver = NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: nil, queue: .main) { [weak self] notification in
            guard let self, let connection = notification.object as? NEVPNConnection,
                  connection === self.manager?.connection || self.stoppingManagers.contains(where: { $0.connection === connection }) else { return }
            self.startRequested = false
            if self.stopRequested, [.connecting, .connected, .reasserting].contains(connection.status) {
                connection.stopVPNTunnel()
            }
            self.refreshStatus()
        }
    }

    private func refreshStatus() {
        if stopNeedsReconciliation { emit(TunnelStatus("disconnecting")); return }
        if stopRequested {
            // Every profile touched by a cancelled preference operation must be
            // inactive. A stop acknowledgement alone never implies disconnected.
            guard stoppingManagers.allSatisfy({ [.invalid, .disconnected].contains($0.connection.status) }) else {
                emit(TunnelStatus("disconnecting"))
                return
            }
            stopRequested = false
            stoppingManagers.removeAll()
            try? storage?.writeStatus(TunnelStatus("disconnected"))
        }
        let state = manager?.connection.status ?? .disconnected
        switch state {
        case .connecting: emit(TunnelStatus("connecting"))
        case .connected: emit(TunnelStatus("connected"))
        case .reasserting: emit(TunnelStatus("connecting"))
        case .disconnecting: emit(TunnelStatus("disconnecting"))
        case .invalid, .disconnected:
            if startRequested { emit(TunnelStatus("connecting")); return }
            if let saved = storage?.readStatus(), saved.state == "error",
               let currentSessionID = storage?.currentSessionID(), saved.sessionID == currentSessionID {
                emit(TunnelStatus("error", message: saved.message))
            } else { emit(TunnelStatus("disconnected")) }
        @unknown default: emit(TunnelStatus("error", message: "The system returned an unknown VPN status."))
        }
    }

    private func queryProviderStatus(command: Command) {
        guard !stopRequested, let manager, [.connected, .reasserting].contains(manager.connection.status),
              let session = manager.connection as? NETunnelProviderSession,
              let request = try? JSONSerialization.data(withJSONObject: ["version": 1, "command": "getStatus"]) else {
            finish(command, status.event)
            return
        }
        let complete: (Data?) -> Void = { data in
            guard self.commands.owns(command) else { return }
            // Do not let a delayed provider reply override a more recent OS disconnect.
            self.refreshStatus()
            if manager === self.manager, manager.connection.status == .connected,
               let data, let reply = try? JSONDecoder().decode(TunnelStatus.self, from: data),
               ["connected", "error"].contains(reply.state) {
                self.emit(TunnelStatus(reply.state, message: reply.message))
            }
            self.finish(command, self.status.event)
        }
        do {
            try session.sendProviderMessage(request) { data in DispatchQueue.main.async { complete(data) } }
            scheduling.after(2) { complete(nil) }
        } catch { complete(nil) }
    }

    private func emit(_ value: TunnelStatus) {
        status = value
        eventSink?(value.event)
    }

    public func onListen(withArguments arguments: Any?, eventSink events: @escaping FlutterEventSink) -> FlutterError? {
        eventSink = events
        refreshStatus()
        return nil
    }

    public func onCancel(withArguments arguments: Any?) -> FlutterError? {
        eventSink = nil
        return nil
    }
}
