# 5. Offline sync: FIFO push, server-derived watermarks

**Status:** accepted

**Context.** A naive offline sync loop fails in four ways:
- Taking the pull watermark from the device clock skips records under clock
  skew.
- Treating every failure the same either loses writes or retries forever;
  401, 4xx, 5xx and transport errors each need their own handling.
- Without idempotent writes, a retry after a lost response becomes a false
  conflict.
- Without tombstones, clients never learn about deletions.

**Decision.**
- A remark and its queued upload are written in one SQLite transaction.
- Uploads leave oldest first. A transient failure stops the pass and backs
  off (5 s doubling, capped at 1 h), so later uploads never overtake. After 8
  attempts, or on a 4xx rejection, the upload is dead-lettered and shown
  under Needs Attention.
- A 401 pauses sync without spending attempts.
- A conflict adopts the server copy, which wins for events.
- The pull watermark is the newest `meta.modified` the server returned, and
  advances only after every page is applied.
- Each pull re-reads 60 s before the watermark, because a transaction can
  commit after a later-stamped one. Re-applying a record is idempotent.
- ICAR `meta.isDeleted` tombstones delete local copies.

- Switching server discards the previous server's watermarks and caches, but
  keeps unsent remarks.

**Consequences.** Every rule above is covered by a SyncEngine or AppDatabase
test, and each test was seen to fail with its rule removed.
