import Flutter
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    guard let registrar = engineBridge.pluginRegistry.registrar(forPlugin: "TailSplitContainer") else {
      return
    }
    // The Packet Tunnel extension reads Tailscale state and the sing-box cache
    // from the shared App Group container.
    let channel = FlutterMethodChannel(
      name: "dev.idy.tailsplit/container",
      binaryMessenger: registrar.messenger()
    )
    channel.setMethodCallHandler { call, result in
      let arguments = call.arguments as? [String: Any]
      switch call.method {
      case "appGroupPath":
        guard let group = arguments?["group"] as? String else {
          result(FlutterError(code: "argument", message: "Missing App Group.", details: nil))
          return
        }
        let url = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group)
        result(url?.path)
      case "excludeFromBackup":
        // Device keys and Tailscale state must not leave the device in backups.
        guard let path = arguments?["path"] as? String else {
          result(FlutterError(code: "argument", message: "Missing path.", details: nil))
          return
        }
        var url = URL(fileURLWithPath: path, isDirectory: true)
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        do {
          try url.setResourceValues(values)
          result(nil)
        } catch {
          result(FlutterError(code: "backup", message: error.localizedDescription, details: nil))
        }
      default:
        result(FlutterMethodNotImplemented)
      }
    }
  }
}
