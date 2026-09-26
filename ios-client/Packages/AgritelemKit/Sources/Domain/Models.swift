import Foundation

/// An animal as listed for a location.
public struct Animal: Identifiable, Hashable, Sendable {
    public var id: AnimalID
    public var name: String?
    public var managementTag: String?
    public var breedCode: String?
    public var birthDate: Date?
    public var modified: Date
    public var isDeleted: Bool

    public init(id: AnimalID, name: String?, managementTag: String?, breedCode: String?, birthDate: Date?, modified: Date, isDeleted: Bool = false) {
        self.id = id
        self.name = name
        self.managementTag = managementTag
        self.breedCode = breedCode
        self.birthDate = birthDate
        self.modified = modified
        self.isDeleted = isDeleted
    }
}

/// Dashboard status derived by the server for the last complete UTC day.
public enum AnimalStatus: String, Hashable, Sendable, CaseIterable, Codable {
    case normal = "Normal"
    case inHeat = "InHeat"
    case veterinaryAttention = "VeterinaryAttention"
    case heatStress = "HeatStress"
}

/// One animal's row in the herd overview.
public struct HerdAnimal: Identifiable, Hashable, Sendable, Codable {
    public var id: AnimalID
    public var managementTag: String?
    public var statuses: [AnimalStatus]
    public var ruminationMinutes: Double?

    public init(id: AnimalID, managementTag: String?, statuses: [AnimalStatus], ruminationMinutes: Double?) {
        self.id = id
        self.managementTag = managementTag
        self.statuses = statuses
        self.ruminationMinutes = ruminationMinutes
    }
}

/// Herd KPIs and per-animal status for one location.
public struct HerdOverview: Hashable, Sendable, Codable {
    public var asOf: Date
    /// The complete UTC day the telemetry KPIs describe, at 00:00 UTC.
    public var day: Date
    public var animalCount: Int
    public var meanRuminationMinutes: Double?
    public var estrusAlerts: Int
    public var heatStress: Int
    public var veterinaryAttention: Int
    public var animals: [HerdAnimal]

    public init(asOf: Date, day: Date, animalCount: Int, meanRuminationMinutes: Double?, estrusAlerts: Int, heatStress: Int, veterinaryAttention: Int, animals: [HerdAnimal]) {
        self.asOf = asOf
        self.day = day
        self.animalCount = animalCount
        self.meanRuminationMinutes = meanRuminationMinutes
        self.estrusAlerts = estrusAlerts
        self.heatStress = heatStress
        self.veterinaryAttention = veterinaryAttention
        self.animals = animals
    }
}

/// Quality of a derived daily value.
public enum DataQuality: String, Hashable, Sendable, Codable {
    case verified = "Verified"
    case imputed = "Imputed"
}

/// Cross-metric findings for a day.
public enum TelemetryFlag: String, Hashable, Sendable, Codable {
    case heatSignature = "HeatSignature"
    case timeBudgetConflict = "TimeBudgetConflict"
    case thermalStress = "ThermalStress"
    case dailyOutOfRange = "DailyOutOfRange"
}

/// A derived daily statistic for one metric.
public struct DailyStatistic: Identifiable, Hashable, Sendable, Codable {
    public var metric: Metric
    public var day: Date
    public var value: Double
    public var coverage: Double
    public var isIncomplete: Bool
    public var quality: DataQuality
    public var baselineMedian: Double?
    public var zScore: Double?
    public var flags: [TelemetryFlag]

    public var id: String { "\(metric.id)@\(day.timeIntervalSince1970)" }

    public init(metric: Metric, day: Date, value: Double, coverage: Double, isIncomplete: Bool, quality: DataQuality, baselineMedian: Double?, zScore: Double?, flags: [TelemetryFlag]) {
        self.metric = metric
        self.day = day
        self.value = value
        self.coverage = coverage
        self.isIncomplete = isIncomplete
        self.quality = quality
        self.baselineMedian = baselineMedian
        self.zScore = zScore
        self.flags = flags
    }
}

/// A sensor device and its current assignment.
public struct Device: Identifiable, Hashable, Sendable {
    public var id: String
    public var name: String?
    public var serial: String?
    public var animal: AnimalID?
    public var assignedSince: Date?

    public init(id: String, name: String?, serial: String?, animal: AnimalID?, assignedSince: Date?) {
        self.id = id
        self.name = name
        self.serial = serial
        self.animal = animal
        self.assignedSince = assignedSince
    }
}

/// Remark categories (Agritelem extension to ICAR remarks).
public enum RemarkCategory: String, Hashable, Sendable, CaseIterable, Codable {
    case general = "General"
    case veterinary = "Veterinary"
    case behaviour = "Behaviour"
    case feeding = "Feeding"
}

/// An ICAR remark event.
public struct Remark: Identifiable, Hashable, Sendable {
    /// The client-generated `meta.sourceId`: stable from creation, before
    /// the server assigns its own ID.
    public var sourceID: String
    public var serverID: String?
    public var animal: AnimalID
    public var eventDate: Date
    public var note: String
    public var category: RemarkCategory
    public var source: String
    public var modified: Date

    public var id: String { sourceID }

    public init(sourceID: String, serverID: String?, animal: AnimalID, eventDate: Date, note: String, category: RemarkCategory, source: String, modified: Date) {
        self.sourceID = sourceID
        self.serverID = serverID
        self.animal = animal
        self.eventDate = eventDate
        self.note = note
        self.category = category
        self.source = source
        self.modified = modified
    }
}

/// A problem reported by the server for a rejected write.
public struct APIProblem: Hashable, Sendable, Codable {
    public var code: String
    public var field: String?
    public var detail: String

    public init(code: String, field: String?, detail: String) {
        self.code = code
        self.field = field
        self.detail = detail
    }
}
