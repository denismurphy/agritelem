import Domain
import Foundation
import Testing
@testable import Persistence

private let t0 = Date(timeIntervalSince1970: 1_790_000_000)
private let cow = AnimalID(scheme: "std.iso.11785", id: "372000100000003")

private func payload(_ sourceID: String, note: String = "Lame") -> RemarkSubmissionPayload {
    RemarkSubmissionPayload(sourceID: sourceID, source: "ie.app", creator: nil, animal: cow,
                            eventDate: t0, note: note, category: .veterinary, modified: t0)
}

private func remarks(_ db: AppDatabase) async throws -> [LocalRemark] {
    try await db.writer.read { db in try RemarkRecord.fetchAll(db).map(\.local) }
}

@Suite("AppDatabase")
struct AppDatabaseTests {
    @Test func enqueueRecordsPendingRemarkAndUpload() async throws {
        let db = try AppDatabase.inMemory()
        try await db.enqueueRemark(payload("a"), at: t0)

        let ready = try await db.readyMutations(at: t0)
        #expect(ready.map(\.submission.sourceID) == ["a"])
        #expect(try await remarks(db).map(\.state) == [.pending])
    }

    @Test func enqueueIsAtomic() async throws {
        let db = try AppDatabase.inMemory()
        try await db.enqueueRemark(payload("a"), at: t0)
        await #expect(throws: (any Error).self) {
            try await db.enqueueRemark(payload("a", note: "duplicate"), at: t0)
        }
        #expect(try await db.readyMutations(at: t0).count == 1, "a failed enqueue must not leave an orphan upload")
        #expect(try await remarks(db).map(\.remark.note) == ["Lame"])
    }

    @Test func uploadsLeaveInCreationOrderAndABackedOffOneBlocksLaterOnes() async throws {
        let db = try AppDatabase.inMemory()
        for id in ["a", "b", "c"] {
            try await db.enqueueRemark(payload(id), at: t0)
        }
        #expect(try await db.readyMutations(at: t0).map(\.submission.sourceID) == ["a", "b", "c"])

        let first = try #require(try await db.readyMutations(at: t0).first)
        try await db.recordFailedAttempt(first.id, reason: "timeout", at: t0, maxAttempts: 8)
        #expect(try await db.readyMutations(at: t0).isEmpty, "b and c must wait behind a")
        let afterBackoff = t0.addingTimeInterval(AppDatabase.backoff(afterAttempts: 1))
        #expect(try await db.readyMutations(at: afterBackoff).map(\.submission.sourceID) == ["a", "b", "c"])
    }

    @Test func backoffDoublesAndCaps() {
        #expect(AppDatabase.backoff(afterAttempts: 1) == 5)
        #expect(AppDatabase.backoff(afterAttempts: 2) == 10)
        #expect(AppDatabase.backoff(afterAttempts: 5) == 80)
        #expect(AppDatabase.backoff(afterAttempts: 30) == 3600)
    }

    @Test func repeatedFailuresDeadLetter() async throws {
        let db = try AppDatabase.inMemory()
        try await db.enqueueRemark(payload("a"), at: t0)
        let id = try #require(try await db.readyMutations(at: t0).first?.id)
        for attempt in 1...3 {
            try await db.recordFailedAttempt(id, reason: "HTTP 503", at: t0, maxAttempts: 3)
            let failed = try await remarks(db).first?.state == .failed
            #expect(failed == (attempt == 3))
        }
        #expect(try await db.readyMutations(at: .distantFuture).isEmpty)
    }

    @Test func completionAdoptsServerCopy() async throws {
        let db = try AppDatabase.inMemory()
        try await db.enqueueRemark(payload("a"), at: t0)
        let id = try #require(try await db.readyMutations(at: t0).first?.id)
        let server = Remark(sourceID: "a", serverID: "srv-1", animal: cow, eventDate: t0, note: "Lame (edited on server)",
                            category: .veterinary, source: "ie.app", modified: t0.addingTimeInterval(9))
        try await db.completeMutation(id, serverRemark: server)

        let stored = try #require(try await remarks(db).first)
        #expect(stored.state == .synced)
        #expect(stored.remark.serverID == "srv-1")
        #expect(stored.remark.note == "Lame (edited on server)")
        #expect(try await db.readyMutations(at: .distantFuture).isEmpty)
    }

    @Test func deadLetterRetryAndDiscard() async throws {
        let db = try AppDatabase.inMemory()
        try await db.enqueueRemark(payload("a"), at: t0)
        let id = try #require(try await db.readyMutations(at: t0).first?.id)
        try await db.deadLetter(id, reason: "SOURCE_MISMATCH")
        #expect(try await remarks(db).first?.lastError == "SOURCE_MISMATCH")
        #expect(try await db.readyMutations(at: t0).isEmpty)

        try await db.retryFailedRemark(sourceID: "a", at: t0)
        #expect(try await db.readyMutations(at: t0).map(\.attempts) == [0])
        #expect(try await remarks(db).first?.state == .pending)

        try await db.deadLetter(id, reason: "again")
        try await db.discardFailedRemark(sourceID: "a")
        #expect(try await remarks(db).isEmpty)
        #expect(try await db.readyMutations(at: .distantFuture).isEmpty, "discarding cascades to the upload")
    }

    @Test func discardNeverDeletesSyncedRemarks() async throws {
        let db = try AppDatabase.inMemory()
        let synced = Remark(sourceID: "s", serverID: "1", animal: cow, eventDate: t0, note: "n", category: .general, source: "x", modified: t0)
        try await db.applyRemarks([synced])
        try await db.discardFailedRemark(sourceID: "s")
        #expect(try await remarks(db).count == 1)
    }

    @Test func pulledRemarksDoNotOverwriteQueuedOnes() async throws {
        let db = try AppDatabase.inMemory()
        try await db.enqueueRemark(payload("a"), at: t0)
        let serverView = Remark(sourceID: "a", serverID: "srv", animal: cow, eventDate: t0, note: "server", category: .general, source: "ie.app", modified: t0)
        try await db.applyRemarks([serverView])
        #expect(try await remarks(db).first?.state == .pending)
        #expect(try await remarks(db).first?.remark.note == "Lame")
    }

    @Test func tombstonesDeleteAnimals() async throws {
        let db = try AppDatabase.inMemory()
        var animal = Animal(id: cow, name: "Daisy", managementTag: "103", breedCode: "HOL", birthDate: nil, modified: t0)
        try await db.applyAnimals([animal])
        animal.isDeleted = true
        try await db.applyAnimals([animal])
        let count = try await db.writer.read { db in try AnimalRecord.fetchCount(db) }
        #expect(count == 0)
    }

    @Test func watermarksAndCacheRoundTrip() async throws {
        let db = try AppDatabase.inMemory()
        #expect(try await db.watermark(for: .animals) == nil)
        try await db.setWatermark(t0, for: .animals)
        try await db.setWatermark(t0.addingTimeInterval(60), for: .animals)
        #expect(try await db.watermark(for: .animals) == t0.addingTimeInterval(60))

        try await db.cache(["x": 1], key: "k", at: t0)
        let cached = try #require(try await db.cached([String: Int].self, key: "k"))
        #expect(cached.value == ["x": 1])
        #expect(cached.fetchedAt == t0)
    }

    @Test func resetServerStateKeepsOnlyUnsentWork() async throws {
        let db = try AppDatabase.inMemory()
        try await db.applyAnimals([Animal(id: cow, name: "Daisy", managementTag: "103", breedCode: nil, birthDate: nil, modified: t0)])
        try await db.applyRemarks([Remark(sourceID: "synced", serverID: "1", animal: cow, eventDate: t0, note: "n", category: .general, source: "x", modified: t0)])
        try await db.enqueueRemark(payload("unsent"), at: t0)
        try await db.setWatermark(t0, for: .animals)
        try await db.cache(["k": 1], key: "herd", at: t0)

        try await db.resetServerState()

        #expect(try await db.watermark(for: .animals) == nil, "the next pull must start from scratch")
        #expect(try await db.cached([String: Int].self, key: "herd") == nil)
        #expect(try await remarks(db).map(\.remark.sourceID) == ["unsent"])
        #expect(try await db.readyMutations(at: t0).count == 1)
        let animals = try await db.writer.read { db in try AnimalRecord.fetchCount(db) }
        #expect(animals == 0)
    }

    @Test func pendingCountObservationUpdates() async throws {
        let db = try AppDatabase.inMemory()
        var counts = db.observePendingUploadCount().makeAsyncIterator()
        #expect(try await counts.next() == 0)
        try await db.enqueueRemark(payload("a"), at: t0)
        #expect(try await counts.next() == 1)
    }
}
