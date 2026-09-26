import APIClient
import Domain
import Foundation
import Observation
import Persistence
import Sync

/// Owns the app's long-lived services and the state every screen shares:
/// who is signed in, and how synchronisation is going.
@MainActor
@Observable
final class AppModel {
    enum SyncStatus: Equatable {
        case idle
        case syncing
        case offline
        case needsSignIn
        case failed(String)
    }

    let database: AppDatabase
    let location: LocationID
    private(set) var api: any AgritelemAPI
    private(set) var serverURL: URL
    private(set) var claims: TokenClaims?
    private(set) var syncStatus: SyncStatus = .idle
    private(set) var lastSynced: Date?
    private(set) var pendingUploads = 0
    private(set) var failedRemarks: [LocalRemark] = []
    private(set) var isOnline = true
    /// Set when the on-disk store could not be opened and data is in memory only.
    private(set) var storageError: String?

    private let credentials: Credentials?
    private var engine: SyncEngine
    private var syncTask: Task<Void, Never>?

    init(database: AppDatabase, api: any AgritelemAPI, serverURL: URL, location: LocationID, credentials: Credentials?, claims: TokenClaims?) {
        self.database = database
        self.api = api
        self.serverURL = serverURL
        self.location = location
        self.credentials = credentials
        self.claims = claims
        self.engine = SyncEngine(database: database, api: api)
    }

    // MARK: Composition

    static let defaultServerURL = URL(string: "http://localhost:8080")!
    static let demoLocation = LocationID(scheme: "dk.herdnumber", id: "123456")
    private static let serverURLKey = "serverURL"

    /// The production composition: on-disk database, Keychain token, live API.
    static func live() -> AppModel {
        let credentials = Credentials()
        let environment = ProcessInfo.processInfo.environment
        // Development convenience: a token and server passed at launch
        // (e.g. `SIMCTL_CHILD_AGRITELEM_TOKEN`) are adopted once.
        if let token = environment["AGRITELEM_TOKEN"], TokenClaims(token: token) != nil {
            try? credentials.save(token)
        }
        if let url = environment["AGRITELEM_SERVER_URL"] {
            UserDefaults.standard.set(url, forKey: serverURLKey)
        }
        let serverURL = UserDefaults.standard.string(forKey: serverURLKey).flatMap(URL.init(string:)) ?? defaultServerURL

        let database: AppDatabase
        var storageError: String?
        do {
            let url = URL.applicationSupportDirectory.appending(path: "Agritelem/agritelem.sqlite")
            database = try AppDatabase.open(at: url)
        } catch {
            // An unreadable store must not brick the app in the field: run
            // from memory and tell the user their work will not persist.
            guard let memory = try? AppDatabase.inMemory() else {
                fatalError("Cannot open any database: \(error)")
            }
            database = memory
            storageError = error.localizedDescription
        }
        let api = LiveAgritelemAPI(serverURL: serverURL, location: demoLocation, token: { credentials.token() })
        let model = AppModel(database: database, api: api, serverURL: serverURL, location: demoLocation,
                             credentials: credentials, claims: credentials.token().flatMap(TokenClaims.init(token:)))
        model.storageError = storageError
        return model
    }

    // MARK: Lifecycle

    /// Observes the store and the network for as long as the app runs.
    func start() async {
        await withTaskGroup(of: Void.self) { group in
            group.addTask { await self.observePendingUploads() }
            group.addTask { await self.observeFailedRemarks() }
            group.addTask { await self.observeConnectivity() }
            syncSoon()
        }
    }

    private func observePendingUploads() async {
        do {
            for try await count in database.observePendingUploadCount() {
                pendingUploads = count
            }
        } catch {
            syncStatus = .failed(error.localizedDescription)
        }
    }

    private func observeFailedRemarks() async {
        do {
            for try await failed in database.observeFailedRemarks() {
                failedRemarks = failed
            }
        } catch {
            syncStatus = .failed(error.localizedDescription)
        }
    }

    private func observeConnectivity() async {
        for await online in Connectivity.updates() {
            let cameOnline = online && !isOnline
            isOnline = online
            if cameOnline {
                syncSoon()
            } else if !online, syncStatus != .syncing {
                syncStatus = .offline
            }
        }
    }

    // MARK: Sync

    /// Starts a sync pass unless one is running.
    func syncSoon() {
        guard syncTask == nil else { return }
        guard claims != nil else {
            syncStatus = .needsSignIn
            return
        }
        syncStatus = .syncing
        syncTask = Task {
            let report = await engine.sync()
            apply(report)
            syncTask = nil
        }
    }

    /// Runs a pass and waits for it; for pull-to-refresh.
    func syncNow() async {
        syncSoon()
        await syncTask?.value
    }

    private func apply(_ report: SyncReport) {
        switch report.outcome {
        case .completed:
            syncStatus = .idle
            lastSynced = .now
        case .unauthorized:
            syncStatus = .needsSignIn
        case let .deferred(reason):
            syncStatus = isOnline ? .failed(reason) : .offline
        }
    }

    // MARK: Account

    func signIn(token: String) throws {
        let trimmed = token.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let claims = TokenClaims(token: trimmed) else {
            throw SignInError.malformedToken
        }
        try credentials?.save(trimmed)
        self.claims = claims
        syncSoon()
    }

    func signOut() {
        credentials?.delete()
        claims = nil
        syncStatus = .needsSignIn
    }

    func setServerURL(_ url: URL) {
        guard url != serverURL, let credentials else { return }
        UserDefaults.standard.set(url.absoluteString, forKey: Self.serverURLKey)
        serverURL = url
        api = LiveAgritelemAPI(serverURL: url, location: location, token: { credentials.token() })
        engine = SyncEngine(database: database, api: api)
        // Watermarks and caches belong to the old server. Let any pass against
        // it finish first, then forget its data (keeping unsent remarks) and
        // sync the new server from scratch.
        let previousPass = syncTask
        syncTask = Task {
            await previousPass?.value
            try? await database.resetServerState()
            syncTask = nil
            syncSoon()
        }
    }

    // MARK: Remarks

    /// Saves a remark locally and schedules its upload. Works offline.
    func addRemark(animal: AnimalID, note: String, category: RemarkCategory, eventDate: Date) async throws {
        guard let claims else { throw SignInError.signedOut }
        let now = Date.now
        let payload = RemarkSubmissionPayload(
            sourceID: UUID().uuidString.lowercased(), source: claims.subject, creator: nil,
            animal: animal, eventDate: eventDate, note: note, category: category, modified: now
        )
        try await database.enqueueRemark(payload, at: now)
        syncSoon()
    }

    func retry(_ remark: LocalRemark) async {
        try? await database.retryFailedRemark(sourceID: remark.id, at: .now)
        syncSoon()
    }

    func discard(_ remark: LocalRemark) async {
        try? await database.discardFailedRemark(sourceID: remark.id)
    }
}

enum SignInError: LocalizedError {
    case malformedToken
    case signedOut

    var errorDescription: String? {
        switch self {
        case .malformedToken: String(localized: "That doesn't look like an Agritelem access token.")
        case .signedOut: String(localized: "Sign in to record remarks.")
        }
    }
}
