// Run on a logged-in macOS desktop after `make build build-tray`:
// swift scripts/verify-tray-fullscreen.swift bin/tailsplit-tray bin/tailsplit bin/fullscreen-proof
// The output directory must be new. This fixture owns its full-screen window and
// isolated TailSplit process; it never injects global input or changes networking.
import AppKit
import Foundation

final class FullScreenProof: NSObject, NSApplicationDelegate, NSWindowDelegate {
    let executable: URL
    let helper: URL
    let output: URL
    let child = Process()
    var window: NSWindow!
    var timer: Timer?
    var log: FileHandle?
    var finished = false
    var result: Int32 = 1
    var started = Date()
    var enteringFullScreen = false
    var checkedFullScreen = false

    init(executable: URL, helper: URL, output: URL) {
        self.executable = executable
        self.helper = helper
        self.output = output
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        window = NSWindow(contentRect: NSRect(x: 100, y: 100, width: 800, height: 500),
                          styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        window.title = "TailSplit full-screen popup verification"
        window.collectionBehavior = [.fullScreenPrimary]
        window.delegate = self
        let label = NSTextField(labelWithString: "Verifying TailSplit's tray popup over another app's full-screen Space…")
        label.frame = NSRect(x: 40, y: 220, width: 720, height: 60)
        window.contentView?.addSubview(label)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        launch()
        DispatchQueue.main.asyncAfter(deadline: .now() + 50) { self.finish("Timed out waiting for full-screen popup proof") }
    }

    func windowDidEnterFullScreen(_ notification: Notification) {
        do {
            try Data().write(to: output.appendingPathComponent("fullscreen-ready"), options: .atomic)
        } catch {
            finish("Could not signal the full-screen Space: \(error)")
        }
    }

    func launch() {
        guard !finished else { return }
        do {
            try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)
            let logURL = output.appendingPathComponent("app.log")
            FileManager.default.createFile(atPath: logURL.path, contents: nil)
            log = try FileHandle(forWritingTo: logURL)
            child.executableURL = executable
            child.arguments = ["--config", output.appendingPathComponent("config.yaml").path,
                               "--helper", helper.path, "--log", output.appendingPathComponent("tailsplit.log").path,
                               "--proof-dir", output.path, "--no-connect"]
            var environment = ProcessInfo.processInfo.environment
            environment["TAILSPLIT_TRAY_PROOF_DIR"] = output.path
            environment["TAILSPLIT_TRAY_PROOF_FULLSCREEN"] = "1"
            child.environment = environment
            child.standardOutput = log
            child.standardError = log
            try child.run()
            started = Date()
            timer = Timer.scheduledTimer(withTimeInterval: 0.1, repeats: true) { _ in self.check() }
        } catch {
            finish("Could not launch isolated TailSplit: \(error)")
        }
    }

    func check() {
        if !enteringFullScreen && FileManager.default.fileExists(atPath: output.appendingPathComponent("tray-ready").path) {
            enteringFullScreen = true
            NSApp.activate(ignoringOtherApps: true)
            window.makeKeyAndOrderFront(nil)
            window.toggleFullScreen(nil)
        }
        if !checkedFullScreen,
           let data = try? Data(contentsOf: output.appendingPathComponent("tray-presented.json")),
           let native = (try? JSONSerialization.jsonObject(with: data)) as? [String: Bool] {
            let presentation: [String: Any] = ["native": native,
                "fixture_pid": ProcessInfo.processInfo.processIdentifier,
                "frontmost_pid": NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0,
                "fullscreen": window.styleMask.contains(.fullScreen), "on_active_space": window.isOnActiveSpace]
            if let evidence = try? JSONSerialization.data(withJSONObject: presentation, options: [.prettyPrinted, .sortedKeys]) {
                try? evidence.write(to: output.appendingPathComponent("presentation.json"), options: .atomic)
            }
            // AppKit can report an accessory app active while its nonactivating
            // panel has the keyboard. Check the actual foreground process and
            // Space instead of treating NSApp.isActive as a desktop switch.
            guard ["nonactivating_panel", "joins_all_spaces", "fullscreen_auxiliary",
                   "on_active_space", "visible_on_screen", "key_window"].allSatisfy({ native[$0] == true }),
                  window.styleMask.contains(.fullScreen), window.isOnActiveSpace,
                  NSWorkspace.shared.frontmostApplication?.processIdentifier == ProcessInfo.processInfo.processIdentifier else {
                finish("Popup changed the foreground app or did not appear over the full-screen Space")
                return
            }
            do {
                try Data().write(to: output.appendingPathComponent("fullscreen-checked"), options: .atomic)
                checkedFullScreen = true
            } catch {
                finish("Could not acknowledge full-screen proof: \(error)")
                return
            }
        }
        let reportURL = output.appendingPathComponent("tray-verification.json")
        if let data = try? Data(contentsOf: reportURL),
           var report = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] {
            guard checkedFullScreen else {
                finish("Popup proof completed without full-screen confirmation")
                return
            }
            report["other_app_fullscreen_preserved"] = true
            do {
                let proof = try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys])
                try proof.write(to: output.appendingPathComponent("fullscreen-verification.json"), options: .atomic)
                finish(nil)
            } catch {
                finish("Could not save full-screen proof: \(error)")
            }
        } else if !child.isRunning {
            finish("TailSplit exited before completing its popup proof; inspect app.log")
        } else if Date().timeIntervalSince(started) > 40 {
            finish("Popup proof timed out; inspect app.log and tray-diagnostics.json")
        }
    }

    func finish(_ error: String?) {
        guard !finished else { return }
        finished = true
        timer?.invalidate()
        if child.isRunning { child.terminate() }
        result = error == nil ? 0 : 1
        print(error.map { "FAILED: \($0)" } ?? "PASSED: popup visible and keyboard-focused over another app's full-screen Space; reopen and dismissal passed")
        print("Evidence: \(output.path)")
        if window?.styleMask.contains(.fullScreen) == true {
            window.toggleFullScreen(nil)
            DispatchQueue.main.asyncAfter(deadline: .now() + 3) { NSApp.terminate(nil) }
        } else {
            NSApp.terminate(nil)
        }
    }

    func windowDidExitFullScreen(_ notification: Notification) {
        if finished { NSApp.terminate(nil) }
    }

    func applicationWillTerminate(_ notification: Notification) {
        if child.isRunning { child.terminate() }
        try? log?.close()
        exit(result)
    }
}

guard CommandLine.arguments.count == 4 else {
    fputs("Usage: swift scripts/verify-tray-fullscreen.swift <tray-binary> <helper-binary> <new-output-directory>\n", stderr)
    exit(2)
}
let paths = CommandLine.arguments.dropFirst().map { URL(fileURLWithPath: $0).standardizedFileURL }
guard !FileManager.default.fileExists(atPath: paths[2].path) else {
    fputs("Output directory already exists; choose a fresh proof directory.\n", stderr)
    exit(2)
}
let app = NSApplication.shared
let proof = FullScreenProof(executable: paths[0], helper: paths[1], output: paths[2])
app.setActivationPolicy(.regular)
app.delegate = proof
app.run()
exit(proof.result)
