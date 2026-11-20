# V2 Hot-Path Benchmarks

Repository-level benchmarks for the paths shipped with AI triage, external mail, the operator mail
metrics, the platform account ledger, and registration email codes. Each benchmark also runs every
statement it sends once under `EXPLAIN (ANALYZE, BUFFERS)` and fails on a sequential scan of a
large table, so a plan regression fails the benchmark, not just its numbers.

## How To Run

```bash
cd api
export FASTBUILD_TEST_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/postgres?sslmode=disable
go test ./internal/modules/mailbox -run '^$' -bench '^BenchmarkV2' -benchtime=50x
go test ./internal/modules/user -run '^$' -bench 'RegistrationEmailCode' -benchtime=200x
```

Each benchmark logs one `explain` line per statement (time, sequentially scanned tables, indexes
read). Set `ZMAIL_BENCH_EXPLAIN_DIR=<dir>` to also write the full plans, one file per benchmark.
The scenes live in their own schemas and are dropped when the test binary exits. Seeding the
mailbox scene takes about 25 seconds and roughly 1 GB of database disk while it exists.

| File | Benchmarks |
|---|---|
| `api/internal/modules/mailbox/v2_hot_paths_bench_test.go` | `BenchmarkV2Inbox*`, `BenchmarkV2Operator*`, `BenchmarkV2PlatformAccounts` |
| `api/internal/modules/user/registration_email_code_bench_test.go` | `BenchmarkRegistrationEmailCode{Issue,Consume}` |
| `api/internal/infra/testing/explain.go` | `Record` (SQL capture through GORM callbacks) and `ExplainAnalyze` |

## Machine And Scale

- Apple M3 Max (14 cores, 36 GB), macOS; PostgreSQL 16.16 (aarch64) in Docker on the same machine,
  `shared_buffers=128MB`, `work_mem=4MB`. Warm cache; 50 iterations per mailbox benchmark, 200 per
  registration benchmark. Absolute numbers are machine-specific; compare before and after on one
  machine.
- Mailbox scene: 500 organizations, 2,000 users (every 40th disabled), four members per
  organization, 1,198 mailboxes. Organization 1 has 200 mailboxes (181–200 external: Gmail and
  Outlook), AI triage on mailboxes 1–100, 100,000 messages (85% inbound, 5% trash, 10% archived)
  over 60 days, triage results for all inbound mail of the triage mailboxes (15% reply, 5% deadline,
  60% FYI, 20% review), and 5,000 platform accounts. 300,000 outbound messages over 60 days across
  all mailboxes (about 5,000 a day; 92% sent, 5% failed, 3% queued) with receipts for the last 31
  days (about 145,000 events). 15,000 platform accounts in all. `mail_messages` holds 400,000 rows.
- Registration scene: 200,000 stored codes for 100,000 addresses over 48 hours.

## Results

p50/p95 in milliseconds, measured per full repository operation (all statements the service issues
for one request, e.g. access rows + page + snippets + triage for an inbox page).

| Benchmark | Before p50 / p95 | After p50 / p95 | Notes |
|---|---:|---:|---|
| Inbox `triage=reply` | 42.5 / 51.6 | **12.1 / 13.0** | query fix |
| Inbox `triage=deadline` | 43.5 / 46.7 | **7.3 / 9.1** | query fix |
| Inbox `triage=reply,review` | 43.3 / 47.2 | 42.0 / 43.4 | common categories: neutral |
| Triage counts | 21.2 / 23.8 | 20.1 / 23.3 | |
| Inbox `view=all` with external sources | 9.0 / 16.3 | 7.9 / 9.9 | |
| Inbox `view=all&group=thread` with external sources | 35.5 / 40.3 | 35.4 / 38.0 | |
| Operator overview, 7 days | 39.3 / 45.1 | **18.5 / 21.0** | index |
| Operator overview, 31 days | 110.3 / 118.2 | **56.9 / 60.8** | index |
| Operator organizations, page 1 | 26.0 / 29.2 | **5.2 / 5.6** | index |
| Operator organizations, `q=acme` | 24.2 / 26.9 | **3.9 / 4.3** | index |
| Outbound log, page 1 | 43.1 / 51.5 | **2.0 / 2.1** | index |
| Outbound log, page 100 | 47.9 / 55.7 | **3.4 / 3.9** | index |
| Outbound log, one organization | 1.3 / 1.5 | 1.3 / 1.6 | already used `idx_mail_messages_inbox_time` |
| Platform accounts, page 1 | 1.0 / 1.3 | 1.1 / 1.2 | |
| Platform accounts, `q=acct12` (handle) | 6.0 / 7.3 | 6.6 / 8.1 | |
| Platform accounts, `q=box17@` (address) | 9.4 / 10.3 | 9.0 / 10.1 | |
| Platform accounts, status + mailbox | 0.9 / 1.0 | 0.9 / 1.0 | |
| Platform accounts, member view + `q` | 2.3 / 2.5 | 2.4 / 4.4 | |
| Registration code issue (address with history) | — | 1.7 / 1.9 | |
| Registration code consume | — | 1.1 / 1.2 | |

