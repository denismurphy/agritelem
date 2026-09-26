import XCTest

/// Walks the main flow against built-in demo data (no server needed) and
/// keeps a screenshot of each screen as a test attachment.
final class AgritelemUITests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
        app = XCUIApplication()
        app.launchArguments = ["-AgritelemDemo"]
        app.launch()
    }

    @MainActor
    func testDashboardDetailAndRemarkFlow() {
        XCTAssertTrue(app.navigationBars["Herd"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Needs Attention"].waitForExistence(timeout: 5))
        snapshot("1-dashboard")

        app.buttons.containing(.staticText, identifier: "Daisy").firstMatch.tap()
        XCTAssertTrue(app.staticTexts["In Heat"].waitForExistence(timeout: 5))
        snapshot("2-animal-rumination")

        app.buttons["Temperature"].tap()
        let celsius = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "°C")).firstMatch
        XCTAssertTrue(celsius.waitForExistence(timeout: 5), "the headline switches to the temperature reading")
        snapshot("3-animal-temperature")

        app.buttons["Add Remark"].tap()
        let note = app.textFields["What did you observe?"]
        XCTAssertTrue(note.waitForExistence(timeout: 5))
        note.typeText("Bellowing and restless at evening milking.")
        snapshot("4-remark-composer")
        app.buttons["Save"].tap()

        XCTAssertTrue(app.staticTexts["Bellowing and restless at evening milking."].waitForExistence(timeout: 5),
                      "the saved remark appears on the animal's timeline")
        snapshot("5-remark-saved")
    }

    @MainActor
    func testFilterShowsOnlyAnimalsInHeat() {
        XCTAssertTrue(app.navigationBars["Herd"].waitForExistence(timeout: 10))
        app.buttons["Filter"].tap()
        app.buttons["In Heat"].tap()
        XCTAssertTrue(app.staticTexts["Daisy"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["Bessie"].exists)
    }

    private func snapshot(_ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
