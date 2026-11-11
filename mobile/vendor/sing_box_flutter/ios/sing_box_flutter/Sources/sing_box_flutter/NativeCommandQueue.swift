// SPDX-License-Identifier: GPL-3.0-or-later
import Foundation

/// Main-queue command ownership. Retiring a command revokes every continuation,
/// including its right to reply or release the next command. A stop owns the same
/// slot as an ordinary command, but acquires it immediately and cancels its backlog.
final class NativeCommandQueue {
    final class Command {
        fileprivate var replies: [(Any?) -> Void]
        fileprivate let isStop: Bool

        fileprivate init(reply: @escaping (Any?) -> Void, isStop: Bool = false) {
            replies = [reply]
            self.isStop = isStop
        }
    }

    private var active: Command?
    private var pending: [(Command, (Command) -> Void)] = []
    private var deliveringReplies = false

    func owns(_ command: Command) -> Bool { active === command }

    func enqueue(reply: @escaping (Any?) -> Void, execute: @escaping (Command) -> Void) {
        pending.append((Command(reply: reply), execute))
        runNext()
    }

    func interrupt(reply: @escaping (Any?) -> Void, cancelledValue: Any,
                   execute: (Command) -> Void) {
        let retired = active?.isStop == false ? active : nil
        let backlog = pending
        pending.removeAll()
        let existingStop = active?.isStop == true ? active : nil
        let stop = existingStop ?? Command(reply: reply, isStop: true)
        if existingStop != nil { stop.replies.append(reply) }
        // Install the barrier before delivering cancellation replies (reentrancy).
        active = stop
        if let retired { deliver(retired, cancelledValue) }
        for (command, _) in backlog { deliver(command, cancelledValue) }
        if existingStop == nil { execute(stop) }
    }

    func finish(_ command: Command, _ value: Any? = nil) {
        guard owns(command) else { return }
        active = nil
        let wasDelivering = deliveringReplies
        deliveringReplies = true
        deliver(command, value)
        deliveringReplies = wasDelivering
        runNext()
    }

    private func deliver(_ command: Command, _ value: Any?) {
        let replies = command.replies
        command.replies.removeAll()
        for reply in replies { reply(value) }
    }

    private func runNext() {
        guard active == nil, !deliveringReplies, !pending.isEmpty else { return }
        let (command, execute) = pending.removeFirst()
        active = command
        execute(command)
    }
}

/// Injectable scheduling keeps cancellation tests independent of timers and the
/// OS. Validation has no side effects; only its main-queue continuation may commit.
struct NativeCommandScheduling {
    let validate: (@escaping () -> Void) -> Void
    let after: (TimeInterval, @escaping () -> Void) -> Void

    static var live: NativeCommandScheduling {
        // Synchronous preflight cannot be forcibly terminated. A retired worker
        // must not occupy a serial queue needed by the next command. Repeated
        // pathological validations can still consume CPU until they return.
        let workers = DispatchQueue(label: "dev.idy.sing_box_flutter.validation", attributes: .concurrent)
        return NativeCommandScheduling(
            validate: { work in workers.async(execute: DispatchWorkItem(block: work)) },
            after: { delay, work in DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work) }
        )
    }
}
