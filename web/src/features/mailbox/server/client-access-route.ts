import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse } from '@/app/api/_shared/success-response';
import {
  MAIL_SIGNATURE_MAX_LENGTH,
  normalizeMailSignature,
} from '@/features/mailbox/signature-schemas';
import { mockSignatureStore } from '@/features/mailbox/server/mock-signature-store';
import { mockMailAppPasswordStore } from '@/features/mailbox/server/mock-mail-app-password-store';
import { verifyMockAccountPassword } from '@/features/auth/server/mock-identity';
import { MAIL_APP_PASSWORD_NAME_MAX_LENGTH } from '@/features/mailbox/mail-client-schemas';
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type MailboxTarget,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  mayManageMailboxes,
  viewerOf,
  jsonObject,
  parseRouteId,
  privateMailboxHeaders,
  success,
  failure,
  mailboxNotFound,
  validationFailed,
  forbidden,
} from './route-shared';

/** 2000 characters of up to four UTF-8 bytes, JSON-escaped, with room for the envelope. */
const maxSignatureBodyBytes = 16_384;

/** Reads the caller's signature for a mailbox they may send from (`contracts/MAILBOXES.md`). */
export async function getMailSignatureRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId.value}/signature`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const viewer = viewerOf(target);
  const mailbox = mockMailboxStore
    .list(target.organizationId, viewer)
    .find(item => item.id === mailboxId.value);
  if (!mailbox) return mailboxNotFound();
  if (!mailbox.access.send) return forbidden();
  return success(mockSignatureStore.read(target.organizationId, mailbox.id, viewer.userId));
}

/** Replaces the caller's signature; an empty body removes it. */
export async function updateMailSignatureRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const body = await jsonObject(request, maxSignatureBodyBytes);
  if (!body.ok) return body.response;
  const { body: raw } = body.value;
  if (typeof raw !== 'string') {
    return validationFailed({ body: ['The body field is required'] });
  }
  const signature = normalizeMailSignature(raw);
  if ([...signature].length > MAIL_SIGNATURE_MAX_LENGTH) {
    return validationFailed({ body: ['must be at most 2000 characters'] });
  }
  if (/[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/.test(signature)) {
    return validationFailed({ body: ['must not contain control characters'] });
  }

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${mailboxId.value}/signature`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { body: raw },
      fieldMap: { body: 'body' },
    });
  }
  const viewer = viewerOf(target);
  const mailbox = mockMailboxStore
    .list(target.organizationId, viewer)
    .find(item => item.id === mailboxId.value);
  if (!mailbox) return mailboxNotFound();
  if (!mailbox.access.send) return forbidden();
  return success(
    mockSignatureStore.save(target.organizationId, mailbox.id, viewer.userId, signature)
  );
}

/* Mail client access (`contracts/MAILBOXES.md`, Mail Client Access). */

const maxMailAppPasswordBodyBytes = 2048;

/** The mock caller of the mail client routes, with whether the membership may use mail apps. */
function mailClientCaller(target: MailboxTarget & { available: true; backend: 'mock' }) {
  return {
    organizationId: target.organizationId,
    userId: target.context.user_id,
    granted: target.context.features?.mail_clients !== false,
  };
}

/** The caller's own mail client passwords, the cap, whether mail apps can sign in, and where. */
export async function listMailAppPasswordsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-app-passwords',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockMailAppPasswordStore.list(mailClientCaller(target)));
}

/**
 * Creates a mail client password after the account credentials; the response is the only one
 * that carries its secret. The credentials are forwarded once and never logged.
 */
