import 'server-only';

import { NextResponse } from 'next/server';
import { mockMemberWelcomeStore } from '@/features/mailbox/server/mock-member-welcome-store';
import type { MemberWelcomeLanguage } from '@/features/mailbox/member-welcome-schemas';
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  mayManageMailboxes,
  jsonObject,
  success,
  failure,
  mailboxNotFound,
  validationFailed,
  forbidden,
} from './route-shared';

/* Member welcome mail (`contracts/MAILBOXES.md`, Member Welcome Mail): owners and admins only. */

const maxMemberWelcomeBodyBytes = 32 * 1024;

const memberWelcomeLanguages: readonly string[] = ['en-US', 'zh-Hans'];

type MemberWelcomeBody = { language: MemberWelcomeLanguage; subject: string; body: string };

/** Checks the shape of a welcome setting or preview body; the API checks the copy itself. */
function memberWelcomeBody(
  value: Record<string, unknown>
): { ok: true; value: MemberWelcomeBody } | { ok: false; response: NextResponse } {
  const errors: Record<string, string[]> = {};
  const { language, subject = '', body = '' } = value;
  if (typeof language !== 'string' || !memberWelcomeLanguages.includes(language)) {
    errors.language = ['The language field must be en-US or zh-Hans'];
  }
  if (typeof subject !== 'string') errors.subject = ['The subject field must be a string'];
  if (typeof body !== 'string') errors.body = ['The body field must be a string'];
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      language: language as MemberWelcomeLanguage,
      subject: subject as string,
      body: body as string,
    },
  };
}

/** The organization's welcome mail setting. */
export async function getMemberWelcomeSettingRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'member-welcome-mail-setting',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockMemberWelcomeStore.setting(target.organizationId));
}

/** Replaces the organization's welcome mail setting. */
export async function updateMemberWelcomeSettingRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const raw = await jsonObject(request, maxMemberWelcomeBodyBytes);
  if (!raw.ok) return raw.response;
  const input = memberWelcomeBody(raw.value);
  if (!input.ok) return input.response;
  const { enabled } = raw.value;
  if (typeof enabled !== 'boolean') {
    return validationFailed({ enabled: ['The enabled field is required'] });
  }
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: 'member-welcome-mail-setting',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { enabled, ...input.value },
      fieldMap: { enabled: 'enabled', language: 'language', subject: 'subject', body: 'body' },
    });
  }
  const result = mockMemberWelcomeStore.update(target.organizationId, { enabled, ...input.value });
  return result.ok ? success(result.value) : validationFailed(result.fields);
}

/** Renders welcome copy with sample values; nothing is saved or sent. */
export async function previewMemberWelcomeRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const raw = await jsonObject(request, maxMemberWelcomeBodyBytes);
  if (!raw.ok) return raw.response;
  const input = memberWelcomeBody(raw.value);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'member-welcome-mail-previews',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { language: 'language', subject: 'subject', body: 'body' },
    });
  }
  const result = mockMemberWelcomeStore.preview(target.context.organization_name, input.value);
  return result.ok ? success(result.value) : validationFailed(result.fields);
}

/** Every mailbox that can be welcomed, with its latest welcome mail. */
export async function listMemberWelcomeMailsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'member-welcome-mails',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const candidates = mockMailboxStore.welcomeCandidates(
    target.organizationId,
    target.context.user_id
  );
  return success(mockMemberWelcomeStore.list(target.organizationId, candidates));
}

/** Sends a mailbox's welcome mail again. */
export async function resendMemberWelcomeMailRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const raw = await jsonObject(request, maxMemberWelcomeBodyBytes);
  if (!raw.ok) return raw.response;
  const mailboxId = raw.value.mailbox_id;
  if (typeof mailboxId !== 'number' || !Number.isSafeInteger(mailboxId) || mailboxId < 1) {
    return validationFailed({ mailbox_id: ['The mailbox_id field must be a positive integer'] });
  }
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'member-welcome-mails',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { mailbox_id: mailboxId },
      fieldMap: { mailbox_id: 'mailbox_id' },
    });
  }
  if (!mockMailboxStore.hasMailbox(target.organizationId, mailboxId)) return mailboxNotFound();
  const candidate = mockMailboxStore
    .welcomeCandidates(target.organizationId, target.context.user_id)
    .find(item => item.mailbox_id === mailboxId);
  if (!candidate) {
    return failure(409, ApiErrorCode.MAILBOX_WELCOME_NOT_ELIGIBLE, 'Mailbox cannot be welcomed');
  }
  const result = mockMemberWelcomeStore.resend(target.organizationId, candidate);
  if (result.ok) return success(result.value, 201);
  if (result.error === 'disabled') {
    return failure(409, ApiErrorCode.MAILBOX_WELCOME_DISABLED, 'Member welcome mails are off');
  }
  const limited = failure(
    429,
    ApiErrorCode.MAILBOX_WELCOME_RESEND_LIMITED,
    'Member welcome mail resent too often'
  );
  limited.headers.set('Retry-After', String(result.retryAfterSeconds));
  return limited;
}
