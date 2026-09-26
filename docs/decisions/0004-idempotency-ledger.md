# 4. Idempotency ledger outside the hypertable

**Status:** accepted

**Context.** Gateways and phones deliver at least once: a lost response leads
to a retry. `(meta.source, meta.sourceId)` must identify a record globally.
TimescaleDB refuses unique indexes that omit the partitioning column
[measured: psql against 2.30.1-pg17: "cannot create a unique index without the
column "t" (used in partitioning)"].

**Decision.** Keep a plain `ingest_keys` table keyed on `(source, source_id)`,
storing a hash of the resource's content. The hash excludes `meta.modified`,
which retries may restamp.
- Same key, same hash: **replayed** (200).
- Same key, different hash: **conflict**.

Corrections are new records with new source IDs. Readers take the latest
revision per hour by `meta.modified`, then by ingest sequence.
`meta.modified` more than 5 minutes ahead of the server is rejected, so a
wrong clock can't pin a value permanently.

**Consequences.**
- Keys are claimed in sorted order inside the batch transaction, so concurrent
  batches serialise on the unique index instead of deadlocking.
- Tested with eight concurrent writers sending one key.
