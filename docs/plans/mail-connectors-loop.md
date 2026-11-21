# Mail Connectors: Autonomous Loop Instructions

Standing instructions for the agent loop that works through
[mail-connectors-roadmap.zh-CN.md](mail-connectors-roadmap.zh-CN.md). The owner asked for the roadmap
to be carried out without further questions. Progress is recorded in
[mail-connectors-progress.zh-CN.md](mail-connectors-progress.zh-CN.md).

## Each Iteration

1. Read this file and the last entries of the progress log. Check `git status`, the current branch,
   and the state of open pull requests.
2. Take the first task in the queue below that is not done or blocked.
3. Build it test-first inside the `mailbox` starter, following `AGENTS.md`, `api/AGENTS.md`, and
   `web/AGENTS.md`. Keep each task to one reviewable pull request.
4. Verify with the narrowest checks that prove the change, always with PostgreSQL:
   `FASTBUILD_TEST_POSTGRES_DSN=postgres://fastbuild:fastbuild-local-only@127.0.0.1:55432/zmail_test?sslmode=disable`.
   Database tests are skipped silently without it. Start Docker (`open -a Docker`) and
   `docker compose -f compose.dev.yaml up -d --wait postgres` first when the database is down.
   This machine runs Node 25: use `pnpm` directly and `NODE_OPTIONS=--no-experimental-webstorage`
   for Vitest.
5. Commit, push the branch, and open a pull request stacked on the previous task's branch.
6. Append an entry to the progress log: what was done, how it was verified, what is next, and any
   default decision adopted.

## Limits

- Never merge a pull request, deploy, change production, or create an account at an outside
  service. Never read or store a real credential.
- A task that needs something only the owner can supply (OAuth applications, an AI provider key,
  a production mailbox) is built and tested against a local fake server, marked `blocked: go-live`
  in the log with the exact thing needed, and the loop moves on.
- An open product decision takes the recommendation in the roadmap, section 8, and is logged as
  "default adopted, pending confirmation". Keep such choices easy to reverse.
- A new third-party dependency needs a written reason in the pull request and the checks of
  `docs/DEPENDENCY_SECURITY.md`. Prefer the standard library.
- Stop the loop when the queue is empty or every remaining task is blocked, and say so in the log.

## Queue

| # | Task | Roadmap |
|---|---|---|
| T1 | Adapter interface gaps: rotated tokens saved with the sync, paged fetch with a "more" flag, opaque cursors, cursor-expired resync | §11 |
| T2 | Gmail adapter (history API, backfill by date, token refresh) over HTTPS with a fake server; provider configuration | C2 |
| T3 | Outlook adapter (Graph per-folder delta, token rotation) with a fake server | C2 |
| T4 | Generic IMAP adapter with app passwords; credential binding flow | C5 |
| T5 | `origin` dimension: `external` becomes `personal` + `connected`; other members' personal connections hidden from managers; admin connection list and forced disconnect; access history | C1 |
| T6 | Web: connect dialog, callback page, "connected by me" group, status and reconnect | C2 |
| T7 | Shared connect: a manager binds a team account and assigns members | C3 |
| T8 | Agent grants and mail feed: grant model, cursor feed, audit; Web Agent tab | D1 |
| T9 | Sync runs table, per-provider rate budget, retention purge of revoked copies | §11, §12 |
| T10 | Tenant connect (Workspace delegation, Microsoft application access) against fake directories | C4 |
| T11 | Reply from the original address; push subscriptions with polling fallback | C6, C7 |

D2 (agent tasks with a real model) needs an AI provider key and stays blocked until one exists.

## CI Rules That Local Checks Miss

Learned from the first CI run; apply them before pushing.

- CI runs `golangci-lint` with `govet` `enable-all` (variable shadowing fails, including `err` and
  names reused inside closures in tests), `gofmt`, `goimports`, `unused`, `staticcheck`,
  `bodyclose`, `exhaustive`, `nilerr`, `unconvert`, `unparam`, `wastedassign`, and `errcheck` with
  blank assignments checked. It is not installed on this machine: run
  `go run golang.org/x/tools/go/analysis/passes/shadow/cmd/shadow@latest ./internal/modules/mailbox/`
  and `gofmt -l api` at least, and remove code that became unused.
- The Web build enforces a first-load budget of 1,070,000 raw bytes for every route, pinned by a
  governance check, so it cannot be raised. Run `pnpm build` in `web/` after any Web change.
  Every new error code adds bytes to all routes. Keep rarely used UI behind `lazy()`, and never
  import a small helper from a large component module: `rowUnread` imported from
  `message-list.tsx` once pulled that whole module into the inbox's first load.
- Merge the latest `feat/connector-adapters` into the task branch before the final check.
