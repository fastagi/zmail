import {
  MAIL_BODY_MAX_BYTES,
  MAIL_SUBJECT_MAX_LENGTH,
  MAIL_TEMPLATE_MAX_PER_ORGANIZATION,
  MAIL_TEMPLATE_NAME_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import type {
  MailTemplate,
  MailTemplateInput,
  MailTemplateKind,
  MailTemplateSummary,
} from '@/features/mailbox/types';
import { templateVariables } from '@/features/mailbox/utils/template-render';
import { cleanValues } from '@/features/mailbox/server/mock-store/helpers';
import { sendMailboxMessage } from '@/features/mailbox/server/mock-store/message-store';
import type { MockMailboxState } from '@/features/mailbox/server/mock-store/state';
import type {
  MailboxViewer,
  MockMailSendResult,
  MockMailTemplateResult,
} from '@/features/mailbox/server/mock-store/types';

/** Every template of the organization by name, or only those of `kind`. */
export function listMailTemplates(
  ctx: MockMailboxState,
  organizationId: number,
  kind?: MailTemplateKind
): MailTemplateSummary[] {
  return [...ctx.templatesOf(organizationId).values()]
    .filter(template => kind === undefined || template.kind === kind)
    .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : a.id - b.id))
    .map(template => ({
      id: template.id,
      name: template.name,
      subject: template.subject,
      variables: template.variables,
      created_at: template.created_at,
      updated_at: template.updated_at,
      kind: template.kind,
      key: template.key,
      from: template.from,
      variable_definitions: template.variable_definitions,
      // The mock has no developer scopes: every caller is inside.
      outside_sending_scope: false,
    }));
}

export function getMailTemplate(
  ctx: MockMailboxState,
  organizationId: number,
  templateId: number
): MockMailTemplateResult {
  const template = ctx.templatesOf(organizationId).get(templateId);
  return template ? { ok: true, template } : { ok: false, error: 'not_found' };
}

export function createMailTemplate(
  ctx: MockMailboxState,
  organizationId: number,
  input: MailTemplateInput
): MockMailTemplateResult {
  const templates = ctx.templatesOf(organizationId);
  const built = buildTemplate(input);
  if (!built) return { ok: false, error: 'invalid' };
  if ([...templates.values()].some(existing => existing.name === built.name)) {
    return { ok: false, error: 'taken' };
  }
  if (
    built.key !== null &&
    [...templates.values()].some(existing => existing.key === built.key)
  ) {
    return { ok: false, error: 'key_taken' };
  }
  if (templates.size >= MAIL_TEMPLATE_MAX_PER_ORGANIZATION) return { ok: false, error: 'limit' };
  const now = ctx.timestamp();
  const template: MailTemplate = {
    id: ctx.nextTemplateId++,
    ...built,
    created_at: now,
    updated_at: now,
  };
  templates.set(template.id, template);
  return { ok: true, template };
}

export function updateMailTemplate(
  ctx: MockMailboxState,
  organizationId: number,
  templateId: number,
  input: MailTemplateInput
): MockMailTemplateResult {
  const templates = ctx.templatesOf(organizationId);
  const existing = templates.get(templateId);
  if (!existing) return { ok: false, error: 'not_found' };
  const built = buildTemplate(input);
  if (!built) return { ok: false, error: 'invalid' };
  if (built.kind !== existing.kind) return { ok: false, error: 'kind_immutable' };
  if ([...templates.values()].some(other => other.id !== templateId && other.name === built.name)) {
    return { ok: false, error: 'taken' };
  }
  if (
    built.key !== null &&
    [...templates.values()].some(other => other.id !== templateId && other.key === built.key)
  ) {
    return { ok: false, error: 'key_taken' };
  }
  const template: MailTemplate = { ...existing, ...built, updated_at: ctx.timestamp() };
  templates.set(templateId, template);
  return { ok: true, template };
}

export function deleteMailTemplate(
  ctx: MockMailboxState,
  organizationId: number,
  templateId: number
): boolean {
  return ctx.templatesOf(organizationId).delete(templateId);
}

/** Sends a template with its sample values, overridden by the given ones. */
export function sendMailTemplateTest(
  ctx: MockMailboxState,
  organizationId: number,
  templateId: number,
  mailboxId: number,
  to: readonly string[],
  variables: Readonly<Record<string, string>>,
  viewer: MailboxViewer
): MockMailSendResult {
  const template = ctx.templatesOf(organizationId).get(templateId);
  if (!template) return { ok: false, error: 'template_not_found' };
  if (to.length === 0 || to.length > 5) return { ok: false, error: 'invalid' };
  return sendMailboxMessage(
    ctx,
    organizationId,
    mailboxId,
    {
      to: [...to],
      template: { id: templateId, variables: { ...template.sample_variables, ...variables } },
    },
    viewer
  );
}

/** The contract's template rules; null for content the API answers with `MAILBOX.TEMPLATE_INVALID`. */
function buildTemplate(
  input: MailTemplateInput
): Omit<MailTemplate, 'id' | 'created_at' | 'updated_at'> | null {
  const name = input.name.trim().split(/\s+/u).filter(Boolean).join(' ');
  const subject = input.subject.trim();
  const html = input.html ?? '';
  const text = input.text ?? '';
  if (name === '' || Array.from(name).length > MAIL_TEMPLATE_NAME_MAX_LENGTH) return null;
  if (subject === '' || subject.length > MAIL_SUBJECT_MAX_LENGTH || /[\r\n]/u.test(subject)) {
    return null;
  }
  if (html.trim() === '' && text.trim() === '') return null;
  if (html.length > MAIL_BODY_MAX_BYTES || text.length > MAIL_BODY_MAX_BYTES) return null;
  const samples = cleanValues(input.sample_variables ?? {});
  const variables = templateVariables({ subject, html, text });
  if (!samples || !variables) return null;
  const kind = input.kind ?? 'standard';
  const transactionalFields =
    input.key !== undefined ||
    input.from_address !== undefined ||
    input.from_name !== undefined ||
    input.variable_definitions !== undefined;
  if (kind === 'standard' && transactionalFields) return null;
  if (
    kind === 'transactional' &&
    (!input.key || !input.from_address || !input.variable_definitions)
  ) {
    return null;
  }
  return {
    name,
    subject,
    html_body: html,
    text_body: text,
    sample_variables: samples,
    variables,
    design: input.design ?? null,
    kind,
    key: kind === 'transactional' ? (input.key ?? null) : null,
    from:
      kind === 'transactional' && input.from_address
        ? { address: input.from_address.toLowerCase(), name: input.from_name ?? '' }
        : null,
    variable_definitions: kind === 'transactional' ? (input.variable_definitions ?? []) : [],
    outside_sending_scope: false,
  };
}
