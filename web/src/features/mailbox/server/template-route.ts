import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse } from '@/app/api/_shared/success-response';
import {
  MAIL_BODY_MAX_BYTES,
  MAIL_TEMPLATE_DESIGN_MAX_BYTES,
  MAIL_SUBJECT_MAX_LENGTH,
  MAIL_TEMPLATE_MAX_VARIABLES,
  MAIL_TEMPLATE_NAME_MAX_LENGTH,
  MAIL_TEMPLATE_TEST_MAX_RECIPIENTS,
  MAIL_TEMPLATE_VARIABLE_PATTERN,
} from '@/features/mailbox/schemas';
import {
  mockMailboxStore,
  type MockMailTemplateResult,
} from '@/features/mailbox/server/mock-mailbox-store';
import type {
  MailTemplateInput,
  MailTemplateKind,
  MailTemplateTestInput,
} from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type Parsed,
  privateMailboxResponse,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  viewerOf,
  jsonObject,
  boundedString,
  addressList,
  parseRouteId,
  isOneOf,
  success,
  failure,
  templateNotFound,
  invalidInput,
  validationFailed,
  variableMap,
  mockSendResponse,
} from './route-shared';

// Two 256 KiB bodies with JSON escaping.
const maxTemplateBodyBytes = 2 * 1_024 * 1_024;

const maxTemplateTestBodyBytes = 512 * 1_024;

const templateKinds: readonly MailTemplateKind[] = ['standard', 'transactional'];

export async function listMailTemplatesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const kind = new URL(request.url).searchParams.get('kind') ?? '';
  if (kind !== '' && !isOneOf(templateKinds, kind)) return invalidInput('Invalid template kind');
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-templates',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      // Only the kind filter reaches the API; anything else in the query is dropped.
      ...(kind === '' ? {} : { searchParams: new URLSearchParams({ kind }) }),
    });
  }
  return success(
    mockMailboxStore.listTemplates(target.organizationId, kind === '' ? undefined : kind)
  );
}

export async function createMailTemplateRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const input = await templateBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mail-templates',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: templateFieldMap,
    });
  }
  return mockTemplateResponse(
    mockMailboxStore.createTemplate(target.organizationId, input.value),
    201
  );
}

export async function getMailTemplateRoute(
  request: Request,
  rawTemplateId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const templateId = parseRouteId(rawTemplateId);
  if (!templateId.ok) return templateId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mail-templates/${templateId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockTemplateResponse(
    mockMailboxStore.getTemplate(target.organizationId, templateId.value)
  );
}

export async function updateMailTemplateRoute(
  request: Request,
  rawTemplateId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const templateId = parseRouteId(rawTemplateId);
  if (!templateId.ok) return templateId.response;
  const input = await templateBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mail-templates/${templateId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: templateFieldMap,
    });
  }
  return mockTemplateResponse(
    mockMailboxStore.updateTemplate(target.organizationId, templateId.value, input.value)
  );
}

