public import Domain
public import Foundation
public import GRDB

/// The on-device SQLite store: a cache of server data plus the queue of local
/// changes waiting to be uploaded. All methods are safe to call from any task.
public final class AppDatabase: Sendable {
    /// The underlying writer, exposed so observations can be started on it.
    public let writer: any DatabaseWriter

    /// Opens (creating if needed) a database at `url` and migrates it.
    public static func open(at url: URL) throws -> AppDatabase {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        var configuration = Configuration()
        configuration.foreignKeysEnabled = true
        return try AppDatabase(DatabasePool(path: url.path, configuration: configuration))
    }

    /// An empty in-memory database, for tests and previews.
    public static func inMemory() throws -> AppDatabase {
        var configuration = Configuration()
        configuration.foreignKeysEnabled = true
        return try AppDatabase(DatabaseQueue(configuration: configuration))
    }

    init(_ writer: any DatabaseWriter) throws {
        self.writer = writer
        try Self.migrator.migrate(writer)
    }

    static var migrator: DatabaseMigrator {
        var migrator = DatabaseMigrator()
        #if DEBUG
        migrator.eraseDatabaseOnSchemaChange = true
        #endif
        migrator.registerMigration("v1") { db in
            try db.create(table: "animal") { t in
                t.primaryKey("key", .text)
                t.column("scheme", .text).notNull()
                t.column("idValue", .text).notNull()
                t.column("name", .text)
                t.column("managementTag", .text)
                t.column("breedCode", .text)
                t.column("birthDate", .datetime)
                t.column("modified", .datetime).notNull()
            }
            try db.create(table: "remark") { t in
                t.primaryKey("sourceID", .text)
                t.column("serverID", .text)
                t.column("animalKey", .text).notNull().indexed()
                t.column("animalScheme", .text).notNull()
                t.column("animalID", .text).notNull()
                t.column("eventDate", .datetime).notNull()
                t.column("note", .text).notNull()
                t.column("category", .text).notNull()
                t.column("source", .text).notNull()
                t.column("modified", .datetime).notNull()
                t.column("state", .text).notNull()
                t.column("lastError", .text)
            }
            // FIFO queue of local changes. `id` preserves creation order.
            try db.create(table: "mutation") { t in
                t.autoIncrementedPrimaryKey("id")
                t.column("remarkSourceID", .text).notNull().unique()
                    .references("remark", column: "sourceID", onDelete: .cascade)
                t.column("payload", .blob).notNull()
                t.column("createdAt", .datetime).notNull()
                t.column("attempts", .integer).notNull().defaults(to: 0)
                t.column("nextAttemptAt", .datetime).notNull()
                t.column("state", .text).notNull()
                t.column("lastError", .text)
            }
            try db.create(table: "syncCursor") { t in
                t.primaryKey("name", .text)
                t.column("watermark", .datetime).notNull()
            }
            try db.create(table: "cachedDocument") { t in
                t.primaryKey("key", .text)
                t.column("payload", .blob).notNull()
                t.column("fetchedAt", .datetime).notNull()
            }
        }
        return migrator
    }
}

// MARK: - Records

struct AnimalRecord: Codable, FetchableRecord, PersistableRecord {
    static let databaseTableName = "animal"
    var key: String
    var scheme: String
    var idValue: String
    var name: String?
    var managementTag: String?
    var breedCode: String?
    var birthDate: Date?
    var modified: Date

    init(_ a: Animal) {
        key = a.id.key
        scheme = a.id.scheme
        idValue = a.id.id
        name = a.name
        managementTag = a.managementTag
        breedCode = a.breedCode
        birthDate = a.birthDate
        modified = a.modified
    }

    var animal: Animal {
        Animal(id: AnimalID(scheme: scheme, id: idValue), name: name, managementTag: managementTag, breedCode: breedCode, birthDate: birthDate, modified: modified)
    }
}

/// Where a locally known remark stands relative to the server.
public enum RemarkSyncState: String, Codable, Sendable, Hashable {
    /// Created on this device; waiting to upload.
    case pending
    /// Stored on the server.
    case synced
    /// The server rejected it; the user must retry or discard it.
    case failed
}

