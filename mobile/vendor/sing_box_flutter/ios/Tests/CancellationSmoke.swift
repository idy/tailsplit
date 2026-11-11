// SPDX-License-Identifier: GPL-3.0-or-later
import Foundation
import Flutter
import NetworkExtension

/// Compiles the real host plugin against inert Flutter/NE modules. Manual async
/// completions exercise the production callback guards and queue, not a model of
/// them. The test runner never links Apple's NetworkExtension or accesses files.
@main
struct CancellationSmoke {
    static func main() throws {
        queueOwnership()
        stalledStartAndQueuedWork()
        lateValidationFailure()
        stalledCheck()
        cancelledInitializeReads()
        statusAfterCancelledInitialize()
        cancelledInitializeValidation()
        cancelledPermissionRead()
        lostSaveCallback()
        pendingSaveIsPassive()
        cancelledPreferenceReload()
        cancelledProviderReply()
        stopFailureAndRetry()
        stopTimeoutAndLateCallback()
        engineDetachDoesNotStop()
        concurrentValidation()
        print("Native cancellation: 16 scenarios passed (real plugin, stubbed OS; no VPN started).")
    }

    private static func queueOwnership() {
        let queue = NativeCommandQueue()
        let old = Reply(), queued = Reply(), stop1 = Reply(), stop2 = Reply(), cancelledBetweenStops = Reply(), fresh = Reply()
        var oldCommand: NativeCommandQueue.Command!
        var barrier: NativeCommandQueue.Command!
        var freshCommand: NativeCommandQueue.Command!
        queue.enqueue(reply: old.record) { oldCommand = $0 }
        queue.enqueue(reply: queued.record) { _ in fatalError("Queued command ran before stop") }
        queue.interrupt(reply: stop1.record, cancelledValue: "cancelled") { barrier = $0 }
        expect(old.count == 1 && queued.count == 1, "Stop must cancel active and queued replies immediately")
        queue.enqueue(reply: cancelledBetweenStops.record) { _ in fatalError("Start overtook stop barrier") }
        queue.finish(oldCommand)
        expect(stop1.count == 0, "Retired callback finished the stop")
        queue.interrupt(reply: stop2.record, cancelledValue: "cancelled") { _ in fatalError("Duplicate stop restarted the barrier") }
        expect(cancelledBetweenStops.count == 1, "Second stop did not cancel its backlog")
        queue.enqueue(reply: fresh.record) { freshCommand = $0 }
        expect(freshCommand == nil, "Fresh command overtook pending stop")
        queue.finish(barrier)
        expect(stop1.count == 1 && stop2.count == 1 && freshCommand != nil, "Coalesced stop did not release fresh work")
        queue.finish(oldCommand, "late success")
        queue.finish(barrier, "late failure")
        expect(fresh.count == 0 && queue.owns(freshCommand), "Late completion released newer command")
        queue.finish(freshCommand)
        expect(fresh.count == 1 && old.count == 1 && stop1.count == 1, "Reply delivered more than once")
    }

    private static func stalledStartAndQueuedWork() {
        let f = Fixture()
        let old = f.send("start", startArguments)
        let retiredValidation = f.scheduler.work.removeFirst() // Never finishes until after stop.
        let queued = f.send("requestPermission")
        let stop = f.send("stop")
        expect(old.code == "operation_cancelled" && queued.code == "operation_cancelled", "Pending replies not cancelled")
        expect(NETunnelProviderManager.loads.count == 1, "Stop did not bypass stalled start")
        let fresh = f.send("start", startArguments)
        expect(f.scheduler.work.isEmpty && fresh.count == 0, "Fresh start overtook async stop")
        f.completeLoad([f.installed.snapshot()])
        expect(stop.succeeded && f.scheduler.work.count == 1, "Stop did not release new validation")
        retiredValidation(); flush()
        expect(TunnelStorage.configurationWrites == 0 && f.session.startCalls == 0, "Retired start saved credentials or revived VPN")
        expect(fresh.count == 0 && old.count == 1, "Retired start completed newer command")
        f.runValidation()
        expect(fresh.succeeded && f.session.startCalls == 1 && TunnelStorage.configurationWrites == 1, "Fresh start could not proceed")
    }