export async function deleteMailTemplateRoute(
  request: Request,
  rawTemplateId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const templateId = parseRouteId(rawTemplateId);
  if (!templateId.ok) return templateId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-templates/${templateId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockMailboxStore.deleteTemplate(target.organizationId, templateId.value)
    ? privateMailboxResponse(apiNoContentResponse())
    : templateNotFound();
}

export async function sendMailTemplateTestRoute(
  request: Request,
  rawTemplateId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const templateId = parseRouteId(rawTemplateId);
  if (!templateId.ok) return templateId.response;
  const input = await templateTestBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mail-templates/${templateId.value}/tests`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { mailbox_id: 'mailbox_id', to: 'to' },
    });
  }
  return mockSendResponse(
    mockMailboxStore.sendTemplateTest(
      target.organizationId,
      templateId.value,
      input.value.mailbox_id,
      input.value.to,
      input.value.variables ?? {},
      viewerOf(target)
    )
  );
}

const templateFieldMap = {
  name: 'name',
  subject: 'subject',
  html: 'html',
  text: 'text',
  sample_variables: 'sample_variables',
  kind: 'kind',
  key: 'key',
  from_address: 'from_address',
  from_name: 'from_name',
  variable_definitions: 'variable_definitions',
} as const;

function mockTemplateResponse(result: MockMailTemplateResult, status: 200 | 201 = 200) {
  if (result.ok) return success(result.template, status);
  switch (result.error) {
    case 'not_found':
      return templateNotFound();
    case 'taken':
      return failure(409, ApiErrorCode.MAILBOX_TEMPLATE_NAME_TAKEN, 'Mail template name is taken');
    case 'key_taken':
      return failure(409, ApiErrorCode.MAILBOX_TEMPLATE_KEY_TAKEN, 'Mail template key is taken');
    case 'kind_immutable':
      return failure(
        409,
        ApiErrorCode.MAILBOX_TEMPLATE_KIND_IMMUTABLE,
        'Mail template kind cannot change'
      );
    case 'limit':
      return failure(
        409,
        ApiErrorCode.MAILBOX_TEMPLATE_LIMIT_REACHED,
        'Mail template limit reached'
      );
    default:
      return failure(422, ApiErrorCode.MAILBOX_TEMPLATE_INVALID, 'Mail template is invalid');
  }
}

/** Shape checks only; names, placeholders, and bodies are judged by the API or the mock. */
function templateFields(body: Record<string, unknown>): Parsed<MailTemplateInput> {
  const {
    name,
    subject,
    html,
    text,
    sample_variables: samples,
    design,
    kind,
    key,
    from_address: fromAddress,
    from_name: fromName,
    variable_definitions: definitions,
    ...rest
  } = body;
  const errors: Record<string, string[]> = {};
  if (Object.keys(rest).length > 0) errors.body = ['Unknown fields'];
  if (kind !== undefined && kind !== 'standard' && kind !== 'transactional') {
    errors.kind = ['The kind field is invalid'];
  }
  if (key !== undefined && !(typeof key === 'string' && MAIL_TEMPLATE_KEY_PATTERN.test(key))) {
    errors.key = ['The key field is invalid'];
  }
  if (
    fromAddress !== undefined &&
    !boundedString(fromAddress, MAIL_TEMPLATE_FROM_ADDRESS_MAX_LENGTH)
  ) {
    errors.from_address = ['The from_address field is invalid'];
  }
  if (fromName !== undefined && !boundedString(fromName, MAIL_TEMPLATE_FROM_NAME_MAX_LENGTH)) {
    errors.from_name = ['The from_name field is invalid'];
  }
  if (definitions !== undefined && !variableDefinitions(definitions)) {
    errors.variable_definitions = ['The variable_definitions field is invalid'];
  }
  if (!boundedString(name, MAIL_TEMPLATE_NAME_MAX_LENGTH) || name === '') {
    errors.name = ['The name field is invalid'];
  }
  if (!boundedString(subject, MAIL_SUBJECT_MAX_LENGTH) || subject === '') {
    errors.subject = ['The subject field is invalid'];
  }
  if (html !== undefined && !boundedString(html, MAIL_BODY_MAX_BYTES)) {
    errors.html = ['The html field is invalid'];
  }
  if (text !== undefined && !boundedString(text, MAIL_BODY_MAX_BYTES)) {
    errors.text = ['The text field is invalid'];
  }
  if (samples !== undefined && !variableMap(samples)) {
    errors.sample_variables = ['The sample_variables field is invalid'];
  }
  if (design !== undefined && design !== null && !templateDesign(design)) {
    errors.design = ['The design field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      name: name as string,
      subject: subject as string,
      ...(typeof html === 'string' ? { html } : {}),
      ...(typeof text === 'string' ? { text } : {}),
      ...(variableMap(samples) ? { sample_variables: samples } : {}),
      ...(templateDesign(design) ? { design } : {}),
      ...(kind === 'standard' || kind === 'transactional' ? { kind } : {}),
      ...(typeof key === 'string' ? { key } : {}),
      ...(typeof fromAddress === 'string' ? { from_address: fromAddress } : {}),
      ...(typeof fromName === 'string' ? { from_name: fromName } : {}),
      ...(variableDefinitions(definitions) ? { variable_definitions: definitions } : {}),
    },
  };
}

const MAIL_TEMPLATE_KEY_PATTERN = /^[a-z][a-z0-9-]{0,63}$/u;

const MAIL_TEMPLATE_FROM_ADDRESS_MAX_LENGTH = 320;

const MAIL_TEMPLATE_FROM_NAME_MAX_LENGTH = 100;

/** Shape checks only; the declaration rules are judged by the API or the mock. */
function variableDefinitions(
  value: unknown
): value is { name: string; secret: boolean; required: boolean }[] {
  return (
    Array.isArray(value) &&
    value.length <= MAIL_TEMPLATE_MAX_VARIABLES &&
    value.every(item => {
      if (typeof item !== 'object' || item === null || Array.isArray(item)) return false;
      const { name, secret, required, ...rest } = item as Record<string, unknown>;
      return (
        Object.keys(rest).length === 0 &&
        typeof name === 'string' &&
        MAIL_TEMPLATE_VARIABLE_PATTERN.test(name) &&
        typeof secret === 'boolean' &&
        typeof required === 'boolean'
      );
    })
  );
}

/** A design document as the contract describes it: an object with a positive integer version. */
function templateDesign(value: unknown): value is { version: number; [key: string]: unknown } {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const { version } = value as { version?: unknown };
  if (typeof version !== 'number' || !Number.isInteger(version) || version < 1) return false;
  return new TextEncoder().encode(JSON.stringify(value)).length <= MAIL_TEMPLATE_DESIGN_MAX_BYTES;
}

async function templateBody(request: Request): Promise<Parsed<MailTemplateInput>> {
  const body = await jsonObject(request, maxTemplateBodyBytes);
  return body.ok ? templateFields(body.value) : body;
}

async function templateTestBody(request: Request): Promise<Parsed<MailTemplateTestInput>> {
  const body = await jsonObject(request, maxTemplateTestBodyBytes);
  if (!body.ok) return body;
  const { mailbox_id: mailboxId, to, variables, ...rest } = body.value;
  const errors: Record<string, string[]> = {};
  if (Object.keys(rest).length > 0) errors.body = ['Unknown fields'];
  if (!(typeof mailboxId === 'number' && Number.isSafeInteger(mailboxId) && mailboxId > 0)) {
    errors.mailbox_id = ['The mailbox_id field is invalid'];
  }
  if (!addressList(to) || to.length === 0 || to.length > MAIL_TEMPLATE_TEST_MAX_RECIPIENTS) {
    errors.to = ['The to field is invalid'];
  }
  if (variables !== undefined && !variableMap(variables)) {
    errors.variables = ['The variables field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      mailbox_id: mailboxId as number,
      to: to as string[],
      ...(variableMap(variables) ? { variables } : {}),
    },
  };
}
