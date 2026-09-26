import APIClient
import Domain
import Foundation
import Observation
import Persistence

/// The telemetry series offered on the detail chart.
enum ChartMetric: String, CaseIterable, Identifiable {
    case rumination
    case activity
    case temperature

    var id: Self { self }

    var metric: Metric {
        switch self {
        case .rumination: .rumination
        case .activity: .steps
        case .temperature: .temperature
        }
    }

    var title: LocalizedStringResource {
        switch self {
        case .rumination: "Rumination"
        case .activity: "Activity"
        case .temperature: "Temperature"
        }
    }

    var systemImage: String {
        switch self {
        case .rumination: "leaf.arrow.trianglehead.clockwise"
        case .activity: "figure.walk"
        case .temperature: "thermometer.medium"
        }
    }
}

@MainActor
@Observable
final class AnimalDetailModel {
    static let days = 30

    let animalID: AnimalID
    private(set) var animal: Animal?
    private(set) var phase: LoadPhase = .loading
    private(set) var isShowingCachedData = false
    private(set) var devices: [Device] = []
    private(set) var remarks: [LocalRemark] = []
    /// The selected metric's days, oldest first.
    private(set) var series: [DailyStatistic] = []
    /// The latest day of the selected metric, if any.
    private(set) var latest: DailyStatistic?
    /// Findings on the most recent day across every metric.
    private(set) var latestFlags: Set<TelemetryFlag> = []

    var metric: ChartMetric = .rumination {
        didSet { rebuildSeries() }
    }

    private var statistics: [DailyStatistic] = [] {
        didSet {
            rebuildSeries()
            let lastDay = statistics.map(\.day).max()
            latestFlags = Set(statistics.filter { $0.day == lastDay }.flatMap(\.flags))
        }
    }

    private let api: any AgritelemAPI
    private let database: AppDatabase
    private var cacheKey: String { "daily:\(animalID.key)" }

    init(animalID: AnimalID, api: any AgritelemAPI, database: AppDatabase) {
        self.animalID = animalID
        self.api = api
        self.database = database
    }

    func load(now: Date = .now) async {
        if statistics.isEmpty, let cached = try? await database.cached([DailyStatistic].self, key: cacheKey) {
            statistics = cached.value
            phase = .loaded
        }
        // The last complete UTC day and the 29 before it.
        let today = Calendar.utc.startOfDay(for: now)
        let to = Calendar.utc.date(byAdding: .day, value: -1, to: today) ?? today
        let from = Calendar.utc.date(byAdding: .day, value: -(Self.days - 1), to: to) ?? to

        async let fetchedDevices = try? api.devices()
        do {
            let fresh = try await api.dailyStatistics(for: animalID, from: from, to: to)
            try? await database.cache(fresh, key: cacheKey, at: now)
            statistics = fresh
            isShowingCachedData = false
            phase = .loaded
        } catch {
            if statistics.isEmpty {
                phase = .failed(HerdDashboardModel.message(for: error))
            } else {
                isShowingCachedData = true
            }
        }
        if let all = await fetchedDevices {
            devices = all.filter { $0.animal == animalID }
        }
    }

    func observeAnimal() async {
        do {
            for try await list in database.observeAnimals() {
                animal = list.first { $0.id == animalID }
            }
        } catch {}
    }

    func observeRemarks() async {
        do {
            for try await list in database.observeRemarks(for: animalID) {
                remarks = list
            }
        } catch {}
    }

    private func rebuildSeries() {
        series = statistics.filter { $0.metric == metric.metric }.sorted { $0.day < $1.day }
        latest = series.last
    }
}