    private static func lateValidationFailure() {
        let f = Fixture()
        let old = f.send("start", ["config": "invalid", "name": "fixture"])
        let retired = f.scheduler.work.removeFirst()
        let stop = f.send("stop")
        f.completeLoad([f.installed.snapshot()])
        let fresh = f.send("checkConfig", ["config": "{}"])
        let queued = f.send("getVersion")
        retired(); flush()
        expect(old.code == "operation_cancelled" && old.count == 1 && stop.succeeded, "Late validation error replied again")
        expect(fresh.count == 0 && queued.count == 0, "Late failure advanced newer command")
        f.runValidation()
        expect(fresh.succeeded && queued.count == 1, "Fresh queue stayed blocked")
    }

    private static func stalledCheck() {
        let f = Fixture()
        let old = f.send("checkConfig", ["config": "{}"])
        _ = f.scheduler.work.removeFirst() // Permanently stalled preflight.
        let stop = f.send("stop")
        f.completeLoad([f.installed.snapshot()])
        let fresh = f.send("checkConfig", ["config": "{}"])
        f.runValidation()
        expect(old.code == "operation_cancelled" && stop.succeeded && fresh.succeeded, "Validation cancellation left the queue blocked")
    }

    private static func cancelledInitializeReads() {
        let f = Fixture(ready: false)
        let old = f.send("initialize", initializeArguments)
        let retiredRead = NETunnelProviderManager.loads.removeFirst()
        let stop = f.send("stop")
        f.installed.connection.status = .connected
        f.completeLoad([f.installed.snapshot()])
        expect(stop.succeeded && f.session.stopCalls > 0, "Stop during initialization did not find OS tunnel")
        expect(f.send("getStatus").state == "disconnecting", "Uninitialized status falsely confirmed an active OS tunnel")
        retiredRead([f.installed.snapshot()], nil); flush()
        expect(f.scheduler.work.isEmpty && old.code == "operation_cancelled", "Retired initialize continued validation")
        f.session.report(.disconnected)
        let fresh = f.send("initialize", initializeArguments)
        f.completeLoad([f.installed.snapshot()]); f.runValidation()
        expect(fresh.succeeded, "Fresh initialize could not proceed")
        retiredRead(nil, fixtureError); flush()
        expect(old.count == 1 && fresh.count == 1, "Late initialize failure replied twice")
    }

    private static func statusAfterCancelledInitialize() {
        let f = Fixture(ready: false)
        expect(f.send("getStatus").code == "not_initialized", "Status without any native context should not claim shutdown")
        let old = f.send("initialize", initializeArguments)
        let retiredRead = NETunnelProviderManager.loads.removeFirst() // Initialization stalls before storage is adopted.
        let stop = f.send("stop")
        f.completeLoad([f.installed.snapshot()])
        expect(stop.succeeded && old.code == "operation_cancelled", "Stop did not retire initialization")
        // Mirrors the app's stream wait followed by its authoritative status query.
        expect(f.events.last == "disconnected" && f.send("getStatus").state == "disconnected", "Storage-free stop could not confirm shutdown")
        let fresh = f.send("initialize", initializeArguments)
        f.completeLoad([f.installed.snapshot()]); f.runValidation()
        expect(fresh.succeeded, "App could not reinitialize after confirmed shutdown")
        let eventCount = f.events.count
        retiredRead([f.installed.snapshot()], nil); flush()
        retiredRead(nil, fixtureError); flush()
        expect(old.count == 1 && fresh.count == 1 && f.scheduler.work.isEmpty && f.events.count == eventCount, "Late original initialization changed recovered state")
        let start = f.send("start", startArguments); f.runValidation()
        expect(start.succeeded, "Recovered initialization did not support a new start")
    }

    private static func cancelledInitializeValidation() {
        let f = Fixture(ready: false)
        let old = f.send("initialize", initializeArguments)
        f.completeLoad([f.installed.snapshot()])
        let retired = f.scheduler.work.removeFirst()
        _ = f.send("stop"); f.completeLoad([f.installed.snapshot()])
        let newManager = makeManager(bundle: "fixture.new", group: "group.fixture.new")
        let fresh = f.send("initialize", ["appGroup": "group.fixture.new", "providerBundleIdentifier": "fixture.new", "notificationTitle": "New"])
        f.completeLoad([newManager]); f.runValidation()
        retired(); flush()
        let start = f.send("start", startArguments); f.runValidation()
        expect(fresh.succeeded && old.count == 1 && start.succeeded, "Initialize generation was lost")
        expect((newManager.connection as! NETunnelProviderSession).startCalls == 1 && f.session.startCalls == 0, "Retired initialize replaced the new manager")
    }

