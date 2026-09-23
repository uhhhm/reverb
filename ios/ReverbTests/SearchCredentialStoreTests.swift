import XCTest
@testable import Reverb

final class SearchCredentialStoreTests: XCTestCase {
    func testCredentialsStayInKeychainAndUnchangedSaveDoesNotRestartCore() throws {
        let previous = SearchCredentialStore.load()
        defer {
            if let previous { _ = try? SearchCredentialStore.save(previous) }
            else { SearchCredentialStore.clear() }
        }
        let credentials = SpotifyCredentials(clientId: "test-client", clientSecret: "test-secret")
        XCTAssertTrue(try SearchCredentialStore.save(credentials))
        XCTAssertEqual(SearchCredentialStore.load(), credentials)
        XCTAssertFalse(try SearchCredentialStore.save(credentials))
    }
}
