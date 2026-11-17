# Visual Template Editor (Reduced Plan)

## Problem

Templates are raw HTML. Marketers who send product updates cannot write email-safe HTML, so
every broadcast needs an engineer. This is the first gap in the 2026-11-17 product review.

## Decision

A block editor in the Web console. The console renders the design to email-safe HTML (tables and
inline styles) and plain text; the API stores the design next to the rendered bodies and keeps
sending the bodies, so sending, previews, test sends, and broadcasts do not change.

- **Blocks:** heading, text (with **bold**, *italic*, and `[label](url)` links), button, image
  (public URL), divider, spacer, footer. Each block has its own settings; the email has global
  colors and width.
- **Variables:** `{{name}}` and `{{name|fallback}}` work in every text field and URL, with a
  picker for contact fields (`first_name`, `last_name`, `email`) and `unsubscribe_url`.
- **Modes:** a template with a design opens in the visual editor; one without (written in HTML)
  opens in the HTML editor. Visual → HTML keeps the rendered HTML and drops the design, after a
  confirmation. HTML → visual starts a new design, also after a confirmation.
- **Starters** (welcome, notification, newsletter) become designs.

## Contract

`MailTemplate.design` (object or `null`) in responses; `design` optional in the create and
replace request. The API checks only that it is a JSON object with a positive integer `version`
and at most 256 KiB, and stores it as given; the bodies stay authoritative.

## Out Of Scope

Image upload (needs public asset storage), drag and drop (up and down controls instead), columns,
saved brand themes, and server-side rendering of designs.

## Verification

Renderer unit tests (escaping, link and URL safety, text version), editor UI tests, API tests for
the design round trip and validation, the mock BFF, and a browser check that a design saves,
reloads, and sends a test message.
