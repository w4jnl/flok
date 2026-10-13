import XCTest

/// The README's phone screenshots, not a test of behaviour: assets/demo/phone.sh runs a relay
/// with made-up instances, then this walks the app through the screens worth showing and writes
/// each as a PNG to FLOK_SHOTS. A push is asked of the script by a file (`push`) there, since
/// only `simctl push` reaches the simulator. Its own scheme, so `make ios-test` never runs it.
@MainActor
final class ScreensTests: XCTestCase {
    private let env = ProcessInfo.processInfo.environment
    private lazy var shots = env["FLOK_SHOTS"] ?? NSTemporaryDirectory()
    private let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")

    func testScreens() throws {
        let relay = try XCTUnwrap(env["FLOK_RELAY"], "FLOK_RELAY: the relay to show")
        let token = try XCTUnwrap(env["FLOK_DEVICE_TOKEN"], "FLOK_DEVICE_TOKEN: one of its device tokens")
        let app = XCUIApplication()
        app.launchArguments = ["-relayURL", relay, "-deviceToken", token]
        app.launch()
        XCTAssertTrue(app.staticTexts["office"].waitForExistence(timeout: 15), "the instances never arrived")
        sleep(2)
        shot("list")

        // notifications allowed, and their settings
        app.buttons["Settings"].tap()
        if app.buttons["Enable notifications"].waitForExistence(timeout: 5) {
            app.buttons["Enable notifications"].tap()
            let allow = springboard.alerts.buttons["Allow"]
            if allow.waitForExistence(timeout: 5) { allow.tap() }
        }
        if app.buttons["Filters and sounds"].waitForExistence(timeout: 5) {
            app.buttons["Filters and sounds"].tap()
            sleep(1)
            shot("settings")
        }

        // the agent waiting on you, its screen live
        app.terminate()
        app.launchArguments = ["-relayURL", relay, "-deviceToken", token, "-open", "home:%3"]
        app.launch()
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'Do you want to proceed?'")).firstMatch
            .waitForExistence(timeout: 15), "no screen for api")
        sleep(2)
        shot("agent")

        // the overview in the Dynamic Island, then a push and its card
        XCUIDevice.shared.press(.home)
        sleep(3)
        shot("island")
        FileManager.default.createFile(atPath: shots + "/push", contents: Data("permission".utf8))
        let banner = springboard.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'needs you'")).firstMatch
        if banner.waitForExistence(timeout: 20) {
            sleep(1)
            shot("banner")
            banner.press(forDuration: 1.2)
            sleep(6) // the card fetches the screen from the relay
            shot("card")
            springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.04)).tap()
            sleep(1)
        }

        // the Lock Screen: the overview and what waits
        XCUIDevice.shared.perform(NSSelectorFromString("pressLockButton"))
        sleep(2)
        XCUIDevice.shared.perform(NSSelectorFromString("pressLockButton"))
        sleep(3)
        let allow = springboard.buttons["Allow"] // iOS asks once before showing Live Activities there
        if allow.waitForExistence(timeout: 3) {
            allow.tap()
            sleep(2)
        }
        shot("lock")
    }

    private func shot(_ name: String) {
        let png = XCUIScreen.main.screenshot().pngRepresentation
        try? png.write(to: URL(fileURLWithPath: shots).appendingPathComponent(name + ".png"))
    }
}
