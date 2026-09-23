// swift-tools-version: 5.9
import PackageDescription

// ReverbAPI is the Swift client for the core's loopback API. The
// swift-openapi-generator build plugin generates it from openapi.yaml, the
// subset of internal/api/openapi.yaml the app calls, which
// tools/contracts/generate.mjs writes and `make contracts-check` checks.
let package = Package(
    name: "ReverbKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "ReverbAPI", targets: ["ReverbAPI"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-openapi-generator", from: "1.6.0"),
        .package(url: "https://github.com/apple/swift-openapi-runtime", from: "1.7.0"),
        .package(url: "https://github.com/apple/swift-openapi-urlsession", from: "1.0.2"),
        .package(url: "https://github.com/apple/swift-http-types", from: "1.0.0"),
    ],
    targets: [
        .target(
            name: "ReverbAPI",
            dependencies: [
                .product(name: "OpenAPIRuntime", package: "swift-openapi-runtime"),
                .product(name: "OpenAPIURLSession", package: "swift-openapi-urlsession"),
                .product(name: "HTTPTypes", package: "swift-http-types"),
            ],
            plugins: [
                .plugin(name: "OpenAPIGenerator", package: "swift-openapi-generator"),
            ]
        ),
    ]
)
