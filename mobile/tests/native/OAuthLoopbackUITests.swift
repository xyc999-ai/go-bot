import XCTest
import ObjectiveC

// Run against the local OAuth fixture (8094), with the production mobile binary.
// Codes are synthetic; no external account or paid model is used.
final class OAuthLoopbackUITests: XCTestCase {
  // Runner-only: animated React Native screens do not become animation-idle.
  // Wait for explicit controls/results instead of XCTest's unbounded idle wait.
  override func setUpWithError() throws {
    if let process = NSClassFromString("XCUIApplicationProcess") {
      if let method = class_getInstanceMethod(process, NSSelectorFromString("waitForQuiescenceIncludingAnimationsIdle:")) {
        let skip: @convention(block) (AnyObject, Bool) -> Void = { _, _ in }
        method_setImplementation(method, imp_implementationWithBlock(skip))
      }
      if let method = class_getInstanceMethod(process, NSSelectorFromString("waitForQuiescenceIncludingAnimationsIdle:isPreEvent:")) {
        let skip: @convention(block) (AnyObject, Bool, Bool) -> Void = { _, _, _ in }
        method_setImplementation(method, imp_implementationWithBlock(skip))
      }
    }
  }
  let app = XCUIApplication(bundleIdentifier: "com.gobot.mobile")
  func button(_ title: String) -> XCUIElement { app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", title)).firstMatch }
  func tap(_ title: String) {
    let value = button(title)
    XCTAssertTrue(value.waitForExistence(timeout: 30), "Missing \(title): \(app.debugDescription)")
    if !value.isHittable { app.swipeUp() }
    value.tap()
  }
  func fixture(_ route: String) {
    let ready = expectation(description: route)
    URLSession.shared.dataTask(with: URL(string: "http://127.0.0.1:8094/__test/" + route)!) { _,response,error in
      XCTAssertNil(error); XCTAssertEqual((response as? HTTPURLResponse)?.statusCode,200);ready.fulfill()
    }.resume()
    wait(for:[ready],timeout:15)
  }
  func browserClose() -> XCUIElement {
    app.buttons.matching(NSPredicate(format: "label IN %@", ["Done", "Close", "完成", "关闭"])).firstMatch
  }
  func testNativeBrowserCallbackAndRestart() {
    continueAfterFailure = false
    fixture("reset")
    app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
    app.launch()
    tap("Me")
    tap("Plugin market")
    tap("OAuth receiver test")
    tap("Accounts and authorization")
    tap("Connect account")
    tap("Continue in browser")
    // SFSafariViewController belongs to this app, keeping it foreground.
    XCTAssertTrue(browserClose().waitForExistence(timeout:15), app.debugDescription)
    fixture("callback")
    XCTAssertTrue(app.staticTexts["Authorization is ready."].waitForExistence(timeout:20), app.debugDescription)
    let capture = XCTAttachment(screenshot:app.screenshot()); capture.name="ios-oauth-authorized";capture.lifetime = .keepAlways;add(capture)
    tap("Done")
    tap("Authorize again")
    tap("Continue in browser")
    XCTAssertTrue(browserClose().waitForExistence(timeout:15))
    // Quiescence is disabled in this runner; let the native presentation settle.
    Thread.sleep(forTimeInterval: 0.7)
    browserClose().coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap() // Close the system browser without completing consent.
    XCTAssertTrue(button("Restart authorization").waitForExistence(timeout:20), app.debugDescription)
    fixture("root-path")
    tap("Restart authorization")
    tap("Continue in browser")
    XCTAssertTrue(browserClose().waitForExistence(timeout:15))
    fixture("callback")
    XCTAssertTrue(app.staticTexts["Authorization is ready."].waitForExistence(timeout:20), app.debugDescription)
  }
}
