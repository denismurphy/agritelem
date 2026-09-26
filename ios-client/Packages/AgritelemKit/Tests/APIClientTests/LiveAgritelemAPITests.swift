import Domain
import Foundation
import HTTPTypes
import OpenAPIRuntime
import Testing
@testable import APIClient

/// Replays a canned response and records the request it received.
private final class StubTransport: ClientTransport, @unchecked Sendable {
    private let lock = NSLock()
    private let status: Int
    private let fixture: String?
    private(set) var request: HTTPRequest?
    private(set) var requestBody: Data?

    init(status: Int = 200, fixture: String?) {
        self.status = status
        self.fixture = fixture
    }

    func send(_ request: HTTPRequest, body: HTTPBody?, baseURL: URL, operationID: String) async throws -> (HTTPResponse, HTTPBody?) {
        let sent = if let body { try await Data(collecting: body, upTo: 1 << 20) } else { Data?.none }
        lock.withLock {
            self.request = request
            self.requestBody = sent
        }
        var response = HTTPResponse(status: .init(code: status))
        guard let fixture else { return (response, nil) }
        let url = try #require(Bundle.module.url(forResource: fixture, withExtension: "json", subdirectory: "Fixtures"))
        response.headerFields[.contentType] = "application/json"
        return (response, HTTPBody(try Data(contentsOf: url)))
    }
}

private struct TransportFailure: Error {}

private final class FailingTransport: ClientTransport {
    func send(_: HTTPRequest, body _: HTTPBody?, baseURL _: URL, operationID _: String) async throws -> (HTTPResponse, HTTPBody?) {
        throw TransportFailure()
    }
}

private let farm = LocationID(scheme: "dk.herdnumber", id: "123456")
private let cow = AnimalID(scheme: "std.iso.11785", id: "372000100000003")

private func api(_ transport: any ClientTransport, token: String? = "t0ken") -> LiveAgritelemAPI {
    LiveAgritelemAPI(serverURL: URL(string: "https://api.example.test")!, location: farm, token: { token }, transport: transport)
}

private func submission(sourceID: String = "fixture-remark-1") -> RemarkSubmission {
    RemarkSubmission(
        sourceID: sourceID, source: "ie.example.gateway", creator: nil, animal: cow,
        eventDate: Date(timeIntervalSince1970: 1_790_000_000), note: "Mounting behaviour observed at milking",
        category: .behaviour, modified: Date(timeIntervalSince1970: 1_790_000_000)
    )
}

@Suite("LiveAgritelemAPI against captured server responses")
struct LiveAgritelemAPITests {
    @Test func herdOverviewDecodesKPIsAndStatuses() async throws {
        let transport = StubTransport(fixture: "herd-overview")
        let overview = try await api(transport).herdOverview()

        #expect(overview.animalCount == 24)
        #expect(overview.animals.count == 24)
        #expect(overview.estrusAlerts == 1)
        #expect(overview.animals.contains { $0.statuses == [.inHeat] })
        var utc = Calendar(identifier: .gregorian)
        utc.timeZone = .gmt
        #expect(utc.dateComponents([.hour, .minute], from: overview.day) == DateComponents(hour: 0, minute: 0))
        #expect(transport.request?.headerFields[.authorization] == "Bearer t0ken")
        #expect(transport.request?.path == "/agritelem/locations/dk.herdnumber/123456/herd-overview")
    }

    @Test func omitsAuthorizationWhenSignedOut() async throws {
        let transport = StubTransport(fixture: "herd-overview")
        _ = try await api(transport, token: nil).herdOverview()
        #expect(transport.request?.headerFields[.authorization] == nil)
    }

    @Test func animalsPageCarriesNextPageToken() async throws {
        let page = try await api(StubTransport(fixture: "animals-page")).animals(modifiedFrom: nil, pageToken: nil)
        #expect(page.items.count == 2)
        #expect(page.items.first?.name == "Bessie")
        #expect(page.items.first?.breedCode == "HOL")
        #expect(page.nextPageToken?.isEmpty == false)
    }

