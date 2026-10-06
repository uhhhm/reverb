import XCTest
@testable import Reverb

/// The signature and SideStore hand-off a sideloaded install depends on: a
/// wrong expiry hides the only warning before the app stops launching, and a
/// mangled link sends SideStore a URL it cannot install.
final class SideloadingTests: XCTestCase {
    private let expiry = ISO8601DateFormatter().date(from: "2026-10-12T09:30:00Z")!

    /// A provisioning profile is a CMS envelope with the plist inside it.
    private func profile(_ dict: String) -> Data {
        var data = Data([0x30, 0x80, 0x06, 0x09, 0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x07, 0x02, 0xA0, 0x80])
        data.append(Data("""
        <?xml version="1.0" encoding="UTF-8"?>
        <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
        <plist version="1.0"><dict>\(dict)</dict></plist>
        """.utf8))
        data.append(Data([0x00, 0x00, 0xA1, 0x82, 0x01, 0xFF]))
        return data
    }

    func testReadsExpiryFromSignedProfile() {
        let data = profile("<key>Name</key><string>Reverb</string><key>ExpirationDate</key><date>2026-10-12T09:30:00Z</date>")
        XCTAssertEqual(SigningProfile.expiry(of: data), expiry)
    }

    func testNoProfileOrUnreadableProfileHasNoExpiry() {
        XCTAssertNil(SigningProfile.expiry(of: nil))
        XCTAssertNil(SigningProfile.expiry(of: Data()))
        XCTAssertNil(SigningProfile.expiry(of: Data([0x30, 0x80, 0xFF, 0x00])))
        XCTAssertNil(SigningProfile.expiry(of: Data("<?xml version=\"1.0\"?><plist><dict>".utf8)))
        XCTAssertNil(SigningProfile.expiry(of: profile("<key>Name</key><string>Reverb</string>")))
        XCTAssertNil(SigningProfile.expiry(of: profile("<key>ExpirationDate</key><string>next week</string>")))
    }

    func testWarnsOnlyInTheLastTwoDays() {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        func days(_ now: String) -> Int? {
            SigningProfile.daysLeftToWarn(expiry: expiry, now: ISO8601DateFormatter().date(from: now)!, calendar: calendar)
        }
        // A fresh free signature, and a paid account's year.
        XCTAssertNil(days("2026-10-05T09:30:00Z"))
        XCTAssertNil(SigningProfile.daysLeftToWarn(expiry: expiry.addingTimeInterval(300 * 86400), now: expiry, calendar: calendar))
        // Counted in calendar days, so late on the 9th is still three days out.
        XCTAssertNil(days("2026-10-09T23:59:00Z"))
        XCTAssertEqual(days("2026-10-10T00:01:00Z"), 2)
        XCTAssertEqual(days("2026-10-11T22:00:00Z"), 1)
        XCTAssertEqual(days("2026-10-12T09:00:00Z"), 0)
        // Running past expiry is possible until iOS rechecks; still warn.
        XCTAssertEqual(days("2026-10-12T10:00:00Z"), 0)
    }

    func testInstallAsksSideStoreThenAltStoreThenTheWeb() {
        let ipa = "https://github.com/o/r/releases/download/v1.2.0/Reverb.ipa?x=1&y=2"
        let release = "https://github.com/o/r/releases/tag/v1.2.0"
        let links = Sideloader.install(ipa: ipa, fallback: release)
        XCTAssertEqual(links.map { $0.scheme }, ["sidestore", "altstore", "https"])
        for link in links.prefix(2) {
            let parts = URLComponents(url: link, resolvingAgainstBaseURL: false)!
            XCTAssertEqual(parts.host, "install")
            // SideStore reads exactly one url item and decodes it.
            XCTAssertEqual(parts.queryItems, [URLQueryItem(name: "url", value: ipa)])
        }
        XCTAssertEqual(links.last?.absoluteString, release)
    }

    func testSourceIsAddedThroughSideStoreThenAltStore() {
        let source = "https://github.com/o/r/releases/download/ios-source/source.json"
        let links = Sideloader.addSource(source)
        XCTAssertEqual(links.map { $0.absoluteString }, [
            "sidestore://source?url=https%3A%2F%2Fgithub.com%2Fo%2Fr%2Freleases%2Fdownload%2Fios-source%2Fsource.json",
            "altstore://source?url=https%3A%2F%2Fgithub.com%2Fo%2Fr%2Freleases%2Fdownload%2Fios-source%2Fsource.json",
        ])
    }

    func testEmptyURLsOfferNothingToOpen() {
        XCTAssertEqual(Sideloader.install(ipa: "", fallback: ""), [])
        XCTAssertEqual(Sideloader.install(ipa: "", fallback: "https://github.com/o/r/releases/tag/v1").map(\.scheme), ["https"])
        XCTAssertEqual(Sideloader.addSource(""), [])
    }

    func testRefreshOpensSideStoreThenAltStore() {
        XCTAssertEqual(Sideloader.refresh.map(\.absoluteString), ["sidestore://", "altstore://"])
    }
}
