import XCTest

final class YtDlpUpdateTests: XCTestCase {
    func testInstalledVersionSurvivesAnUpdateCheckAndRelaunch() {
        continueAfterFailure = false
        let app = XCUIApplication()
        app.launchArguments = ["--reset-data"]
        app.launchEnvironment["REVERB_P2P_PORT"] = "0"
        app.launch()
        app.tabBars.buttons["Devices"].tap()
        let version = app.staticTexts["ytdlp.version"]
        expectation(for: NSPredicate(format: "label MATCHES %@", "^yt-dlp version, [0-9]{4}\\.[0-9]{2}\\.[0-9]{2}.*$"), evaluatedWith: version)
        waitForExpectations(timeout: 30)
        let check = app.buttons["ytdlp.update"]
        let ready = NSPredicate(format: "exists == true AND enabled == true")
        expectation(for: ready, evaluatedWith: check)
        waitForExpectations(timeout: 150)
        check.tap()
        expectation(for: ready, evaluatedWith: check)
        waitForExpectations(timeout: 150)
        XCTAssertTrue(version.exists, "A failed or successful update must leave a loaded version")
        let installed = version.label
        let screenshot = XCTAttachment(screenshot: app.screenshot())
        screenshot.name = "Installed yt-dlp version after update check"
        screenshot.lifetime = .keepAlways
        add(screenshot)
        app.terminate()
        app.launchArguments = []
        app.launch()
        app.tabBars.buttons["Devices"].tap()
        expectation(for: NSPredicate(format: "label == %@", installed), evaluatedWith: app.staticTexts["ytdlp.version"])
        waitForExpectations(timeout: 30)
    }
}