The ns/op column of `go test -bench` matches the mean of these samples; the raw output was, for
example, `BenchmarkV2OperatorOutbound/page=1  50  2009924 ns/op  2.011 p50-ms  2.113 p95-ms`.

## EXPLAIN Findings

### Inbox triage filter: walked every message of mailboxes without matching results (fixed)

The filter was a per-message `EXISTS` on `mail_triage` inside each mailbox's lateral page. The
planner walked `idx_mail_messages_unread` newest first and probed `mail_triage_pkey` for every
message until it found 21 matches. A mailbox with triage off, or where the category is rare, has
fewer than 21 matches, so the walk read all of its Inbox mail: 70,000 probes and 180,000 buffer
hits for one page. The cost grew with the stored mail, not the page.

The filter is now `mail_messages.id IN (SELECT message_id FROM mail_triage WHERE mailbox_id = box.id
AND status IN (...) AND category IN (...))` (`inboxTriageFilter`). The planner reads each mailbox's
matching results from the existing `idx_mail_triage_results (mailbox_id, category)` and hash-joins
them to that mailbox's Inbox rows. A mailbox without results costs one index probe. Buffers per
page fell from 180,821 to about 1,100. Correlating on `mail_messages.mailbox_id` instead of `box.id`
made the planner re-run the subquery per message (480 ms), so the condition must name the lateral
`box`. That is why it lives in `inboxMailboxHeads`, the only path that allows the filter.
Categories covering a third or more of the mail (FYI, review) measure the same either way.

### Operator mail metrics: whole-table scans of `mail_messages` (fixed with an index)

Every metrics query (`countedOutbound`) filtered `mail_messages` by `direction`, `source`, and a
`created_at` range, and no index led with `created_at`. The daily trend, per-organization counts,
and both outbound log statements (count and page) all ran `Seq Scan on mail_messages` over 400,000
rows. That cost grows with all stored mail, inbound included, not with the window.

Migration `2026_11_30_090000_add_mail_outbound_time_index` adds:

```sql
CREATE INDEX idx_mail_messages_outbound_time
  ON mail_messages (created_at, id) INCLUDE (mailbox_id, status, source)
  WHERE direction = 'outbound';
```

That is 18 MB at 300,000 outbound rows. Each count is now an index-only range with zero heap
fetches, and an outbound log page is an `Index Scan Backward` that stops after offset + 20 rows.
`countedOutbound` now writes `direction = 'outbound' AND source = 'zmail'` as SQL literals, not bound
values. Generic plans under the extended protocol can then match the partial index too.

The 31-day trend still takes about 55 ms. The index scan reads its 157,000 rows in about 15 ms; the
rest is the hash join to `mailboxes` and the sort-based `GROUP BY date_trunc(...)`, which spills
3.5 MB to disk at `work_mem=4MB`. This grows with the window's messages, not the table, and stays
well under the 300 ms target. A daily rollup table would be the next step if mail volume grows by
orders of magnitude. The receipt counts read `idx_mail_message_events_message` per message of the
day (about 5,000 probes per kind).

### Plans that were already right

- Unified inbox `all` and `all&group=thread` with external mailboxes: `idx_mail_messages_inbox_time`
  per mailbox, plus `idx_mail_messages_threads` for thread summaries. `source` is a plain column
  and adds no cost.
- Triage counts: a sequential scan of `mail_triage`. All 200 requested mailboxes are the only rows
  in the table here, so the scan is the correct choice. In a multi-organization table
  `idx_mail_triage_results` (mailbox_id leads) is available for the same predicate; this scene
  does not measure that case.
- Platform accounts: `platform_accounts` is sequentially scanned (15,000 rows, a third of them
  organization 1's) and each request finishes in 1–10 ms. The `ILIKE` search on platform, handle,
  and address cannot use a B-tree index; at the 5,000-accounts-per-organization ledger size that
  costs under 10 ms, so no trigram index is warranted yet.
- Registration codes: the window count, the retention delete, and the consume lookup
  (`ORDER BY created_at DESC, id DESC LIMIT 1 FOR UPDATE`) all read
  `idx_registration_email_codes_email_created_at`. The update reads the primary key.
- Operator organization directory: `organizations` and `organization_memberships` (500 and 2,000
  rows) are sequentially scanned in under 1 ms.

## Fixes

| Fix | Kind | Effect |
|---|---|---|
| `inboxTriageFilter`: per-mailbox semi-join on `idx_mail_triage_results` | query, no migration | `triage=reply` p50 42.5 → 12.1 ms; `deadline` 43.5 → 7.3 ms |
| `idx_mail_messages_outbound_time` + literal direction/source in `countedOutbound` | migration (Up/Down test, golden schema, `UPGRADING.md` with `CREATE INDEX CONCURRENTLY`) | outbound log 43–48 → 2–3.4 ms; organizations 24–26 → 4–5 ms; overview 39/110 → 18/57 ms |
