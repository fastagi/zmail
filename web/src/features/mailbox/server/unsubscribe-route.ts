import 'server-only';

import { NextResponse } from 'next/server';

import { apiErrorResponse } from '@/app/api/_shared/error-response';
import { MAIL_UNSUBSCRIBE_TOKEN_PATTERN } from '@/features/mailbox/schemas';
import { resolveMailboxRoute } from '@/features/mailbox/server/mailbox-route';
import { MOCK_UNSUBSCRIBE_SIGNATURE } from '@/features/mailbox/server/mock-broadcast-store';
import { mockContactStore } from '@/features/mailbox/server/mock-contact-store';
import { ApiErrorCode } from '@/http/codes';
import { forwardPublicGoApi } from '@/server/api-adapter/public-route';

/**
 * The public unsubscribe route of a broadcast link. It is deliberately outside the session and
 * same-origin rules of the mailbox routes: mail clients post here for one-click unsubscribe
 * (RFC 8058) from their own servers, and the unsubscribe page posts here without a session. The
 * signed token in the path is the only credential; this route checks its shape, never reads a
 * session or the body, and leaves the signature to the API.
 */
export async function unsubscribeRoute(request: Request, rawToken: string): Promise<NextResponse> {
  const resolution = resolveMailboxRoute();
  if (!resolution.available) return resolution.response;
  if (!MAIL_UNSUBSCRIBE_TOKEN_PATTERN.test(rawToken)) return unsubscribeInvalid();
  // The body is never read; release it so the connection can be reused.
  await request.body?.cancel();
  if (resolution.backend === 'go-api') {
    const response = await forwardPublicGoApi(request, {
      method: 'POST',
      path: `mail-unsubscribes/${rawToken}`,
    });
    response.headers.set('cache-control', 'no-store');
    return response;
  }
  const [organization, contact, signature] = rawToken.split('.');
  if (signature !== MOCK_UNSUBSCRIBE_SIGNATURE) return unsubscribeInvalid();
  mockContactStore.unsubscribe(Number(organization), Number(contact));
  return new NextResponse(null, { status: 204, headers: { 'cache-control': 'no-store' } });
}

function unsubscribeInvalid(): NextResponse {
  return apiErrorResponse({
    status: 404,
    errorCode: ApiErrorCode.MAILBOX_UNSUBSCRIBE_INVALID,
    message: 'Unsubscribe link is invalid',
    headers: { 'cache-control': 'no-store' },
  });
}
