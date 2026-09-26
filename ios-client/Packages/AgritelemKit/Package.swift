// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "AgritelemKit",
    defaultLocalization: "en",
    platforms: [.iOS(.v26), .macOS(.v26)],
    products: [
        .library(name: "AgritelemKit", targets: ["Domain", "APIClient", "Persistence", "Sync"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-openapi-generator", from: "1.13.1"),
        .package(url: "https://github.com/apple/swift-openapi-runtime", from: "1.12.1"),
        .package(url: "https://github.com/apple/swift-openapi-urlsession", from: "1.3.1"),
        .package(url: "https://github.com/apple/swift-http-types", from: "1.8.0"),
        .package(url: "https://github.com/groue/GRDB.swift", from: "7.11.1"),
    ],
    targets: [
        // Value types shared by every layer; no dependencies.
        .target(name: "Domain"),

        // Typed client generated from contracts/openapi.json, wrapped behind
        // the `AgritelemAPI` protocol so nothing else sees generated types.
        .target(
            name: "APIClient",
            dependencies: [
                "Domain",
                .product(name: "OpenAPIRuntime", package: "swift-openapi-runtime"),
                .product(name: "OpenAPIURLSession", package: "swift-openapi-urlsession"),
                .product(name: "HTTPTypes", package: "swift-http-types"),
            ],
            plugins: [.plugin(name: "OpenAPIGenerator", package: "swift-openapi-generator")]
        ),

        // SQLite cache and mutation queue.
        .target(name: "Persistence", dependencies: ["Domain", .product(name: "GRDB", package: "GRDB.swift")]),

        // Offline-first synchronisation between Persistence and the API.
        .target(name: "Sync", dependencies: ["Domain", "APIClient", "Persistence"]),

        .testTarget(
            name: "APIClientTests",
            dependencies: [
                "APIClient",
                .product(name: "OpenAPIRuntime", package: "swift-openapi-runtime"),
                .product(name: "HTTPTypes", package: "swift-http-types"),
            ],
            resources: [.copy("Fixtures")]
        ),
        .testTarget(name: "PersistenceTests", dependencies: ["Persistence"]),
        .testTarget(name: "SyncTests", dependencies: ["Sync"]),
    ],
    swiftLanguageModes: [.v6]
)
