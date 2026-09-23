import XCTest

/// The iOS smoke test: launch, pair with a test runtime, keep a playlist
/// offline, and play its track. The test runtime runs on the Mac
/// (`make ios-testpeer`), which the simulator reaches on 127.0.0.1; the
/// simulator has no camera, so it pairs by typed code, the fallback path.
final class SmokeTests: XCTestCase {
    private struct Pairing: Decodable {
        let code: String
        let address: String
        let playlist: String
        let track: String
    }

    override func setUp() {
        continueAfterFailure = false
    }

    func testDelegatedLibraryBrowseAndPlay() throws {
        let pairing = try testPeerPairing()
        let app = XCUIApplication()
        app.launchArguments = ["--reset-data"]
        app.launchEnvironment["REVERB_P2P_PORT"] = "0"
        app.launch()

        app.tabBars.buttons["Devices"].tap()
        tapWhenReady(app.buttons["devices.pair"])
        app.segmentedControls.buttons["Type a code"].tap()
        type(pairing.code, into: app.textFields["pair.code"])
        type(pairing.address, into: app.textFields["pair.address"])
        app.buttons["pair.submit"].tap()
        XCTAssertTrue(waitForDisappearance(app.navigationBars["Pair a device"], timeout: 60))

        app.tabBars.buttons["Library"].tap()
        app.segmentedControls.buttons["Tracks"].tap()
        let remote = app.descendants(matching: .any)["catalog.track.\(pairing.track)"]
        XCTAssertTrue(remote.waitForExistence(timeout: 60), "The synced desktop track did not appear in Library")
        remote.tap()
        let title = app.staticTexts["nowPlaying.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 20))
        XCTAssertEqual(title.label, pairing.track)
        let elapsed = app.staticTexts["nowPlaying.elapsed"]
        wait(for: [expectation(for: NSPredicate(format: "label IN {'0:01', '0:02', '0:03', '0:04', '0:05'}"), evaluatedWith: elapsed)], timeout: 12)
    }

    func testPairKeepOfflineAndPlay() throws {
        let pairing = try testPeerPairing()
        let app = XCUIApplication()
        app.launchArguments = ["--reset-data"]
        // The Mac may run Reverb on the default port.
        app.launchEnvironment["REVERB_P2P_PORT"] = "0"
        app.launch()

        app.tabBars.buttons["Devices"].tap()
        tapWhenReady(app.buttons["devices.pair"])
        app.segmentedControls.buttons["Type a code"].tap()
        type(pairing.code, into: app.textFields["pair.code"])
        type(pairing.address, into: app.textFields["pair.address"])
        app.buttons["pair.submit"].tap()
        XCTAssertTrue(waitForDisappearance(app.navigationBars["Pair a device"], timeout: 60), "pairing did not finish: \(app.staticTexts["pair.error"].label)")

        app.tabBars.buttons["Playlists"].tap()
        tapWhenReady(app.buttons["playlist.\(pairing.playlist)"], timeout: 90)

        let toggle = app.switches["playlist.offlineToggle"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 20))
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()

        let track = app.descendants(matching: .any)["track.\(pairing.track)"]
        XCTAssertTrue(track.waitForExistence(timeout: 20))
        wait(for: [expectation(for: NSPredicate(format: "value == 'ready'"), evaluatedWith: track)], timeout: 120)
        // The track now plays from the phone alone.
        try testPeer("stop")
        track.tap()

        let title = app.staticTexts["nowPlaying.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 20))
        XCTAssertEqual(title.label, pairing.track)
        let playPause = app.buttons["nowPlaying.playPause"]
        wait(for: [expectation(for: NSPredicate(format: "value == 'playing'"), evaluatedWith: playPause)], timeout: 20)

        // Sound must actually advance; a play request alone also succeeds
        // when a decoder is stuck waiting or has failed. Each query takes
        // about a second, so the checks accept a range rather than a moment.
        let elapsed = app.staticTexts["nowPlaying.elapsed"]
        wait(for: [expectation(for: NSPredicate(format: "label IN {'0:01', '0:02', '0:03', '0:04', '0:05', '0:06', '0:07', '0:08'}"), evaluatedWith: elapsed)], timeout: 10)
        XCTAssertEqual(app.staticTexts["nowPlaying.duration"].label, "0:20")

        playPause.tap()
        wait(for: [expectation(for: NSPredicate(format: "value == 'paused'"), evaluatedWith: playPause)], timeout: 5)
        let pausedAt = elapsed.label
        sleep(2)
        XCTAssertEqual(elapsed.label, pausedAt, "the clock moved while paused")

        // A seek while paused moves the clock and stays paused.
        app.sliders["nowPlaying.position"].adjust(toNormalizedSliderPosition: 0.85)
        wait(for: [expectation(for: NSPredicate(format: "label IN {'0:16', '0:17', '0:18'}"), evaluatedWith: elapsed)], timeout: 5)
        XCTAssertEqual(playPause.value as? String, "paused")

        playPause.tap()
        wait(for: [expectation(for: NSPredicate(format: "value == 'playing'"), evaluatedWith: playPause)], timeout: 5)
        XCTAssertTrue(waitForDisappearance(title, timeout: 15), "the final track did not finish its queue")
    }

    // MARK: Helpers

    /// Asks the test runtime for a fresh code and the address to dial it on.
    private func testPeerPairing() throws -> Pairing {
        try JSONDecoder().decode(Pairing.self, from: testPeer("pairing"))
    }

    /// Calls one of the test runtime's own endpoints.
    @discardableResult
    private func testPeer(_ action: String) throws -> Data {
        let base = ProcessInfo.processInfo.environment["REVERB_TESTPEER"] ?? "http://127.0.0.1:47300"
        var request = URLRequest(url: URL(string: base + "/testpeer/" + action)!)
        request.httpMethod = "POST"
        var result: Result<Data, Error> = .failure(URLError(.timedOut))
        let done = expectation(description: "test runtime answered")
        URLSession.shared.dataTask(with: request) { data, _, error in
            if let data { result = .success(data) } else if let error { result = .failure(error) }
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 15)
        return try result.get()
    }

    private func tapWhenReady(_ element: XCUIElement, timeout: TimeInterval = 20) {
        XCTAssertTrue(element.waitForExistence(timeout: timeout), "\(element) never appeared")
        element.tap()
    }

    private func type(_ text: String, into field: XCUIElement) {
        tapWhenReady(field)
        field.typeText(text)
    }

    private func waitForDisappearance(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let gone = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: element)
        return XCTWaiter().wait(for: [gone], timeout: timeout) == .completed
    }
}
