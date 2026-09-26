import Foundation

/// An ICAR animal identifier: a scheme (such as `std.iso.11785`) and an ID.
public struct AnimalID: Hashable, Sendable, Codable {
    public var scheme: String
    public var id: String

    public init(scheme: String, id: String) {
        self.scheme = scheme
        self.id = id
    }

    /// A stable single-string key for storage and navigation.
    public var key: String { "\(scheme)|\(id)" }

    /// The ID grouped the way it is printed on ISO 11784 tags:
    /// country or manufacturer code, then the national number.
    public var displayID: String {
        guard id.count == 15, id.allSatisfy(\.isASCII) else { return id }
        return "\(id.prefix(3)) \(id.dropFirst(3))"
    }
}

/// An ICAR location (farm or herd) identifier.
public struct LocationID: Hashable, Sendable, Codable {
    public var scheme: String
    public var id: String

    public init(scheme: String, id: String) {
        self.scheme = scheme
        self.id = id
    }
}

/// An ICAR metric identifier.
public struct Metric: Hashable, Sendable, Codable {
    public var scheme: String
    public var id: String

    public init(scheme: String, id: String) {
        self.scheme = scheme
        self.id = id
    }

    public static let rumination = Metric(scheme: "org.icar.activities", id: "RuminationTime")
    public static let eating = Metric(scheme: "org.icar.activities", id: "EatingTime")
    public static let lying = Metric(scheme: "org.icar.activities", id: "LyingTime")
    public static let steps = Metric(scheme: "org.icar.activities", id: "StepCount")
    public static let temperature = Metric(scheme: "agritelem.metrics", id: "ReticularTemperature")
}
