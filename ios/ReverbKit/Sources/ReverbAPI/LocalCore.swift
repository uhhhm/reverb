import Foundation
@_exported import OpenAPIRuntime
@_exported import OpenAPIURLSession

/// Where the core running inside the app serves its API.
public struct LocalCore: Sendable, Equatable {
    public let port: Int

    public init(port: Int) {
        self.port = port
    }

    /// The API root, as the core's loopback guards expect it: 127.0.0.1, not
    /// localhost, which could resolve to ::1.
    public var apiURL: URL { URL(string: "http://127.0.0.1:\(port)/api/v1")! }

    /// A client for the generated operations.
    public func client() -> Client {
        Client(serverURL: apiURL, transport: URLSessionTransport())
    }

    /// The bytes of a library track on this device, for AVPlayer.
    public func streamURL(trackID: String) -> URL {
        apiURL.appendingPathComponent("stream").appendingPathComponent(trackID)
    }

    /// Cover art by id, or nil when a track has none.
    public func coverURL(id: String) -> URL? {
        id.isEmpty ? nil : apiURL.appendingPathComponent("cover").appendingPathComponent(id)
    }
}