struct RemarkRecord: Codable, FetchableRecord, PersistableRecord {
    static let databaseTableName = "remark"
    var sourceID: String
    var serverID: String?
    var animalKey: String
    var animalScheme: String
    var animalID: String
    var eventDate: Date
    var note: String
    var category: String
    var source: String
    var modified: Date
    var state: RemarkSyncState
    var lastError: String?

    init(_ r: Remark, state: RemarkSyncState) {
        sourceID = r.sourceID
        serverID = r.serverID
        animalKey = r.animal.key
        animalScheme = r.animal.scheme
        animalID = r.animal.id
        eventDate = r.eventDate
        note = r.note
        category = r.category.rawValue
        source = r.source
        modified = r.modified
        self.state = state
    }

    var local: LocalRemark {
        LocalRemark(
            remark: Remark(
                sourceID: sourceID, serverID: serverID, animal: AnimalID(scheme: animalScheme, id: animalID),
                eventDate: eventDate, note: note, category: RemarkCategory(rawValue: category) ?? .general,
                source: source, modified: modified
            ),
            state: state,
            lastError: lastError
        )
    }
}

enum MutationState: String, Codable {
    case pending
    case deadLetter
}

struct MutationRecord: Codable, FetchableRecord, MutablePersistableRecord {
    static let databaseTableName = "mutation"
    var id: Int64?
    var remarkSourceID: String
    var payload: Data
    var createdAt: Date
    var attempts: Int
    var nextAttemptAt: Date
    var state: MutationState
    var lastError: String?

    mutating func didInsert(_ inserted: InsertionSuccess) {
        id = inserted.rowID
    }
}

// MARK: - Public value types

/// A remark as known on this device, with its upload state.
public struct LocalRemark: Identifiable, Hashable, Sendable {
    public var remark: Remark
    public var state: RemarkSyncState
    public var lastError: String?
    public var id: String { remark.sourceID }

    public init(remark: Remark, state: RemarkSyncState, lastError: String? = nil) {
        self.remark = remark
        self.state = state
        self.lastError = lastError
    }
}

/// A queued upload.
public struct PendingMutation: Identifiable, Hashable, Sendable {
    public var id: Int64
    public var submission: RemarkSubmissionPayload
    public var attempts: Int
}

/// The stored form of a remark upload. It mirrors the API submission but is
/// owned by Persistence so the queue does not depend on the network layer.
public struct RemarkSubmissionPayload: Codable, Hashable, Sendable {
    public var sourceID: String
    public var source: String
    public var creator: String?
    public var animal: AnimalID
    public var eventDate: Date
    public var note: String
    public var category: RemarkCategory
    public var modified: Date

    public init(sourceID: String, source: String, creator: String?, animal: AnimalID, eventDate: Date, note: String, category: RemarkCategory, modified: Date) {
        self.sourceID = sourceID
        self.source = source
        self.creator = creator
        self.animal = animal
        self.eventDate = eventDate
        self.note = note
        self.category = category
        self.modified = modified
    }
}

/// Names of synchronised collections, for watermarks.
public enum SyncCollection: String, Sendable {
    case animals
    case remarks
}

// MARK: - Operations

