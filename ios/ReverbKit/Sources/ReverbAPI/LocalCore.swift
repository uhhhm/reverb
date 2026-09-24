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
