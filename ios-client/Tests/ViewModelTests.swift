import APIClient
import Domain
import Foundation
import Persistence
import Testing
@testable import Agritelem

/// Serves demo data until `failing` is set, then fails like a dropped network.
private final class SwitchableAPI: AgritelemAPI, @unchecked Sendable {
    private let lock = NSLock()
    private var _failing = false
    var failing: Bool {
        get { lock.withLock { _failing } }
        set { lock.withLock { _failing = newValue } }
    }

    private let demo = DemoAPI()

    func herdOverview() async throws(APIError) -> HerdOverview {
        if failing { throw .transient("offline") }
        return try await demo.herdOverview()
    }

    func animals(modifiedFrom: Date?, pageToken: String?) async throws(APIError) -> Page<Animal> {
        try await demo.animals(modifiedFrom: modifiedFrom, pageToken: pageToken)
    }

    func devices() async throws(APIError) -> [Device] { try await demo.devices() }

    func dailyStatistics(for animal: AnimalID, from: Date, to: Date) async throws(APIError) -> [DailyStatistic] {
        if failing { throw .transient("offline") }
        return try await demo.dailyStatistics(for: animal, from: from, to: to)
    }

    func remarks(modifiedFrom: Date?, pageToken: String?) async throws(APIError) -> Page<Remark> {
        try await demo.remarks(modifiedFrom: modifiedFrom, pageToken: pageToken)
    }

    func submitRemark(_ remark: RemarkSubmission) async throws(APIError) -> RemarkPostOutcome {
        try await demo.submitRemark(remark)
    }
}

/// Serves an overview of normal animals with the given management tags.
private struct TagsAPI: AgritelemAPI {
    let tags: [String]

    func herdOverview() async throws(APIError) -> HerdOverview {
        let animals = tags.enumerated().map { i, tag in
            HerdAnimal(id: DemoData.id(i), managementTag: tag, statuses: [.normal], ruminationMinutes: nil)
        }
        return HerdOverview(asOf: .now, day: DemoData.lastDay, animalCount: animals.count, meanRuminationMinutes: nil,
                            estrusAlerts: 0, heatStress: 0, veterinaryAttention: 0, animals: animals)
    }

    func animals(modifiedFrom _: Date?, pageToken _: String?) async throws(APIError) -> Page<Animal> { Page(items: [], nextPageToken: nil) }
    func devices() async throws(APIError) -> [Device] { [] }
    func dailyStatistics(for _: AnimalID, from _: Date, to _: Date) async throws(APIError) -> [DailyStatistic] { [] }
    func remarks(modifiedFrom _: Date?, pageToken _: String?) async throws(APIError) -> Page<Remark> { Page(items: [], nextPageToken: nil) }
    func submitRemark(_: RemarkSubmission) async throws(APIError) -> RemarkPostOutcome { throw .notFound }
}

@MainActor
@Suite("HerdDashboardModel")
struct HerdDashboardModelTests {
    @Test func putsAnimalsNeedingAttentionFirstAndFilters() async throws {
        let model = HerdDashboardModel(api: DemoAPI(), database: try AppDatabase.inMemory())
        await model.load()

        #expect(model.phase == .loaded)
        #expect(model.attention.map(\.tag) == ["110", "103", "106"], "vet attention, then heat, then heat stress")
        #expect(model.rows.count == DemoData.names.count)

        model.filter = .inHeat
        #expect(model.rows.map(\.tag) == ["103"])

        model.filter = .all
        model.searchText = "10"
        #expect(model.rows.allSatisfy { $0.tag.contains("10") })
    }

    @Test func ordersTagsNumerically() async throws {
        let model = HerdDashboardModel(api: TagsAPI(tags: ["100", "99", "7"]), database: try AppDatabase.inMemory())
        await model.load()
        #expect(model.rows.map(\.tag) == ["7", "99", "100"])
    }

    @Test func usesSynchronisedNamesWhenAvailable() async throws {
        let database = try AppDatabase.inMemory()
        try await database.applyAnimals(DemoData.animals)
        let model = HerdDashboardModel(api: DemoAPI(), database: database)
        await model.load()
        let observation = Task { await model.observeAnimals() }
        defer { observation.cancel() }

        try await waitUntil { model.rows.contains { $0.name == "Daisy" } }
        #expect(model.rows.first { $0.tag == "103" }?.breedCode == "HOL")
    }

    @Test func fallsBackToCacheWhenOffline() async throws {
        let database = try AppDatabase.inMemory()
        let api = SwitchableAPI()
        await HerdDashboardModel(api: api, database: database).load()

        api.failing = true
        let offline = HerdDashboardModel(api: api, database: database)
        await offline.load()
        #expect(offline.phase == .loaded)
        #expect(offline.isShowingCachedData)
        #expect(offline.overview?.estrusAlerts == 1)
    }

