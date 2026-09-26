public import APIClient
public import Domain
public import Foundation
public import Persistence

/// How a synchronisation pass ended.
public enum SyncOutcome: Equatable, Sendable {
    /// Every due upload was attempted and the pull completed.
    case completed
    /// The server could not be reached or failed; queued uploads remain.
    case deferred(reason: String)
    /// The token was rejected; sync is paused until the user signs in.
    case unauthorized
}

/// What a pass did, for status display and tests.
public struct SyncReport: Equatable, Sendable {
    public var outcome: SyncOutcome
    public var uploaded = 0
    public var conflicts = 0
    public var rejected = 0
    public var animalsPulled = 0
    public var remarksPulled = 0

    public init(outcome: SyncOutcome) {
        self.outcome = outcome
    }
}

/// Moves data between the on-device store and the server.
///
/// Push sends queued remarks oldest first and stops at the first transient
/// failure, so uploads never reach the server out of order. Pull then fetches
/// animals and remarks changed since the last pass. Watermarks come from the
/// server's `meta.modified` values, never the device clock, and each pull
/// re-reads a short overlap window because a transaction can commit after a
/// later-stamped one; re-applying a record is idempotent.
public actor SyncEngine {
    /// Uploads are dead-lettered after this many transient failures.
    public static let maxAttempts = 8
    /// How far back each pull re-reads from the watermark.
    public static let overlap: TimeInterval = 60

    private let database: AppDatabase
    private let api: any AgritelemAPI
    private let now: @Sendable () -> Date
    private var inFlight: Task<SyncReport, Never>?

    public init(database: AppDatabase, api: any AgritelemAPI, now: @escaping @Sendable () -> Date = { Date() }) {
        self.database = database
        self.api = api
        self.now = now
    }

    /// Runs one pass, or joins the pass already running.
    public func sync() async -> SyncReport {
        if let inFlight {
            return await inFlight.value
        }
        let task = Task { await self.run() }
        inFlight = task
        let report = await task.value
        inFlight = nil
        return report
    }

    private func run() async -> SyncReport {
        var report = SyncReport(outcome: .completed)
        do {
            if let stop = try await push(into: &report) {
                report.outcome = stop
                return report
            }
            report.animalsPulled = try await pullAnimals()
            report.remarksPulled = try await pullRemarks()
        } catch let error as APIError {
            report.outcome = error == .unauthorized ? .unauthorized : .deferred(reason: String(describing: error))
        } catch {
            report.outcome = .deferred(reason: error.localizedDescription)
        }
        return report
    }

    /// Uploads due mutations in order. Returns an outcome if the pass must stop.
    private func push(into report: inout SyncReport) async throws -> SyncOutcome? {
        for mutation in try await database.readyMutations(at: now()) {
            switch await submit(mutation.submission) {
            case let .success(.stored(remark)):
                try await database.completeMutation(mutation.id, serverRemark: remark)
                report.uploaded += 1
            case let .success(.conflict(current)):
                // The server's copy wins for events.
                try await database.completeMutation(mutation.id, serverRemark: current)
                report.conflicts += 1
            case .failure(.unauthorized):
                return .unauthorized
            case let .failure(.rejected(problems)):
                try await database.deadLetter(mutation.id, reason: problems.map(\.detail).joined(separator: "\n"))
                report.rejected += 1
            case .failure(.forbidden):
                try await database.deadLetter(mutation.id, reason: "This account cannot write to this farm.")
                report.rejected += 1
            case .failure(.notFound):
                try await database.deadLetter(mutation.id, reason: "The farm or animal no longer exists.")
                report.rejected += 1
            case let .failure(error):
                // Transient or unexpected: back off, and keep later uploads
                // behind this one.
                try await database.recordFailedAttempt(mutation.id, reason: String(describing: error), at: now(), maxAttempts: Self.maxAttempts)
                return .deferred(reason: String(describing: error))
            }
        }
        return nil
    }

    private func submit(_ payload: RemarkSubmissionPayload) async -> Result<RemarkPostOutcome, APIError> {
        do {
            return .success(try await api.submitRemark(RemarkSubmission(payload)))
        } catch {
            return .failure(error)
        }
    }

    private func pullAnimals() async throws -> Int {
        let since = try await database.watermark(for: .animals).map { $0.addingTimeInterval(-Self.overlap) }
        var token: String?
        var count = 0
        var newest: Date?
        repeat {
            let page = try await api.animals(modifiedFrom: since, pageToken: token)
            try await database.applyAnimals(page.items)
            count += page.items.count
            newest = ([newest].compactMap { $0 } + page.items.map(\.modified)).max()
            token = page.nextPageToken
        } while token != nil
        // Advance only after every page is applied.
        if let newest {
            try await database.setWatermark(newest, for: .animals)
        }
        return count
    }

    private func pullRemarks() async throws -> Int {
        let since = try await database.watermark(for: .remarks).map { $0.addingTimeInterval(-Self.overlap) }
        var token: String?
        var count = 0
        var newest: Date?
        repeat {
            let page = try await api.remarks(modifiedFrom: since, pageToken: token)
            try await database.applyRemarks(page.items)
            count += page.items.count
            newest = ([newest].compactMap { $0 } + page.items.map(\.modified)).max()
            token = page.nextPageToken
        } while token != nil
        if let newest {
            try await database.setWatermark(newest, for: .remarks)
        }
        return count
    }
}

extension RemarkSubmission {
    init(_ p: RemarkSubmissionPayload) {
        self.init(sourceID: p.sourceID, source: p.source, creator: p.creator, animal: p.animal,
                  eventDate: p.eventDate, note: p.note, category: p.category, modified: p.modified)
    }
}
