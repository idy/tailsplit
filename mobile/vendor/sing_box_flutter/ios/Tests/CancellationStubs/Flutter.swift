// SPDX-License-Identifier: GPL-3.0-or-later
// Test-only module: no Flutter engine or platform channels are loaded.
import Foundation

public typealias FlutterResult = (Any?) -> Void
public typealias FlutterEventSink = (Any?) -> Void
public let FlutterMethodNotImplemented = NSObject()
public final class FlutterError {
    public let code: String
    public let message: String?
    public let details: Any?
    public init(code: String, message: String?, details: Any?) {
        self.code = code; self.message = message; self.details = details
    }
}
public final class FlutterMethodCall {
    public let method: String
    public let arguments: Any?
    public init(methodName: String, arguments: Any?) { method = methodName; self.arguments = arguments }
}
public protocol FlutterPlugin {
    static func register(with registrar: FlutterPluginRegistrar)
}
public protocol FlutterStreamHandler {}
public protocol FlutterPluginRegistrar {
    func messenger() -> Any
    func addMethodCallDelegate(_ delegate: FlutterPlugin, channel: FlutterMethodChannel)
}
public final class FlutterMethodChannel {
    public init(name: String, binaryMessenger: Any) {}
}
public final class FlutterEventChannel {
    public init(name: String, binaryMessenger: Any) {}
    public func setStreamHandler(_ handler: FlutterStreamHandler) {}
}
