import APIClient
import Domain
import Foundation
import Observation
import Persistence

/// One animal as shown on the dashboard.
struct HerdRow: Identifiable, Equatable {
    var id: AnimalID
    var name: String
    var tag: String
    var breedCode: String?
    var statuses: [AnimalStatus]
    var ruminationMinutes: Double?

    var primaryStatus: AnimalStatus {
        statuses.min { $0.urgency < $1.urgency } ?? .normal
    }
}

/// Dashboard filter chips.
enum StatusFilter: String, CaseIterable, Identifiable {
    case all
    case inHeat
    case heatStress
    case veterinary

    var id: Self { self }

    var title: LocalizedStringResource {
        switch self {
        case .all: "All"
        case .inHeat: AnimalStatus.inHeat.title
        case .heatStress: AnimalStatus.heatStress.title
        case .veterinary: AnimalStatus.veterinaryAttention.title
        }
    }

    var status: AnimalStatus? {
        switch self {
        case .all: nil
        case .inHeat: .inHeat
        case .heatStress: .heatStress
        case .veterinary: .veterinaryAttention
        }
    }
}

@MainActor
@Observable
final class HerdDashboardModel {
    private(set) var phase: LoadPhase = .loading
    private(set) var overview: HerdOverview?
    private(set) var fetchedAt: Date?
    /// True when the overview shown is a cached copy because refresh failed.
    private(set) var isShowingCachedData = false
    private(set) var rows: [HerdRow] = []
    private(set) var attention: [HerdRow] = []

    var searchText = "" {
        didSet { rebuildRows() }
    }

    var filter: StatusFilter = .all {
        didSet { rebuildRows() }
    }

    private var animals: [AnimalID: Animal] = [:] {
        didSet { rebuildRows() }
    }

    private let api: any AgritelemAPI
    private let database: AppDatabase
    private static let cacheKey = "herd-overview"

    init(api: any AgritelemAPI, database: AppDatabase) {
        self.api = api
        self.database = database
    }

    /// Shows the cached overview immediately, then refreshes it.
    func load() async {
        if overview == nil, let cached = try? await database.cached(HerdOverview.self, key: Self.cacheKey) {
            apply(cached.value, fetchedAt: cached.fetchedAt)
        }
        await refresh()
    }

    func refresh() async {
        do {
            let fresh = try await api.herdOverview()
            try? await database.cache(fresh, key: Self.cacheKey, at: .now)
            apply(fresh, fetchedAt: .now)
            isShowingCachedData = false
            phase = .loaded
        } catch {
            if overview == nil {
                phase = .failed(Self.message(for: error))
            } else {
                isShowingCachedData = true
                phase = .loaded
            }
        }
    }

    /// Keeps names and breeds in step with the synchronised animal list.
    func observeAnimals() async {
        do {
            for try await list in database.observeAnimals() {
                animals = Dictionary(list.map { ($0.id, $0) }, uniquingKeysWith: { $1 })
            }
        } catch {
            // Names fall back to tags; the dashboard still works.
        }
    }

    private func apply(_ overview: HerdOverview, fetchedAt: Date) {
        self.overview = overview
        self.fetchedAt = fetchedAt
        rebuildRows()
    }

    private func rebuildRows() {
        guard let overview else {
            rows = []
            attention = []
            return
        }
        let all = overview.animals.map { herdAnimal in
            let animal = animals[herdAnimal.id]
            let tag = herdAnimal.managementTag ?? animal?.managementTag ?? String(herdAnimal.id.id.suffix(4))
            return HerdRow(
                id: herdAnimal.id,
                name: animal?.name ?? String(localized: "Tag \(tag)"),
                tag: tag,
                breedCode: animal?.breedCode,
                statuses: herdAnimal.statuses,
                ruminationMinutes: herdAnimal.ruminationMinutes
            )
        }
        .sorted { a, b in
            if a.primaryStatus.urgency != b.primaryStatus.urgency {
                return a.primaryStatus.urgency < b.primaryStatus.urgency
            }
            // Numeric-aware, so tag 99 comes before tag 100.
            return a.tag.localizedStandardCompare(b.tag) == .orderedAscending
        }

        attention = all.filter { $0.primaryStatus != .normal }
        let query = searchText.trimmingCharacters(in: .whitespaces)
        rows = all.filter { row in
            (filter.status.map(row.statuses.contains) ?? true)
                && (query.isEmpty || row.name.localizedStandardContains(query) || row.tag.localizedStandardContains(query))
        }
    }

    static func message(for error: any Error) -> String {
        switch error as? APIError {
        case .unauthorized?: String(localized: "Sign in again to see your herd.")
        case .forbidden?: String(localized: "This account can't see this farm.")
        case .transient?: String(localized: "Can't reach the server. Check your connection.")
        default: String(localized: "Something went wrong loading the herd.")
        }
    }
}