extension AppDatabase {
    private static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }()

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()

    /// Records a new remark and queues its upload in one transaction, so a
    /// remark never exists without its upload or vice versa.
    public func enqueueRemark(_ s: RemarkSubmissionPayload, at now: Date) async throws {
        let payload = try Self.encoder.encode(s)
        let remark = Remark(sourceID: s.sourceID, serverID: nil, animal: s.animal, eventDate: s.eventDate,
                            note: s.note, category: s.category, source: s.source, modified: s.modified)
        try await writer.write { db in
            try RemarkRecord(remark, state: .pending).insert(db)
            var mutation = MutationRecord(remarkSourceID: s.sourceID, payload: payload, createdAt: now,
                                          attempts: 0, nextAttemptAt: now, state: .pending)
            try mutation.insert(db)
        }
    }

    /// Queued uploads due at `now`, oldest first.
    public func readyMutations(at now: Date, limit: Int = 50) async throws -> [PendingMutation] {
        let records = try await writer.read { db in
            try MutationRecord
                .filter(Column("state") == MutationState.pending.rawValue)
                .order(Column("id"))
                .limit(limit)
                .fetchAll(db)
        }
        // Stop at the first mutation not yet due, so later ones never
        // overtake it: uploads leave in creation order.
        var ready: [PendingMutation] = []
        for record in records {
            guard record.nextAttemptAt <= now, let id = record.id else { break }
            ready.append(PendingMutation(id: id, submission: try Self.decoder.decode(RemarkSubmissionPayload.self, from: record.payload), attempts: record.attempts))
        }
        return ready
    }

    /// The server stored the remark (or a remark it considers the same):
    /// adopt the server's copy and drop the queued upload.
    public func completeMutation(_ id: Int64, serverRemark: Remark) async throws {
        try await writer.write { db in
            guard let mutation = try MutationRecord.fetchOne(db, key: id) else { return }
            var record = RemarkRecord(serverRemark, state: .synced)
            record.sourceID = mutation.remarkSourceID
            try record.upsert(db)
            _ = try MutationRecord.deleteOne(db, key: id)
        }
    }

    /// The server refused the upload for a reason retrying cannot fix.
    public func deadLetter(_ id: Int64, reason: String) async throws {
        try await writer.write { db in
            guard var mutation = try MutationRecord.fetchOne(db, key: id) else { return }
            mutation.state = .deadLetter
            mutation.lastError = reason
            try mutation.update(db)
            try db.execute(sql: "UPDATE remark SET state = ?, lastError = ? WHERE sourceID = ?",
                           arguments: [RemarkSyncState.failed.rawValue, reason, mutation.remarkSourceID])
        }
    }

    /// The upload failed transiently. It is retried after exponential backoff,
    /// and dead-lettered once `maxAttempts` is reached.
    public func recordFailedAttempt(_ id: Int64, reason: String, at now: Date, maxAttempts: Int) async throws {
        try await writer.write { db in
            guard var mutation = try MutationRecord.fetchOne(db, key: id) else { return }
            mutation.attempts += 1
            mutation.lastError = reason
            if mutation.attempts >= maxAttempts {
                mutation.state = .deadLetter
                try db.execute(sql: "UPDATE remark SET state = ?, lastError = ? WHERE sourceID = ?",
                               arguments: [RemarkSyncState.failed.rawValue, reason, mutation.remarkSourceID])
            } else {
                mutation.nextAttemptAt = now.addingTimeInterval(Self.backoff(afterAttempts: mutation.attempts))
            }
            try mutation.update(db)
        }
    }

    /// Delay before the next attempt: 5 s doubling per attempt, capped at 1 h.
    static func backoff(afterAttempts attempts: Int) -> TimeInterval {
        min(5 * pow(2, Double(max(attempts - 1, 0))), 3600)
    }

    /// Re-queues a dead-lettered remark for immediate upload.
    public func retryFailedRemark(sourceID: String, at now: Date) async throws {
        try await writer.write { db in
            try db.execute(sql: "UPDATE mutation SET state = ?, attempts = 0, nextAttemptAt = ?, lastError = NULL WHERE remarkSourceID = ?",
                           arguments: [MutationState.pending.rawValue, now, sourceID])
            try db.execute(sql: "UPDATE remark SET state = ?, lastError = NULL WHERE sourceID = ? AND state = ?",
                           arguments: [RemarkSyncState.pending.rawValue, sourceID, RemarkSyncState.failed.rawValue])
        }
    }

    /// Deletes a remark that never reached the server, with its upload.
    public func discardFailedRemark(sourceID: String) async throws {
        try await writer.write { db in
            _ = try RemarkRecord.filter(Column("sourceID") == sourceID && Column("state") == RemarkSyncState.failed.rawValue).deleteAll(db)
        }
    }

    /// Applies animals pulled from the server; tombstones delete.
    public func applyAnimals(_ animals: [Animal]) async throws {
        try await writer.write { db in
            for animal in animals {
                if animal.isDeleted {
                    _ = try AnimalRecord.deleteOne(db, key: animal.id.key)
                } else {
                    try AnimalRecord(animal).upsert(db)
                }
            }
        }
    }

    /// Applies remarks pulled from the server. A remark with an upload still
    /// queued keeps its local state; the upload reconciles it.
    public func applyRemarks(_ remarks: [Remark]) async throws {
        try await writer.write { db in
            for remark in remarks {
                let queued = try MutationRecord.filter(Column("remarkSourceID") == remark.sourceID).fetchCount(db) > 0
                if !queued {
                    try RemarkRecord(remark, state: .synced).upsert(db)
                }
            }
        }
    }

    /// Forgets everything learned from the current server (cached animals,
    /// synced remarks, documents and watermarks) so a different server is
    /// synchronised from scratch. Unsent local remarks are kept.
    public func resetServerState() async throws {
        try await writer.write { db in
            try db.execute(sql: "DELETE FROM syncCursor")
            try db.execute(sql: "DELETE FROM cachedDocument")
            try db.execute(sql: "DELETE FROM animal")
            try db.execute(sql: "DELETE FROM remark WHERE sourceID NOT IN (SELECT remarkSourceID FROM mutation)")
        }
    }

    public func watermark(for collection: SyncCollection) async throws -> Date? {
        try await writer.read { db in
            try Date.fetchOne(db, sql: "SELECT watermark FROM syncCursor WHERE name = ?", arguments: [collection.rawValue])
        }
    }

    public func setWatermark(_ date: Date, for collection: SyncCollection) async throws {
        try await writer.write { db in
            try db.execute(sql: "INSERT INTO syncCursor (name, watermark) VALUES (?, ?) ON CONFLICT(name) DO UPDATE SET watermark = excluded.watermark",
                           arguments: [collection.rawValue, date])
        }
    }

    /// Caches a server document (for offline display) under `key`.
    public func cache<T: Encodable & Sendable>(_ value: T, key: String, at now: Date) async throws {
        let payload = try Self.encoder.encode(value)
        try await writer.write { db in
            try db.execute(sql: "INSERT INTO cachedDocument (key, payload, fetchedAt) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET payload = excluded.payload, fetchedAt = excluded.fetchedAt",
                           arguments: [key, payload, now])
        }
    }

    /// A cached document and when it was fetched, or nil.
    public func cached<T: Decodable & Sendable>(_ type: T.Type, key: String) async throws -> (value: T, fetchedAt: Date)? {
        let stored: (payload: Data, fetchedAt: Date)? = try await writer.read { db in
            guard let row = try Row.fetchOne(db, sql: "SELECT payload, fetchedAt FROM cachedDocument WHERE key = ?", arguments: [key]) else {
                return nil
            }
            return (row["payload"], row["fetchedAt"])
        }
        guard let stored else { return nil }
        return (try Self.decoder.decode(T.self, from: stored.payload), stored.fetchedAt)
    }
}

