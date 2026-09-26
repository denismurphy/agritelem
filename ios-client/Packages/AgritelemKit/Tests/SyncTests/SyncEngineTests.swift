import APIClient
import Domain
import Foundation
import Persistence
import Testing
@testable import Sync

private let t0 = Date(timeIntervalSince1970: 1_790_000_000)
private let cow = AnimalID(scheme: "std.iso.11785", id: "372000100000003")

/// A scripted API that records what it was asked.
private final class FakeAPI: AgritelemAPI, @unchecked Sendable {
    private let lock = NSLock()
    private var submitResults: [Result<RemarkPostOutcome, APIError>]
    private var animalPages: [Result<Page<Animal>, APIError>]
    private var remarkPages: [Result<Page<Remark>, APIError>]
    private let submitDelay: Duration
    private(set) var submitted: [String] = []
    private(set) var animalQueries: [Date?] = []

    init(submit: [Result<RemarkPostOutcome, APIError>] = [], animals: [Result<Page<Animal>, APIError>] = [], remarks: [Result<Page<Remark>, APIError>] = [], submitDelay: Duration = .zero) {
        self.submitResults = submit
        self.animalPages = animals
        self.remarkPages = remarks
        self.submitDelay = submitDelay
    }

    private func next<T>(_ queue: inout [Result<T, APIError>], empty: T) -> Result<T, APIError> {
        queue.isEmpty ? .success(empty) : queue.removeFirst()
    }

    func submitRemark(_ remark: RemarkSubmission) async throws(APIError) -> RemarkPostOutcome {
        try? await Task.sleep(for: submitDelay)
        let result = lock.withLock {
            submitted.append(remark.sourceID)
            return next(&submitResults, empty: .stored(stored(remark.sourceID)))
        }
        return try result.get()
    }

    func animals(modifiedFrom: Date?, pageToken _: String?) async throws(APIError) -> Page<Animal> {
        let result = lock.withLock {
            animalQueries.append(modifiedFrom)
            return next(&animalPages, empty: Page(items: [], nextPageToken: nil))
        }
        return try result.get()
    }

    func remarks(modifiedFrom _: Date?, pageToken _: String?) async throws(APIError) -> Page<Remark> {
        try lock.withLock { next(&remarkPages, empty: Page(items: [], nextPageToken: nil)) }.get()
    }

    func herdOverview() async throws(APIError) -> HerdOverview { throw .notFound }
    func devices() async throws(APIError) -> [Device] { [] }
    func dailyStatistics(for _: AnimalID, from _: Date, to _: Date) async throws(APIError) -> [DailyStatistic] { [] }
}

private func stored(_ sourceID: String, note: String = "Lame") -> Remark {
    Remark(sourceID: sourceID, serverID: "srv-\(sourceID)", animal: cow, eventDate: t0, note: note,
           category: .veterinary, source: "ie.app", modified: t0.addingTimeInterval(5))
}

private func animal(_ n: Int, modified: Date) -> Animal {
    Animal(id: AnimalID(scheme: "std.iso.11785", id: "37200010000000\(n)"), name: "Cow \(n)", managementTag: "10\(n)",
           breedCode: "HOL", birthDate: nil, modified: modified)
}

private func queue(_ db: AppDatabase, _ ids: String...) async throws {
    for id in ids {
        try await db.enqueueRemark(RemarkSubmissionPayload(sourceID: id, source: "ie.app", creator: nil, animal: cow,
                                                           eventDate: t0, note: "Lame", category: .veterinary, modified: t0), at: t0)
    }
}

@Suite("SyncEngine")
struct SyncEngineTests {
    @Test func uploadsQueuedRemarksAndAdoptsServerIDs() async throws {
        let db = try AppDatabase.inMemory()
        try await queue(db, "a", "b")
        let api = FakeAPI()
        let report = await SyncEngine(database: db, api: api, now: { t0 }).sync()

        #expect(report.outcome == .completed)
        #expect(report.uploaded == 2)
        #expect(api.submitted == ["a", "b"])
        #expect(try await db.readyMutations(at: .distantFuture).isEmpty)
    }