    @Test func dailyStatisticsCarryDerivedFields() async throws {
        let transport = StubTransport(fixture: "daily-summary")
        let from = Date(timeIntervalSince1970: 1_789_000_000)
        let stats = try await api(transport).dailyStatistics(for: cow, from: from, to: from.addingTimeInterval(6 * 86_400))

        let flagged = stats.filter { $0.flags.contains(.heatSignature) }
        #expect(!flagged.isEmpty, "the simulated estrus day should be flagged")
        #expect(stats.contains { $0.metric == .rumination && $0.baselineMedian != nil })
        let query = try #require(transport.request?.path.flatMap(URLComponents.init(string:))?.queryItems)
        #expect(query.contains(URLQueryItem(name: "duration", value: "P1D")))
        #expect(query.contains(URLQueryItem(name: "animal-id", value: cow.id)))
        #expect(query.contains { $0.name == "date-from" && $0.value?.count == 10 })
    }

    @Test func devicesMapCurrentAssignment() async throws {
        let devices = try await api(StubTransport(fixture: "devices")).devices()
        #expect(devices.count == 2)
        #expect(devices.allSatisfy { $0.animal != nil && $0.assignedSince != nil })
    }

    @Test func remarksPageMapsSourceIDAndCategory() async throws {
        let page = try await api(StubTransport(fixture: "remarks-page")).remarks(modifiedFrom: nil, pageToken: nil)
        let remark = try #require(page.items.first)
        #expect(remark.sourceID.hasPrefix("fixture-remark"))
        #expect(remark.serverID != nil)
        #expect(remark.category == .behaviour)
    }

    @Test func submitRemarkSendsICARShapeAndMapsStored() async throws {
        let transport = StubTransport(fixture: "remark-stored")
        let outcome = try await api(transport).submitRemark(submission())

        guard case let .stored(remark) = outcome else {
            Issue.record("outcome = \(outcome), want stored")
            return
        }
        #expect(remark.sourceID == "fixture-remark-1")
        #expect(remark.serverID != nil)

        let body = try #require(transport.requestBody)
        let json = try #require(try JSONSerialization.jsonObject(with: body) as? [String: Any])
        #expect(json["resourceType"] as? String == "icarRemarkEventResource")
        #expect((json["meta"] as? [String: Any])?["sourceId"] as? String == "fixture-remark-1")
        #expect((json["agritelem"] as? [String: Any])?["category"] as? String == "Behaviour")
        #expect((json["animal"] as? [String: Any])?["id"] as? String == cow.id)
    }

    @Test func submitRemarkMapsConflictToServerState() async throws {
        let outcome = try await api(StubTransport(status: 409, fixture: "remark-conflict")).submitRemark(submission())
        guard case let .conflict(current) = outcome else {
            Issue.record("outcome = \(outcome), want conflict")
            return
        }
        #expect(current.note == "Mounting behaviour observed at milking")
    }

    @Test func rejectionCarriesServerProblems() async throws {
        await #expect(throws: APIError.rejected([APIProblem(
            code: "SOURCE_MISMATCH", field: "/meta/source",
            detail: #"meta.source "ie.spoofed" does not match the authenticated source "ie.example.gateway""#
        )])) {
            try await api(StubTransport(status: 422, fixture: "remark-rejected")).submitRemark(submission())
        }
    }

    @Test(arguments: [(401, APIError.unauthorized), (403, .forbidden), (404, .notFound), (503, .transient("server responded with HTTP 503"))])
    func statusClassification(status: Int, expected: APIError) async throws {
        await #expect(throws: expected) {
            try await api(StubTransport(status: status, fixture: nil)).herdOverview()
        }
    }

    @Test func transportFailureIsTransient() async throws {
        do {
            _ = try await api(FailingTransport()).herdOverview()
            Issue.record("expected an error")
        } catch {
            guard case .transient = error else {
                Issue.record("error = \(error), want transient")
                return
            }
        }
    }
}

@Test func malformedSuccessBodyIsAContractViolationNotTransient() async throws {
    do {
        _ = try await api(StubTransport(status: 200, fixture: "malformed")).herdOverview()
        Issue.record("expected an error")
    } catch {
        guard case .unexpectedResponse = error else {
            Issue.record("error = \(error), want unexpectedResponse")
            return
        }
    }
}
