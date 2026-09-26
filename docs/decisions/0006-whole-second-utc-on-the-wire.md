# 6. Whole-second UTC timestamps on the wire

**Status:** accepted

**Context.** Go marshals `time.Time` with nanoseconds whenever they are
non-zero, and PostgreSQL `now()` has microseconds. An end-to-end test caught
`meta.modified = "2026-09-26T18:24:11.753589Z"`. The specification requires
`YYYY-MM-DDTHH:MM:SSZ`, and ISO 8601 decoders commonly omit fractional
seconds.

**Decision.** The HTTP layer truncates every outgoing timestamp to whole UTC
seconds. Page tokens keep full precision internally. The sync watermark
tolerates the truncation, because pulls filter with `>=` and re-read an
overlap window.

**Consequences.** Clients can use a strict ISO 8601 decoder. The end-to-end
test asserts the format with a regular expression.
