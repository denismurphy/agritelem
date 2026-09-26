# Cleaning pipeline

The spec describes a five-step cleaning pipeline. Agritelem runs **step 1 at
ingest** and **steps 2–5 when summaries are read**
([ADR 0003](decisions/0003-validate-at-ingest-derive-at-read.md)). Both halves
are pure Go functions in `backend/internal/pipeline`.

Provenance tags on thresholds:
- **[spec]**: the value is in `spec.md`.
- **[ICAR]**: the value comes from ICAR ADE.
- **[chosen]**: an engineering choice. These are tunable and are not domain truth.

## Step 1: validation at ingest (`pipeline.Validate`)

Gateways submit **hourly** ICAR statistics (`duration: PT1H`). A resource is
accepted or rejected as a whole, and every problem is reported, not only the
first. Rejected resources are stored in `quarantine` with their problems.

| Check | Rejection code |
|---|---|
| `meta` present; `meta.sourceId` non-blank | `MISSING_FIELD`, `SOURCE_ID_REQUIRED` |
| `meta.source` equals the token subject | `SOURCE_MISMATCH` |
| Timestamps are UTC ([ICAR] `icarDateTimeType`) | `NON_UTC_TIMESTAMP` |
| `meta.modified` and each hour's end ≤ server time + 5 min [chosen] | `FUTURE_TIMESTAMP` |
| Animal ID valid under `std.iso.11785` or `eu.animalId` [ICAR] | `INVALID_ANIMAL_ID`, `UNSUPPORTED_ANIMAL_SCHEME` |
| Metric supported; unit and aggregation match it | `UNSUPPORTED_METRIC`, `UNIT_MISMATCH`, `AGGREGATION_MISMATCH` |
| Window aligned to the hour, duration `PT1H` | `MISALIGNED_WINDOW`, `UNSUPPORTED_DURATION` |
| Value within hourly bounds | `PHYSIOLOGICAL_OUTLIER` |
| No duplicate metric-and-hour in one resource; ≤ 744 statistics | `DUPLICATE_STATISTIC`, `TOO_MANY_STATISTICS` |
| Animal registered at the location (checked by the service) | `UNKNOWN_ANIMAL` |

### Metrics and bounds

| Metric | Scheme | Unit | Aggregation | Hourly | Daily |
|---|---|---|---|---|---|
| RuminationTime | `org.icar.activities` [ICAR] | MIN | Sum | 0–60 [chosen] | 0–720 [spec] |
| EatingTime | `org.icar.activities` [ICAR] | MIN | Sum | 0–60 [chosen] | 0–720 [chosen] |
| LyingTime | `org.icar.activities` [ICAR] | MIN | Sum | 0–60 [chosen] | 0–1440 [chosen] |
| StepCount | `org.icar.activities` [ICAR] | ACT | Sum | 0–5000 [chosen] | 0–60000 [chosen] |
| ReticularTemperature | `agritelem.metrics` | CEL | Average | 35–43 °C [chosen] | 35–43 °C [chosen] |

Activity uses ICAR's `StepCount`, the usual pedometer signal for estrus.
Reticular temperature has no ICAR code, so it uses the Agritelem scheme.

## Steps 2–5: derivation at read time (`pipeline.Derive`)

The store first rolls the latest revision of each hour into UTC days. For
each hour it takes the newest `meta.modified`, and the later ingest wins a
tie. It returns, per metric and day, the number of hours present (`n`) and
the sum of their values.

**Step 2, coverage.** `coverage = n / 24`.
- `coverage ≥ 0.85` [spec]: **Verified**
- `0.5 ≤ coverage < 0.85` [chosen]: **Imputed**
- `coverage < 0.5` [chosen]: the day is **omitted**

ICAR `isIncomplete` is true whenever `n < 24`.

**Step 5, daily value.**
- Sum metrics are scaled to a full day: `sum × 24 / n`. This is mean
  imputation of the missing hours.
- Average metrics use `sum / n`.

**Step 3, robust baseline** (14 days [spec]). Take the daily values from the
14 days before the day being scored, then:

```
median = median(history)                        needs ≥ 7 days [chosen]
MAD    = median(|x − median| for x in history)
z      = (value − median) / (1.4826 × MAD)      undefined when MAD = 0
```

1.4826 × MAD estimates the standard deviation for normally distributed data,
but unlike the standard deviation it is barely moved by a single outlier day.

**Step 4, cross-metric checks.**

| Flag | Rule |
|---|---|
| `HeatSignature` | rumination z ≤ −1.5 **and** steps z ≥ 2.0 on the same day [chosen] |
| `TimeBudgetConflict` | eating + lying > 1440 min [chosen] |
| `ThermalStress` | daily mean reticular temperature ≥ 39.5 °C [chosen] |
| `DailyOutOfRange` | value outside the metric's daily bounds |

The herd overview maps flags on the last complete UTC day to statuses:
- `HeatSignature` → **In Heat**
- `ThermalStress` → **Heat Stress**
- a Veterinary remark in the last 7 days [chosen] → **Veterinary Attention**

The herd's mean rumination uses Verified days only.

## Known limitations

- **Mean imputation is biased by the daily rhythm.** Found when running the
  gateway simulator against the live API: a sensor dropped out 08:00–18:00,
  and the imputed rumination was **595 min against a baseline near 470**
  [measured: `make sim` then the summary endpoint]. Cows ruminate least in
  daytime, so filling the gap with the mean of the night-heavy hours that
  remain overestimates. This could mask a real drop. The day is labelled
  `Imputed` and left out of herd means, which limits the damage.
  **Fix:** impute each missing hour from the animal's own hour-of-day profile
  over its baseline window.
- **Days are UTC days**, as the spec requires. For farms far from UTC, a UTC
  day splits the farm's day. Location time zones are the follow-up.
- **Deriving at read time costs work proportional to the requested range.** The
  range is capped at 366 days per request [chosen]. The herd overview scans
  15 days × 24 hours × metrics for every animal.
