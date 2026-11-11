import Flutter
import XCTest

@testable import sing_box_flutter

class RunnerTests: XCTestCase {
  func testGetVersionBeforeInitialization() {
    let plugin = SingBoxFlutterPlugin()
    let call = FlutterMethodCall(methodName: "getVersion", arguments: nil)
    let resultExpectation = expectation(description: "Core version is returned without initializing the VPN.")

    plugin.handle(call) { result in
      // The bundled core version is pinned in native-core.json. An error or
      // unsupported-method reply should fail the assertion without crashing.
      XCTAssertEqual(result as? String, "1.14.4")
      resultExpectation.fulfill()
    }
    waitForExpectations(timeout: 5)
  }
}