    @Test func conflictAdoptsServerState() async throws {
        let db = try AppDatabase.inMemory()
        try await queue(db, "a")
        let api = FakeAPI(submit: [.success(.conflict(current: stored("a", note: "Server says otherwise")))])
        let report = await SyncEngine(database: db, api: api, now: { t0 }).sync()

        #expect(report.conflicts == 1)
        var remarks = db.observeRemarks(for: cow).makeAsyncIterator()
        #expect(try await remarks.next()?.first?.remark.note == "Server says otherwise")
    }

    @Test func rejectionDeadLettersAndContinues() async throws {
        let db = try AppDatabase.inMemory()
        try await queue(db, "a", "b")
        let api = FakeAPI(submit: [.failure(.rejected([APIProblem(code: "UNKNOWN_ANIMAL", field: "/animal", detail: "not registered")]))])
        let report = await SyncEngine(database: db, api: api, now: { t0 }).sync()

        #expect(report.rejected == 1)
        #expect(report.uploaded == 1)
        var failed = db.observeFailedRemarks().makeAsyncIterator()
        let failures = try #require(try await failed.next())
        #expect(failures.map(\.remark.sourceID) == ["a"])
        #expect(failures.first?.lastError == "not registered")
    }

    @Test func transientFailureStopsThePassToKeepOrder() async throws {
        let db = try AppDatabase.inMemory()
        try await queue(db, "a", "b")
        let api = FakeAPI(submit: [.failure(.transient("offline"))])
        let report = await SyncEngine(database: db, api: api, now: { t0 }).sync()

        #expect(report.outcome == .deferred(reason: #"transient("offline")"#))
        #expect(api.submitted == ["a"], "b must not overtake a")
        #expect(api.animalQueries.isEmpty, "no pull while the server is unreachable")
        let retry = try await db.readyMutations(at: t0.addingTimeInterval(5))
        #expect(retry.first?.attempts == 1)
    }

    @Test func unauthorizedPausesWithoutDeadLettering() async throws {
        let db = try AppDatabase.inMemory()
        try await queue(db, "a")
        let report = await SyncEngine(database: db, api: FakeAPI(submit: [.failure(.unauthorized)]), now: { t0 }).sync()

        #expect(report.outcome == .unauthorized)
        #expect(try await db.readyMutations(at: t0).map(\.attempts) == [0], "a sign-in problem is not the upload's fault")
    }

    @Test func pullWatermarkComesFromServerDataNotTheDeviceClock() async throws {
        let db = try AppDatabase.inMemory()
        let deviceClock = Date(timeIntervalSince1970: 1_900_000_000) // years ahead of the server
        let serverTime = t0
        let api = FakeAPI(animals: [.success(Page(items: [animal(1, modified: serverTime)], nextPageToken: nil))])
        let engine = SyncEngine(database: db, api: api, now: { deviceClock })

        _ = await engine.sync()
        _ = await engine.sync()

        #expect(api.animalQueries.count == 2)
        #expect(api.animalQueries[0] == nil, "first pull fetches everything")
        #expect(api.animalQueries[1] == serverTime.addingTimeInterval(-SyncEngine.overlap))
    }

    @Test func watermarkAdvancesOnlyAfterEveryPage() async throws {
        let db = try AppDatabase.inMemory()
        let api = FakeAPI(animals: [
            .success(Page(items: [animal(1, modified: t0)], nextPageToken: "p2")),
            .failure(.transient("dropped")),
        ])
        let report = await SyncEngine(database: db, api: api, now: { t0 }).sync()

        #expect(report.outcome == .deferred(reason: #"transient("dropped")"#))
        #expect(try await db.watermark(for: .animals) == nil)
    }

    @Test func concurrentPassesSubmitOnce() async throws {
        let db = try AppDatabase.inMemory()
        try await queue(db, "a")
        let api = FakeAPI(submitDelay: .milliseconds(50))
        let engine = SyncEngine(database: db, api: api, now: { t0 })

        async let first = engine.sync()
        async let second = engine.sync()
        _ = await (first, second)

        #expect(api.submitted == ["a"])
    }
}
