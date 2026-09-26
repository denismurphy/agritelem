# 3. Validate at ingest, derive at read

**Status:** accepted

**Context.** The cleaning pipeline has five steps. Steps 3–4 depend on a
14-day history. Hourly data arrives late and out of order, so
stored baselines and z-scores go stale: one late hour can change the next 14
days' baselines. Keeping them fresh means recomputing up to 14 days per late
insert, and up to 24 daily revisions per animal, metric and day.

**Decision.** Persist only validated, immutable hourly observations (step 1).
Derive coverage, daily values, baselines and flags (steps 2–5) in a pure
function when summaries are read.

**Consequences.**
- Derived values are always consistent with the stored data; there is no
  recomputation machinery.
- Read cost grows with the requested range, which is capped at 366 days.
- Clients that want stored daily values must request them; there is no
  materialised daily table to query directly.
