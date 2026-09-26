package import OpenAPIRuntime
import OpenAPIURLSession
public import Domain
public import Foundation
import HTTPTypes

/// Errors from API calls, classified by what the caller should do next.
public enum APIError: Error, Equatable, Sendable {
    /// The token is missing, invalid or expired: ask for a new one.
    case unauthorized
    /// The token is valid but lacks access to this location or operation.
    case forbidden
    /// The resource does not exist at this location.
    case notFound
    /// The server understood the request and rejected its content.
    case rejected([APIProblem])
    /// The request may succeed if retried later (network, timeouts, 5xx).
    case transient(String)
    /// The response did not match the contract.
    case unexpectedResponse(String)
}

/// A page of results and the opaque token for the next page, if any.
public struct Page<Element: Sendable>: Sendable {
    public var items: [Element]
    public var nextPageToken: String?

    public init(items: [Element], nextPageToken: String?) {
        self.items = items
        self.nextPageToken = nextPageToken
    }
}

/// A remark to submit. `sourceID` makes retries idempotent.
public struct RemarkSubmission: Sendable, Hashable, Codable {
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

/// The outcome of submitting a remark.
public enum RemarkPostOutcome: Sendable, Equatable {
    /// Stored now or earlier with identical content (an idempotent retry).
    case stored(Remark)
    /// The server holds different content for this source ID; server wins.
    case conflict(current: Remark)
}

/// The Agritelem API for one location. Implementations are safe to share.
public protocol AgritelemAPI: Sendable {
    func herdOverview() async throws(APIError) -> HerdOverview
    func animals(modifiedFrom: Date?, pageToken: String?) async throws(APIError) -> Page<Animal>
    func devices() async throws(APIError) -> [Device]
    func dailyStatistics(for animal: AnimalID, from: Date, to: Date) async throws(APIError) -> [DailyStatistic]
    func remarks(modifiedFrom: Date?, pageToken: String?) async throws(APIError) -> Page<Remark>
    func submitRemark(_ remark: RemarkSubmission) async throws(APIError) -> RemarkPostOutcome
}

/// Supplies the current bearer token, or nil when signed out.
public typealias TokenProvider = @Sendable () async -> String?

/// Talks to a live Agritelem server with the client generated from
/// contracts/openapi.json.
public struct LiveAgritelemAPI: AgritelemAPI {
    private let client: Client
    private let location: LocationID

    public init(serverURL: URL, location: LocationID, token: @escaping TokenProvider) {
        self.init(serverURL: serverURL, location: location, token: token, transport: URLSessionTransport())
    }

    package init(serverURL: URL, location: LocationID, token: @escaping TokenProvider, transport: any ClientTransport) {
        self.client = Client(serverURL: serverURL, transport: transport, middlewares: [BearerTokenMiddleware(token: token)])
        self.location = location
    }

    private var path: (locationScheme: String, locationId: String) { (location.scheme, location.id) }

    public func herdOverview() async throws(APIError) -> HerdOverview {
        let output = try await call {
            try await client.getHerdOverview(path: .init(locationScheme: path.locationScheme, locationId: path.locationId))
        }
        switch output {
        case let .ok(ok):
            return try Map.herdOverview(try decode { try ok.body.json })
        case let .`default`(status, response):
            throw Map.error(status: status, body: try? response.body.json)
        }
    }

    public func animals(modifiedFrom: Date?, pageToken: String?) async throws(APIError) -> Page<Animal> {
        let output = try await call {
            try await client.getAnimals(
                path: .init(locationScheme: path.locationScheme, locationId: path.locationId),
                query: .init(metaModifiedFrom: modifiedFrom, pageSize: 200, pageToken: pageToken)
            )
        }
        switch output {
        case let .ok(ok):
            let collection = try decode { try ok.body.json }
            let items = try (collection.value2.member ?? []).map { resource throws(APIError) in try Map.animal(resource) }
            return Page(items: items, nextPageToken: Map.pageToken(from: collection.value1.view?.next))
        case let .`default`(status, response):
            throw Map.error(status: status, body: try? response.body.json)
        }
    }

    public func devices() async throws(APIError) -> [Device] {
        let output = try await call {
            try await client.getDevices(path: .init(locationScheme: path.locationScheme, locationId: path.locationId))
        }
        switch output {
        case let .ok(ok):
            return (try decode { try ok.body.json }).value2.member?.map(Map.device) ?? []
        case let .`default`(status, response):
            throw Map.error(status: status, body: try? response.body.json)
        }
    }

