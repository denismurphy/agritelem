import Domain
import Foundation

/// Maps between generated ICAR wire types and domain values. ICAR composes
/// resources with `allOf`, which the generator represents as nested
/// `value1`/`value2`/... payloads; that nesting is confined to this file.
enum Map {
    // MARK: Errors

    static func error(status: Int, body: Components.Schemas.IcarErrorCollection?) -> APIError {
        switch status {
        case 401: return .unauthorized
        case 403: return .forbidden
        case 404: return .notFound
        case 400, 409, 413, 422: return .rejected(problems(body?.errors ?? []))
        default: return .transient("server responded with HTTP \(status)")
        }
    }

    static func problems(_ messages: [Components.Schemas.IcarResponseMessageResource]) -> [APIProblem] {
        messages.map { APIProblem(code: $0._type ?? "UNKNOWN", field: $0.instance, detail: $0.detail ?? $0.title ?? "") }
    }

    // MARK: Dates

    private static let utcCalendar: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = .gmt
        return calendar
    }()

    /// Formats the UTC calendar day containing `date` as `YYYY-MM-DD`.
    static func utcDay(_ date: Date) -> String {
        date.formatted(Date.ISO8601FormatStyle(timeZone: .gmt).year().month().day())
    }

    static func date(fromUTCDay string: String) throws(APIError) -> Date {
        guard let date = try? Date.ISO8601FormatStyle(timeZone: .gmt).year().month().day().parse(string) else {
            throw .unexpectedResponse("invalid date \(string)")
        }
        return date
    }

    static func pageToken(from next: String?) -> String? {
        guard let next, let components = URLComponents(string: next) else { return nil }
        return components.queryItems?.first { $0.name == "page-token" }?.value
    }

    // MARK: Resources

    static func animalID(_ identifier: Components.Schemas.IcarAnimalIdentifierType) -> AnimalID {
        AnimalID(scheme: identifier.value1.scheme, id: identifier.value1.id)
    }

    static func herdOverview(_ o: Components.Schemas.AgritelemHerdOverview) throws(APIError) -> HerdOverview {
        HerdOverview(
            asOf: o.asOf,
            day: try date(fromUTCDay: o.day),
            animalCount: o.kpis.animalCount,
            meanRuminationMinutes: o.kpis.meanRuminationMinutes,
            estrusAlerts: o.kpis.estrusAlerts,
            heatStress: o.kpis.heatStress,
            veterinaryAttention: o.kpis.veterinaryAttention,
            animals: o.animals.map { animal in
                HerdAnimal(
                    id: animalID(animal.animal),
                    managementTag: animal.managementTag,
                    statuses: animal.statuses.compactMap { AnimalStatus(rawValue: $0.rawValue) },
                    ruminationMinutes: animal.ruminationMinutes
                )
            }
        )
    }

    static func animal(_ r: Components.Schemas.IcarAnimalCoreResource) throws(APIError) -> Animal {
        let base = r.value1.value2
        guard let meta = r.value1.value1.meta else {
            throw .unexpectedResponse("animal \(r.value2.identifier.value1.id) has no meta")
        }
        return Animal(
            id: animalID(r.value2.identifier),
            name: base.name,
            managementTag: base.managementTag,
            breedCode: base.primaryBreed?.value1.id,
            birthDate: base.birthDate,
            modified: meta.modified,
            isDeleted: meta.isDeleted ?? false
        )
    }

    static func device(_ r: Components.Schemas.IcarDeviceResource) -> Device {
        Device(
            id: r.value2.id,
            name: r.value2.name,
            serial: r.value2.serial,
            animal: r.value2.animal.map(animalID),
            assignedSince: r.value1.meta?.validFrom
        )
    }

    static func dailyStatistics(_ r: Components.Schemas.IcarObservationSummaryResource) throws(APIError) -> [DailyStatistic] {
        try (r.value2.statistics ?? []).map { s throws(APIError) in
            guard let metric = s.value2.metric, let value = s.value2.value, let ext = s.value3.agritelem else {
                throw .unexpectedResponse("daily statistic without metric, value or derived fields")
            }
            return DailyStatistic(
                metric: Metric(scheme: metric.value1.scheme, id: metric.value1.id),
                day: s.value1.startDateTime,
                value: value,
                coverage: ext.coverage ?? 1,
                isIncomplete: s.value1.isIncomplete ?? false,
                quality: ext.dataQuality.flatMap { DataQuality(rawValue: $0.rawValue) } ?? .verified,
                baselineMedian: ext.baselineMedian,
                zScore: ext.zScore,
                flags: (ext.flags ?? []).compactMap { TelemetryFlag(rawValue: $0.rawValue) }
            )
        }
    }

    static func remark(_ r: Components.Schemas.IcarRemarkEventResource) throws(APIError) -> Remark {
        let event = r.value1.value1
        guard let meta = event.value1.meta, let sourceID = meta.sourceId else {
            throw .unexpectedResponse("remark without meta.sourceId")
        }
        guard let eventDate = event.value2.eventDateTime else {
            throw .unexpectedResponse("remark \(sourceID) has no eventDateTime")
        }
        return Remark(
            sourceID: sourceID,
            serverID: event.value2.id,
            animal: animalID(r.value1.value2.animal),
            eventDate: eventDate,
            note: r.value2.note ?? "",
            category: r.value3.agritelem?.category.flatMap { RemarkCategory(rawValue: $0.rawValue) } ?? .general,
            source: meta.source,
            modified: meta.modified
        )
    }

    static func remarkResource(_ s: RemarkSubmission, location: LocationID) -> Components.Schemas.IcarRemarkEventResource {
        let meta = Components.Schemas.IcarMetaDataType(creator: s.creator, modified: s.modified, source: s.source, sourceId: s.sourceID)
        let resource = Components.Schemas.IcarResource(
            location: .init(value1: .init(id: location.id, scheme: location.scheme)),
            meta: meta,
            resourceType: "icarRemarkEventResource"
        )
        return .init(
            value1: .init(
                value1: .init(value1: resource, value2: .init(eventDateTime: s.eventDate)),
                value2: .init(animal: .init(value1: .init(id: s.animal.id, scheme: s.animal.scheme)))
            ),
            value2: .init(note: s.note),
            value3: .init(agritelem: .init(category: Components.Schemas.AgritelemRemarkCategory(rawValue: s.category.rawValue)))
        )
    }
}
