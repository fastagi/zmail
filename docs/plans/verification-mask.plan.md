# Verification-code masking implementation plan

Spec: `docs/plans/verification-mask.zh-CN.md`. Executed inline (native) during the overnight run.

## Global constraints

- English source, brand Zmail, PostgreSQL only; no migration.
- Masked token text is `maskedSecret` (`••••••`); masked URL is `host + maskedURLPath` (`/••••`).
- Inbound only; outbound and transactional output are unchanged.

## Tasks

1. `secret_mask.go`: `maskSecretsInText(text string) (string, bool)`, `maskSecretsInHTML(html string) (string, bool)`,
   `maskSnippetSource(text string, truncated bool) string`, `maskInboundSecrets(message) maskedContent`.
   Test: `secret_mask_test.go` table tests per spec §6 (codes, links, false positives, HTML byte-identity, cross-node context).
2. DTO: `toMailMessageSummary` masks inbound subject; `toMailMessageResponse` masks inbound subject/text/html and sets
   `SecretsMasked`; `toRevealedMailMessageResponse` returns the original. Inbox snippet source masked in `inbox_service`.
   Test: `secret_mask_dto_test.go`.
3. Reveal: `POST /v1/mailboxes/:mailbox_id/messages/:message_id/reveal` in the session group, same permission as
   `GetMessage`, `Cache-Control: no-store`, structured log `mail.secret_revealed`. Route catalog, `contracts/openapi.yaml`,
   `contracts/MAILBOXES.md`, regenerated web/admin types. Test: handler test + route catalog check.
4. kest flow `api/tests/kest/verification_mask.flow.md`.
5. Web: BFF route for reveal, lock panel in the reading pane(s), i18n zh-Hans/en-US, component test; screenshots 1440×900 light/dark.