    public func dailyStatistics(for animal: AnimalID, from: Date, to: Date) async throws(APIError) -> [DailyStatistic] {
        let output = try await call {
            try await client.getAnimalObservationSummaryMetrics(
                path: .init(locationScheme: path.locationScheme, locationId: path.locationId),
                query: .init(
                    dateTo: Map.utcDay(to), dateFrom: Map.utcDay(from),
                    animalScheme: animal.scheme, animalId: animal.id, duration: "P1D"
                )
            )
        }
        switch output {
        case let .ok(ok):
            let resources = (try decode { try ok.body.json }).value2.member ?? []
            var statistics: [DailyStatistic] = []
            for resource in resources {
                statistics += try Map.dailyStatistics(resource)
            }
            return statistics
        case let .`default`(status, response):
            throw Map.error(status: status, body: try? response.body.json)
        }
    }

    public func remarks(modifiedFrom: Date?, pageToken: String?) async throws(APIError) -> Page<Remark> {
        let output = try await call {
            try await client.getRemarkEvents(
                path: .init(locationScheme: path.locationScheme, locationId: path.locationId),
                query: .init(metaModifiedFrom: modifiedFrom, pageSize: 200, pageToken: pageToken)
            )
        }
        switch output {
        case let .ok(ok):
            let collection = try decode { try ok.body.json }
            let items = try (collection.value2.member ?? []).map { resource throws(APIError) in try Map.remark(resource) }
            return Page(items: items, nextPageToken: Map.pageToken(from: collection.value1.view?.next))
        case let .`default`(status, response):
            throw Map.error(status: status, body: try? response.body.json)
        }
    }

    public func submitRemark(_ remark: RemarkSubmission) async throws(APIError) -> RemarkPostOutcome {
        let body = Map.remarkResource(remark, location: location)
        let output = try await call {
            try await client.postSingleRemarkEvent(
                path: .init(locationScheme: path.locationScheme, locationId: path.locationId),
                body: .json(body)
            )
        }
        switch output {
        case let .ok(ok):
            return .stored(try Map.remark(try decode { try ok.body.json }))
        case let .conflict(conflict):
            return .conflict(current: try Map.remark(try decode { try conflict.body.json }.current))
        case .created, .accepted:
            // The server always answers 200 with the stored remark; a bodiless
            // 201/202 would leave the client unable to reconcile.
            throw .unexpectedResponse("remark accepted without the stored resource")
        case let .`default`(status, response):
            throw Map.error(status: status, body: try? response.body.json)
        }
    }

    /// Runs a generated-client call, classifying transport failures.
    private func call<Output>(_ operation: () async throws -> Output) async throws(APIError) -> Output {
        do {
            return try await operation()
        } catch let error as ClientError {
            // A response arrived but could not be decoded, e.g. an error
            // status without the JSON body the contract promises (as proxies
            // send): classify by status so a 401 still means "sign in again".
            if let status = error.response?.status.code, !(200..<300).contains(status) {
                throw Map.error(status: status, body: nil)
            }
            if error.response != nil || error.underlyingError is DecodingError {
                throw .unexpectedResponse(String(describing: error.underlyingError))
            }
            throw .transient(error.underlyingError.localizedDescription)
        } catch {
            throw .transient(error.localizedDescription)
        }
    }

    private func decode<T>(_ body: () throws -> T) throws(APIError) -> T {
        do {
            return try body()
        } catch {
            throw .unexpectedResponse(String(describing: error))
        }
    }
}

/// Adds `Authorization: Bearer <token>` to every request.
struct BearerTokenMiddleware: ClientMiddleware {
    let token: TokenProvider

    func intercept(
        _ request: HTTPRequest,
        body: HTTPBody?,
        baseURL: URL,
        operationID: String,
        next: @Sendable (HTTPRequest, HTTPBody?, URL) async throws -> (HTTPResponse, HTTPBody?)
    ) async throws -> (HTTPResponse, HTTPBody?) {
        var request = request
        if let token = await token() {
            request.headerFields[.authorization] = "Bearer \(token)"
        }
        return try await next(request, body, baseURL)
    }
}
