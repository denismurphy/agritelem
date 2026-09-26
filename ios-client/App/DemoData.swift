#if DEBUG
import APIClient
import Domain
import Foundation
import Persistence

/// Deterministic sample data for previews and UI tests, so neither needs a
/// server. Mirrors the backend's gateway simulator: one animal in estrus and
/// one with an elevated temperature on the last complete day.
enum DemoData {
    static let names = ["Bessie", "Clover", "Daisy", "Buttercup", "Primrose", "Hazel", "Willow", "Maple",
                        "Poppy", "Rosie", "Bluebell", "Heather"]
    static let breeds = ["HOL", "HOL", "HOL", "JER", "BSW", "HOL", "MON", "JER"]
    static let estrusIndex = 2
    static let feverIndex = 5

    static let lastDay: Date = Calendar.utc.date(byAdding: .day, value: -1, to: Calendar.utc.startOfDay(for: .now))!

    static func id(_ index: Int) -> AnimalID {
        AnimalID(scheme: "std.iso.11785", id: String(format: "372%012d", 100_000_001 + index))
    }

    static var animals: [Animal] {
        names.indices.map { i in
            Animal(id: id(i), name: names[i], managementTag: String(101 + i), breedCode: breeds[i % breeds.count],
                   birthDate: Calendar.utc.date(from: DateComponents(year: 2019 + i % 5, month: 1 + i % 12, day: 1 + i % 27)),
                   modified: lastDay)
        }
    }

    /// A small deterministic generator (SplitMix64), so previews are stable.
    struct Noise {
        var state: UInt64
        mutating func next() -> Double {
            state &+= 0x9E37_79B9_7F4A_7C15
            var z = state
            z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
            z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
            return Double((z ^ (z >> 31)) >> 11) / Double(1 << 53) * 2 - 1
        }
    }

    static func dailyStatistics(for index: Int, days: Int = 30) -> [DailyStatistic] {
        var noise = Noise(state: UInt64(index + 1))
        var result: [DailyStatistic] = []
        for metric in [Metric.rumination, .steps, .temperature] {
            var values: [Double] = []
            for offset in stride(from: days - 1, through: 0, by: -1) {
                let day = Calendar.utc.date(byAdding: .day, value: -offset, to: lastDay)!
                let isLast = offset == 0
                var value: Double
                switch metric {
                case .rumination: value = 470 + noise.next() * 18 - (isLast && index == estrusIndex ? 130 : 0)
                case .steps: value = 3000 + noise.next() * 220 + (isLast && index == estrusIndex ? 4200 : 0)
                default: value = 38.9 + noise.next() * 0.12 + (isLast && index == feverIndex ? 1.3 : 0)
                }
                let history = values.suffix(14)
                let baseline = history.count >= 7 ? median(Array(history)) : nil
                let mad = baseline.map { b in median(history.map { abs($0 - b) }) }
                var z: Double?
                if let baseline, let mad, mad > 0 {
                    z = (value - baseline) / (1.4826 * mad)
                }
                // Day 20 back lost a sensor for most of the day.
                let imputed = offset == 20 && metric != .temperature
                var flags: [TelemetryFlag] = []
                if isLast, index == estrusIndex, metric != .temperature { flags.append(.heatSignature) }
                if isLast, index == feverIndex, metric == .temperature { flags.append(.thermalStress) }
                result.append(DailyStatistic(metric: metric, day: day, value: value, coverage: imputed ? 0.6 : 1,
                                             isIncomplete: imputed, quality: imputed ? .imputed : .verified,
                                             baselineMedian: baseline, zScore: z, flags: flags))
                values.append(value)
            }
        }
        return result
    }

    static var overview: HerdOverview {
        let herd = names.indices.map { i in
            let rumination = dailyStatistics(for: i).last { $0.metric == .rumination }?.value
            var statuses: [AnimalStatus] = []
            if i == estrusIndex { statuses.append(.inHeat) }
            if i == feverIndex { statuses.append(.heatStress) }
            if i == 9 { statuses.append(.veterinaryAttention) }
            return HerdAnimal(id: id(i), managementTag: String(101 + i), statuses: statuses.isEmpty ? [.normal] : statuses,
                              ruminationMinutes: rumination)
        }
        let verified = herd.compactMap(\.ruminationMinutes)
        return HerdOverview(asOf: .now, day: lastDay, animalCount: herd.count,
                            meanRuminationMinutes: verified.reduce(0, +) / Double(verified.count),
                            estrusAlerts: 1, heatStress: 1, veterinaryAttention: 1, animals: herd)
    }

    static var remarks: [Remark] {
        [
            Remark(sourceID: "demo-1", serverID: "1", animal: id(estrusIndex), eventDate: lastDay.addingTimeInterval(9 * 3600),
                   note: "Standing to be mounted in the collecting yard.", category: .behaviour, source: "ie.example.app", modified: lastDay),
            Remark(sourceID: "demo-2", serverID: "2", animal: id(9), eventDate: lastDay.addingTimeInterval(-86_400),
                   note: "Slight lameness on the left hind. Hoof check booked.", category: .veterinary, source: "ie.example.app", modified: lastDay),
        ]
    }

    private static func median(_ values: [Double]) -> Double {
        let sorted = values.sorted()
        let n = sorted.count
        return n % 2 == 1 ? sorted[n / 2] : (sorted[n / 2 - 1] + sorted[n / 2]) / 2
    }
}

/// Serves `DemoData` through the API protocol.
struct DemoAPI: AgritelemAPI {
    func herdOverview() async throws(APIError) -> HerdOverview { DemoData.overview }

    func animals(modifiedFrom _: Date?, pageToken _: String?) async throws(APIError) -> Page<Animal> {
        Page(items: DemoData.animals, nextPageToken: nil)
    }

    func devices() async throws(APIError) -> [Device] {
        DemoData.names.indices.map { i in
            Device(id: "bolus-\(101 + i)", name: "Reticular bolus", serial: String(format: "RB-%06d", 40_000 + i),
                   animal: DemoData.id(i), assignedSince: Calendar.utc.date(from: DateComponents(year: 2025, month: 1, day: 1)))
        }
    }

    func dailyStatistics(for animal: AnimalID, from _: Date, to _: Date) async throws(APIError) -> [DailyStatistic] {
        guard let index = DemoData.names.indices.first(where: { DemoData.id($0) == animal }) else { throw .notFound }
        return DemoData.dailyStatistics(for: index)
    }

    func remarks(modifiedFrom _: Date?, pageToken _: String?) async throws(APIError) -> Page<Remark> {
        Page(items: DemoData.remarks, nextPageToken: nil)
    }

    func submitRemark(_ remark: RemarkSubmission) async throws(APIError) -> RemarkPostOutcome {
        .stored(Remark(sourceID: remark.sourceID, serverID: UUID().uuidString, animal: remark.animal, eventDate: remark.eventDate,
                       note: remark.note, category: remark.category, source: remark.source, modified: .now))
    }
}

extension AppModel {
    /// A self-contained model backed by `DemoAPI` and an in-memory store.
    static func demo() -> AppModel {
        guard let database = try? AppDatabase.inMemory() else { fatalError("in-memory database unavailable") }
        let claims = TokenClaims(token: demoToken)
        return AppModel(database: database, api: DemoAPI(), serverURL: defaultServerURL, location: demoLocation,
                        credentials: nil, claims: claims)
    }

    /// An unsigned token whose only use is to name the demo source.
    private static let demoToken = "e30.eyJzdWIiOiJpZS5leGFtcGxlLmFwcCJ9.demo"
}
#endif