    private static func cancelledPermissionRead() {
        let f = Fixture()
        let permission = f.send("requestPermission")
        let retiredRead = NETunnelProviderManager.loads.removeFirst()
        _ = f.send("stop"); f.completeLoad([f.installed.snapshot()])
        let fresh = f.send("checkConfig", ["config": "{}"])
        retiredRead([], nil); flush()
        expect(NETunnelProviderManager.saves.isEmpty && permission.code == "operation_cancelled", "Cancelled permission installed a profile")
        retiredRead(nil, fixtureError); flush()
        expect(permission.count == 1 && fresh.count == 0, "Late permission reply finished fresh command")
        f.runValidation()
        expect(fresh.succeeded, "Permission cancellation stalled fresh command")
    }

    private static func lostSaveCallback() {
        let f = Fixture(installed: false)
        let permission = f.send("requestPermission"); f.completeLoad([])
        let (saved, _) = NETunnelProviderManager.saves[0] // OS commits, callback is permanently lost.
        expect(!saved.isOnDemandEnabled && saved.onDemandRules == nil, "Submitted permission write can auto-start")
        let persisted = saved.snapshot()
        let stop = f.send("stop")
        expect(NETunnelProviderManager.loads.count == 1, "Stop waited for lost save acknowledgement")
        f.completeLoad([persisted])
        expect(stop.succeeded && permission.code == "operation_cancelled", "Lost save callback blocked stop")
        // Disable the now-committed profile: the transaction marker must have
        // settled the ledger, so an explicit later permission save is allowed.
        persisted.isEnabled = false
        let retry = f.send("requestPermission"); f.completeLoad([persisted.snapshot()])
        expect(NETunnelProviderManager.saves.count == 2 && retry.count == 0, "Committed transaction still blocks a fresh save")
        let (newSave, complete) = NETunnelProviderManager.saves[1]
        complete(nil); flush()
        NETunnelProviderManager.reloads.removeFirst().1(nil); flush()
        expect(retry.value as? Bool == true && newSave !== saved, "Fresh permission did not finish independently")
    }

    private static func pendingSaveIsPassive() {
        let f = Fixture(installed: false)
        let permission = f.send("requestPermission"); f.completeLoad([])
        let (saved, lateCallback) = NETunnelProviderManager.saves[0]
        let stop = f.send("stop"); f.completeLoad([]) // Write is still not visible.
        expect(stop.succeeded && permission.code == "operation_cancelled", "Uncommitted passive save blocked stop")
        let check = f.send("checkConfig", ["config": "{}"]); f.runValidation()
        let duplicate = f.send("requestPermission"); f.completeLoad([])
        expect(check.succeeded && duplicate.code == "preferences_pending" && NETunnelProviderManager.saves.count == 1, "Pending save was ignored or blocked unrelated work")
        // A late passive install cannot start a tunnel, and its retired callback
        // must not reload/adopt its receiver or write configuration/status.
        let statusWrites = TunnelStorage.statusWrites.count
        lateCallback(nil); flush()
        expect(NETunnelProviderManager.reloads.isEmpty && TunnelStorage.configurationWrites == 0, "Cancelled save continued its operation")
        expect((saved.connection as! NETunnelProviderSession).startCalls == 0 && TunnelStorage.statusWrites.count == statusWrites, "Late save revived VPN or changed status")
        let fresh = f.send("requestPermission"); f.completeLoad([saved.snapshot()])
        expect(fresh.value as? Bool == true, "Permission did not recover after late install")
        let start = f.send("start", startArguments)
        lateCallback(fixtureError); flush()
        expect(start.count == 0 && permission.count == 1, "Late save error completed a newer command")
        f.runValidation()
        expect(start.succeeded, "Late save error cleared the live manager")
    }

