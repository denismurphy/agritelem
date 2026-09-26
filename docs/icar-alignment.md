# ICAR ADE alignment

Agritelem implements a **subset of ICAR Animal Data Exchange 1.5.1**. The
bundle is vendored in `contracts/vendor/icar-ade-1.5.1/` (tag `v1.5.1`, commit
`2448ee27`). This page lists which ICAR operations are used, what Agritelem
adds, and where Agritelem's policies are stricter than the standard.

## Identifier and code schemes

| Kind | Scheme | Source |
|---|---|---|
| Animals | `std.iso.11785`, `eu.animalId` | ICAR `well-known/icarAnimalIdentifierType.md` |
| Metrics | `org.icar.activities` (`RuminationTime`, `EatingTime`, `LyingTime`, `StepCount`) | ICAR `well-known/icarObservationSummaryMetrics.md` |
| Metrics | `agritelem.metrics` (`ReticularTemperature`) | Agritelem; ICAR defines no code for it |
| Breeds | `icar.breed-3` (`HOL`, `JER`, `BSW`, `MON`) | ICAR `well-known/icarBreedIdentifierType.md`, Interbull codes |
| Locations | `dk.herdnumber` (demo herd) | ICAR `well-known/icarLocationIdentifierType.md` |
| Units | `MIN`, `ACT`, `CEL` | UN/CEFACT, as ICAR recommends |

## Operations used (unchanged paths)

| Operation | ICAR path |
|---|---|
| List animals | `GET /locations/{scheme}/{id}/animals` |
| List devices | `GET /locations/{scheme}/{id}/devices` |
| Observation summaries | `GET /locations/{scheme}/{id}/observation-summary-metrics` |
| Batch ingest | `POST /batches/locations/{scheme}/{id}/observation-summary-metrics` |
| Remarks | `GET` and `POST /locations/{scheme}/{id}/remarks` |

## Agritelem extensions

All additions are **additive**. `contractgen` rejects any extension that
changes an ICAR value, so every ICAR constraint still holds.

| Extension | Where | Why |
|---|---|---|
| `agritelem` object on `icarObservationStatisticsType` | coverage, quality, baseline median, z-score, flags | Pipeline output (steps 2–5) |
| `agritelem.category` on `icarRemarkEventResource` | General / Veterinary / Behaviour / Feeding | ICAR remarks are free text; the dashboard needs veterinary remarks |
| `page-size`, `page-token` query parameters | animals, remarks | Keyset pagination for offline sync |
| `meta-modified-from` on animals | animals | ICAR 1.5.1 lists it for other collections but not animals |
| `409` response with the stored remark | `POST remarks` | Server-wins conflict resolution needs the server copy |
| `GET /agritelem/locations/{scheme}/{id}/herd-overview` | new path | ICAR has no herd KPI resource |

## Policies stricter than ICAR

- **`meta.sourceId` is required on writes.** ICAR 1.5.1 makes it optional
  until 2.0; Agritelem needs it for idempotent retries.
- **`meta.source` must equal the token subject**, so a gateway cannot write as
  another.
- **Ingest accepts hourly statistics only** (`PT1H`), and animal IDs only under
  `std.iso.11785` or `eu.animalId`.
- **Timestamps are whole-second UTC** (`YYYY-MM-DDTHH:MM:SSZ`), as `spec.md` §5
  requires. The server truncates sub-second precision on output.
- **Unimplemented filters are refused, not ignored.** `date-from`/`date-to` and
  `meta-modified-to` on remarks and devices return 400. Silently ignoring a
  filter would hand clients unfiltered data.
- **Create returns 200 with the stored resource.** ICAR allows 200 or
  201/202 for POST. Always returning the stored copy gives idempotent retries
  the same response as the first success.

## Upstream quirks

- In the 1.5.1 bundle, the components named `date-from` and `date-to` declare
  each other's wire names. Operations always reference both, so the wire
  parameters are correct; only the generated type names read swapped.
- `swift-openapi-generator` 1.13 drops properties declared as
  `anyOf: [{type: null}, {$ref: …}]`. `contractgen` rewrites the equivalent
  `type: [string, "null"]` form for `meta.created`, `validFrom` and `validTo`.