// MARK: - Observation

extension AppDatabase {
    /// All cached animals, updating as the database changes.
    public func observeAnimals() -> AsyncValueObservation<[Animal]> {
        ValueObservation
            .tracking { db in try AnimalRecord.order(Column("managementTag")).fetchAll(db).map(\.animal) }
            .values(in: writer)
    }

    /// One animal's remarks, newest first.
    public func observeRemarks(for animal: AnimalID) -> AsyncValueObservation<[LocalRemark]> {
        ValueObservation
            .tracking { db in
                try RemarkRecord.filter(Column("animalKey") == animal.key)
                    .order(Column("eventDate").desc)
                    .fetchAll(db).map(\.local)
            }
            .values(in: writer)
    }

    /// Remarks the server rejected, awaiting a user decision.
    public func observeFailedRemarks() -> AsyncValueObservation<[LocalRemark]> {
        ValueObservation
            .tracking { db in
                try RemarkRecord.filter(Column("state") == RemarkSyncState.failed.rawValue)
                    .order(Column("eventDate").desc)
                    .fetchAll(db).map(\.local)
            }
            .values(in: writer)
    }

    /// The number of uploads still queued (excluding dead letters).
    public func observePendingUploadCount() -> AsyncValueObservation<Int> {
        ValueObservation
            .tracking { db in try MutationRecord.filter(Column("state") == MutationState.pending.rawValue).fetchCount(db) }
            .values(in: writer)
    }
}