    private static func cancelledPreferenceReload() {
        let f = Fixture(installed: false)
        let old = f.send("requestPermission"); f.completeLoad([])
        let (saved, savedCallback) = NETunnelProviderManager.saves[0]
        savedCallback(nil); flush()
        let (privateManager, retiredReload) = NETunnelProviderManager.reloads.removeFirst()
        let liveSnapshot = saved.snapshot()
        _ = f.send("stop"); f.completeLoad([liveSnapshot])
        let fresh = f.send("checkConfig", ["config": "{}"])
        // NE may mutate loadFromPreferences' receiver before calling back.
        privateManager.isEnabled = false
        retiredReload(nil); flush()
        retiredReload(fixtureError); flush()
        expect(fresh.count == 0 && old.count == 1, "Retired reload completed new command")
        f.runValidation()
        let start = f.send("start", startArguments)
        f.runValidation()
        expect(start.succeeded, "Late reload changed the adopted manager")
    }

    private static func cancelledProviderReply() {
        let f = Fixture()
        f.session.report(.connected)
        let old = f.send("getStatus")
        let lateProvider = f.session.messages.removeFirst()
        let stop = f.send("stop"); f.completeLoad([f.installed.snapshot()])
        let eventsAfterStop = f.events.count
        let fresh = f.send("checkConfig", ["config": "{}"])
        lateProvider(try! JSONEncoder().encode(TunnelStatus("connected"))); flush()
        f.scheduler.fire(delay: 2)
        expect(old.code == "operation_cancelled" && old.count == 1 && stop.succeeded && fresh.count == 0, "Provider reply escaped command scope")
        expect(f.events.count == eventsAfterStop, "Retired provider reply changed status")
        f.runValidation()
        let status = f.send("getStatus")
        expect(status.state == "disconnecting", "Stop reported disconnected while NE is connected")
        f.session.report(.disconnected)
        expect(f.send("getStatus").state == "disconnected", "OS disconnect was not confirmed")
    }

    private static func stopFailureAndRetry() {
        let f = Fixture()
        f.session.report(.connected)
        let stop = f.send("stop"); f.completeLoad(nil, error: fixtureError)
        expect(stop.code == "stop_unconfirmed", "Failed stop was reported successful")
        f.session.report(.disconnected)
        expect(f.send("getStatus").state == "disconnecting", "Old status cleared failed reconciliation")
        expect(f.send("start", startArguments).code == "busy", "Start bypassed failed stop fence")
        let fresh = f.send("checkConfig", ["config": "{}"]); f.runValidation()
        expect(fresh.succeeded, "Stop failure blocked independent validation")
        let retry = f.send("stop"); f.completeLoad([f.installed.snapshot()])
        expect(retry.succeeded && f.send("getStatus").state == "disconnected", "Explicit stop retry did not recover")
    }

    private static func stopTimeoutAndLateCallback() {
        let f = Fixture()
        let first = f.send("stop")
        let retiredLoad = NETunnelProviderManager.loads.removeFirst()
        let cancelled = f.send("start", startArguments)
        let second = f.send("stop")
        expect(cancelled.code == "operation_cancelled" && NETunnelProviderManager.loads.isEmpty, "Repeated stop failed to coalesce")
        f.scheduler.fire(delay: 5)
        expect(first.code == "stop_unconfirmed" && second.code == "stop_unconfirmed", "Stop deadline did not release replies")
        let retry = f.send("stop")
        let eventsBeforeLateReply = f.events.count
        retiredLoad([f.installed.snapshot()], nil); flush()
        expect(retry.count == 0 && f.events.count == eventsBeforeLateReply, "Retired stop completed retry or changed its status")
        f.completeLoad([f.installed.snapshot()])
        let fresh = f.send("start", startArguments)
        retiredLoad(nil, fixtureError); flush()
        expect(fresh.count == 0 && retry.succeeded, "Late stop failure released fresh command")
        f.runValidation()
        expect(fresh.succeeded && first.count == 1 && second.count == 1, "Timed-out stop prevented later start")
    }

    private static func engineDetachDoesNotStop() {
        let f = Fixture()
        f.session.report(.connected)
        let stops = f.session.stopCalls
        f.plugin.detachFromEngine(for: Registrar())
        expect(f.session.stopCalls == stops && f.session.status == .connected, "Engine detach stopped OS-owned tunnel")
    }

