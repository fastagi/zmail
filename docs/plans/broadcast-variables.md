# Broadcast Variables (Reduced Plan)

## Problem

A broadcast fills a template only from each contact (`email`, `first_name`, `last_name`, its
properties, and `unsubscribe_url`). A template that uses a value shared by the whole campaign, such
as `{{promo_code}}`, `{{event_date}}`, or `{{action_url}}`, fails every recipient with
`template.variables_missing` unless every contact happens to carry it. Marketers have to edit the
template for each campaign. This is the second gap in the 2026-11-17 product review.

## Decision

- A broadcast has `variables`: named values shared by every recipient, edited with the draft and
  fixed when sending starts (the worker reads them from the broadcast row, which only a draft can
  change).
- Each recipient's values are, from lowest to highest: the broadcast's variables, then the
  contact's properties, then `email`, `first_name`, `last_name`, and `unsubscribe_url`. A contact
  property therefore overrides a shared value of the same name (a personal code beats the campaign
  code), and the reserved contact fields cannot be set on a broadcast.
- Limits are the template-send limits: at most 100 values, names like other variables, values of at
  most 2,000 characters, cleaned like message text.
- The console lists the template's variables on the broadcast form: contact fields are marked as
  filled automatically, every other variable gets an input, and a variable with no value and no
  fallback is called out before sending, because contacts without that property will fail.

## Contract

`MailBroadcast.variables` (object of strings, always present) in responses; `variables` optional in
the create and replace request. Invalid names, reserved names, or too many or too long values are
`422 MAILBOX.BROADCAST_INVALID`.

## Out Of Scope

Per-recipient overrides other than contact properties, typed values (dates, numbers), and a
server-side pre-send check of variables against every contact's properties.

## Verification

Service tests for validation and precedence, a PostgreSQL repository round trip, the mock BFF and
BFF validation, broadcast form UI tests, and a broadcast on production to an internal address whose
message shows a shared value.