export async function createMailAppPasswordRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const body = await jsonObject(request, maxMailAppPasswordBodyBytes);
  if (!body.ok) return body.response;
  const { name, scopes, current_password: currentPassword, totp_code: totpCode } = body.value;
  const errors: Record<string, string[]> = {};
  const trimmed = typeof name === 'string' ? name.trim() : '';
  if (trimmed === '' || [...trimmed].length > MAIL_APP_PASSWORD_NAME_MAX_LENGTH) {
    errors.name = ['must be 1 to 64 characters'];
  }
  const protocols = Array.isArray(scopes) ? scopes : [];
  if (
    protocols.length === 0 ||
    protocols.some(scope => scope !== 'imap' && scope !== 'smtp') ||
    new Set(protocols).size !== protocols.length
  ) {
    errors.scopes = ['must be one or both of imap and smtp'];
  }
  if (typeof currentPassword !== 'string' || currentPassword === '') {
    errors.current_password = ['is required'];
  }
  if (totpCode !== undefined && typeof totpCode !== 'string') {
    errors.totp_code = ['is invalid'];
  }
  if (Object.keys(errors).length > 0) return validationFailed(errors);

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mail-app-passwords',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: {
        name: trimmed,
        scopes: protocols,
        current_password: currentPassword,
        ...(typeof totpCode === 'string' && totpCode !== '' ? { totp_code: totpCode } : {}),
      },
      fieldMap: {
        name: 'name',
        scopes: 'scopes',
        current_password: 'current_password',
        totp_code: 'totp_code',
      },
    });
  }
  const result = mockMailAppPasswordStore.create(
    mailClientCaller(target),
    {
      name: trimmed,
      scopes: protocols as ('imap' | 'smtp')[],
      currentPassword: currentPassword as string,
    },
    verifyMockAccountPassword
  );
  if (result.ok) return success(result.value, 201);
  switch (result.error) {
    case 'validation':
      return validationFailed(result.fields);
    case 'rate_limited': {
      const response = failure(429, ApiErrorCode.COMMON_RATE_LIMITED, 'Too many attempts');
      response.headers.set('Retry-After', String(result.retryAfterSeconds));
      return response;
    }
    case 'feature_not_granted':
      return failure(
        403,
        ApiErrorCode.ORGANIZATION_FEATURE_NOT_GRANTED,
        'An organization owner or admin has not granted you this feature'
      );
    case 'disabled':
      return failure(
        403,
        ApiErrorCode.MAILBOX_MAIL_CLIENT_ACCESS_DISABLED,
        'Mail client access is disabled for this organization'
      );
    case 'limit_reached':
      return failure(
        409,
        ApiErrorCode.MAILBOX_APP_PASSWORD_LIMIT_REACHED,
        'Mail client password limit reached'
      );
  }
}

/** Revokes one of the caller's own mail client passwords at once. */
export async function revokeMailAppPasswordRoute(
  request: Request,
  rawPasswordId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const passwordId = parseRouteId(rawPasswordId);
  if (!passwordId.ok) return passwordId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-app-passwords/${passwordId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockMailAppPasswordStore.revoke(mailClientCaller(target), passwordId.value)
    ? apiNoContentResponse(privateMailboxHeaders())
    : failure(404, ApiErrorCode.MAILBOX_APP_PASSWORD_NOT_FOUND, 'Mail client password not found');
}

/** The organization's mail app sign-in switch; every member may read it. */
export async function getMailClientSettingsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-client-settings',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockMailAppPasswordStore.settings(target.organizationId));
}

/** Turns mail app sign-in on or off for the organization; owners and admins only. */
export async function updateMailClientSettingsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const body = await jsonObject(request, maxMailAppPasswordBodyBytes);
  if (!body.ok) return body.response;
  const { access_enabled: accessEnabled } = body.value;
  if (typeof accessEnabled !== 'boolean') {
    return validationFailed({ access_enabled: ['The access_enabled field is required'] });
  }
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: 'mail-client-settings',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { access_enabled: accessEnabled },
      fieldMap: { access_enabled: 'access_enabled' },
    });
  }
  return success(mockMailAppPasswordStore.updateSettings(target.organizationId, accessEnabled));
}