    private static func concurrentValidation() {
        let scheduling = NativeCommandScheduling.live
        let started = DispatchSemaphore(value: 0), release = DispatchSemaphore(value: 0), fresh = DispatchSemaphore(value: 0)
        scheduling.validate { started.signal(); release.wait() }
        defer { release.signal() }
        expect(started.wait(timeout: .now() + 2) == .success, "Validation worker did not start")
        scheduling.validate { fresh.signal() }
        expect(fresh.wait(timeout: .now() + 2) == .success, "Retired validation still occupies a serial worker")
    }
}

private let fixtureError = NSError(domain: "fixture-private-sentinel", code: 1)
private let initializeArguments: [String: Any] = ["appGroup": "group.fixture", "providerBundleIdentifier": "fixture.tunnel", "notificationTitle": "Fixture"]
private let startArguments: [String: Any] = ["config": "{}", "name": "Fixture"]
private func expect(_ condition: @autoclosure () -> Bool, _ message: String, file: StaticString = #file, line: UInt = #line) {
    precondition(condition(), message, file: file, line: line)
}
private func flush() { RunLoop.current.run(until: Date().addingTimeInterval(0.005)) }
private func makeManager(bundle: String = "fixture.tunnel", group: String = "group.fixture") -> NETunnelProviderManager {
    let manager = NETunnelProviderManager()
    let tunnel = NETunnelProviderProtocol()
    tunnel.providerBundleIdentifier = bundle
    tunnel.providerConfiguration = ["appGroup": group]
    manager.protocolConfiguration = tunnel
    manager.isEnabled = true
    return manager
}
private final class Reply {
    var count = 0
    var value: Any?
    var code: String? { (value as? FlutterError)?.code }
    var state: String? { (value as? [String: Any])?["state"] as? String }
    var succeeded: Bool { count == 1 && value == nil }
    func record(_ value: Any?) {
        count += 1; self.value = value
        if let failure = value as? FlutterError {
            expect(failure.details == nil && !(failure.message ?? "").contains("sentinel"), "Error leaked OS details")
        }
    }
}
private final class Scheduler {
    var work: [() -> Void] = []
    var timers: [(TimeInterval, () -> Void)] = []
    var scheduling: NativeCommandScheduling {
        NativeCommandScheduling(validate: { self.work.append($0) }, after: { self.timers.append(($0, $1)) })
    }
    func fire(delay: TimeInterval) {
        let index = timers.firstIndex { $0.0 == delay }!
        timers.remove(at: index).1(); flush()
    }
}
private final class Fixture {
    let scheduler = Scheduler()
    let plugin: SingBoxFlutterPlugin
    let installed = makeManager()
    var session: NETunnelProviderSession { installed.connection as! NETunnelProviderSession }
    var events: [String] = []
    init(ready: Bool = true, installed hasInstalled: Bool = true) {
        NETunnelProviderManager.reset()
        TunnelStorage.configurationWrites = 0; TunnelStorage.statusWrites = []
        CoreCapabilities.failure = nil
        plugin = SingBoxFlutterPlugin(scheduling: scheduler.scheduling)
        _ = plugin.onListen(withArguments: nil) { [weak self] event in
            if let state = (event as? [String: Any])?["state"] as? String { self?.events.append(state) }
        }
        if ready {
            let reply = send("initialize", initializeArguments)
            completeLoad(hasInstalled ? [installed.snapshot()] : [])
            runValidation()
            expect(reply.succeeded, "Fixture initialization failed")
        }
    }
    func send(_ method: String, _ arguments: [String: Any]? = nil) -> Reply {
        let reply = Reply()
        plugin.handle(FlutterMethodCall(methodName: method, arguments: arguments), result: reply.record)
        flush()
        return reply
    }
    func completeLoad(_ managers: [NETunnelProviderManager]?, error: Error? = nil) {
        expect(!NETunnelProviderManager.loads.isEmpty, "Expected a pending preferences read")
        NETunnelProviderManager.loads.removeFirst()(managers, error); flush()
    }
    func runValidation() {
        expect(!scheduler.work.isEmpty, "Expected pending validation")
        scheduler.work.removeFirst()(); flush()
    }
}
private final class Registrar: FlutterPluginRegistrar {
    func messenger() -> Any { NSObject() }
    func addMethodCallDelegate(_ delegate: FlutterPlugin, channel: FlutterMethodChannel) {}
}