    @Test func reportsFailureWithoutAnyCache() async throws {
        let api = SwitchableAPI()
        api.failing = true
        let model = HerdDashboardModel(api: api, database: try AppDatabase.inMemory())
        await model.load()
        guard case .failed = model.phase else {
            Issue.record("phase = \(model.phase), want failed")
            return
        }
    }
}

@MainActor
@Suite("AnimalDetailModel")
struct AnimalDetailModelTests {
    @Test func seriesFollowsSelectedMetric() async throws {
        let model = AnimalDetailModel(animalID: DemoData.id(DemoData.estrusIndex), api: DemoAPI(), database: try AppDatabase.inMemory())
        await model.load()

        #expect(model.series.count == 30)
        #expect(model.series.allSatisfy { $0.metric == .rumination })
        #expect(model.series == model.series.sorted { $0.day < $1.day })
        #expect(model.latestFlags.contains(.heatSignature))

        model.metric = .temperature
        #expect(model.series.allSatisfy { $0.metric == .temperature })
        #expect(model.latest?.metric == .temperature)
    }

    @Test func showsCachedTelemetryOffline() async throws {
        let database = try AppDatabase.inMemory()
        let api = SwitchableAPI()
        let id = DemoData.id(0)
        await AnimalDetailModel(animalID: id, api: api, database: database).load()

        api.failing = true
        let offline = AnimalDetailModel(animalID: id, api: api, database: database)
        await offline.load()
        #expect(offline.isShowingCachedData)
        #expect(offline.series.count == 30)
    }
}

@MainActor
@Suite("RemarkComposerModel")
struct RemarkComposerModelTests {
    @Test func requiresNonBlankNoteWithinLimit() {
        let model = RemarkComposerModel()
        #expect(!model.canSave)
        model.note = "   \n"
        #expect(!model.canSave)
        model.note = "Lame"
        #expect(model.canSave)
        model.note = String(repeating: "é", count: RemarkComposerModel.maxNoteLength + 1)
        #expect(!model.canSave)
        #expect(model.remainingCharacters == -1)
    }

    /// The server counts Unicode scalars (Go runes), not grapheme clusters:
    /// "👍🏽" is one character on screen but two scalars on the server.
    @Test func countsNoteLengthTheWayTheServerDoes() {
        let model = RemarkComposerModel()
        model.note = String(repeating: "👍🏽", count: 1001) // 1,001 graphemes, 2,002 scalars
        #expect(model.remainingCharacters == RemarkComposerModel.maxNoteLength - 2002)
        #expect(!model.canSave, "the server would reject this note")
    }

    @Test func submitsTrimmedNoteAndClampsFutureDates() async throws {
        let model = RemarkComposerModel()
        model.note = "  Lame  "
        model.eventDate = .now.addingTimeInterval(3600)
        var captured: (String, Date)?
        let ok = await model.submit { note, _, date in captured = (note, date) }
        #expect(ok)
        #expect(captured?.0 == "Lame")
        #expect(try #require(captured?.1) <= .now)
    }

    @Test func surfacesSaveErrors() async {
        let model = RemarkComposerModel()
        model.note = "Lame"
        let ok = await model.submit { _, _, _ in throw SignInError.signedOut }
        #expect(!ok)
        #expect(model.errorMessage == SignInError.signedOut.errorDescription)
    }
}

@Suite("TokenClaims")
struct TokenClaimsTests {
    @Test func readsSubjectAndExpiry() throws {
        // {"sub":"ie.example.gateway","exp":1790000000}
        let claims = try #require(TokenClaims(token: "e30.eyJzdWIiOiJpZS5leGFtcGxlLmdhdGV3YXkiLCJleHAiOjE3OTAwMDAwMDB9.sig"))
        #expect(claims.subject == "ie.example.gateway")
        #expect(claims.expires == Date(timeIntervalSince1970: 1_790_000_000))
    }

    @Test(arguments: ["", "not-a-jwt", "a.b", "e30.e30.sig", "e30.!!!.sig"])
    func rejectsMalformedTokens(token: String) {
        #expect(TokenClaims(token: token) == nil)
    }
}

/// Polls `condition` on the main actor until it holds or a second passes.
@MainActor
private func waitUntil(_ condition: () -> Bool) async throws {
    for _ in 0..<100 where !condition() {
        try await Task.sleep(for: .milliseconds(10))
    }
    #expect(condition())
}
