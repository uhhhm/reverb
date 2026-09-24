import Foundation
import HTTPTypes
@_exported import OpenAPIRuntime
@_exported import OpenAPIURLSession

/// Where the core running inside the app serves its API, and the secret it
/// requires. Other apps on the phone can reach a loopback port, so the core
/// refuses any request without this launch's secret, which reaches the app
/// across the gomobile binding and never over HTTP. Every request built here
/// carries it.
public struct LocalCore: Sendable, Equatable {
    public let port: Int
    let secret: String

    /// The header the core reads the secret from.
    public static let secretHeader = "X-Reverb-Secret"

    public init(port: Int, secret: String) {
        self.port = port
        self.secret = secret
    }

    /// The API root, as the core's loopback guards expect it: 127.0.0.1, not
    /// localhost, which could resolve to ::1.
    public var apiURL: URL { URL(string: "http://127.0.0.1:\(port)/api/v1")! }

    /// A client for the generated operations.
    public func client() -> Client {
        Client(serverURL: apiURL, transport: URLSessionTransport(), middlewares: [SecretMiddleware(secret: secret)])
    }

    /// A request to the core for a URL built from `apiURL`.
    public func request(_ url: URL) -> URLRequest {
        var request = URLRequest(url: url)
        request.setValue(secret, forHTTPHeaderField: Self.secretHeader)
        return request
    }

    /// The headers AVPlayer sends with each request for a stream.
    public var streamHeaders: [String: String] { [Self.secretHeader: secret] }

    /// The bytes of a library track on this device, for AVPlayer with
    /// `streamHeaders`.
    public func streamURL(trackID: String) -> URL {
        apiURL.appendingPathComponent("stream").appendingPathComponent(trackID)
    }

    /// A search result or recommendation outside the library, streamed from
    /// its source through the core's yt-dlp, for AVPlayer with
    /// `streamHeaders`. The artist and title help the core find it.
    public func externalStreamURL(source: String, externalId: String, artist: String?, title: String?) -> URL {
        external(source: source, externalId: externalId, suffix: nil, artist: artist, title: title)
    }

    /// Asks the core to resolve an external track ahead of playing it; POST
    /// it with `request`.
    public func externalPrewarmURL(source: String, externalId: String, artist: String?, title: String?) -> URL {
        external(source: source, externalId: externalId, suffix: "prewarm", artist: artist, title: title)
    }

    private func external(source: String, externalId: String, suffix: String?, artist: String?, title: String?) -> URL {
        var url = apiURL.appendingPathComponent("external/stream")
            .appendingPathComponent(source).appendingPathComponent(externalId)
        if let suffix { url.appendPathComponent(suffix) }
        var components = URLComponents(url: url, resolvingAgainstBaseURL: false)!
        let hints = [("artist", artist), ("title", title)].compactMap { name, value in
            value.flatMap { $0.isEmpty ? nil : URLQueryItem(name: name, value: $0) }
        }
        if !hints.isEmpty { components.queryItems = hints }
        return components.url ?? url
    }

    /// Cover art by id, or nil when a track has none. Fetch it with `request`.
    public func coverURL(id: String) -> URL? {
        id.isEmpty ? nil : apiURL.appendingPathComponent("cover").appendingPathComponent(id)
    }
}

/// Adds the launch secret to every generated operation.
struct SecretMiddleware: ClientMiddleware {
    let secret: String

    func intercept(
        _ request: HTTPRequest,
        body: HTTPBody?,
        baseURL: URL,
        operationID: String,
        next: (HTTPRequest, HTTPBody?, URL) async throws -> (HTTPResponse, HTTPBody?)
    ) async throws -> (HTTPResponse, HTTPBody?) {
        var request = request
        request.headerFields[HTTPField.Name(LocalCore.secretHeader)!] = secret
        return try await next(request, body, baseURL)
    }
}
