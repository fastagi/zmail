import 'server-only';

import { NextResponse } from 'next/server';

import { apiErrorResponse } from '@/app/api/_shared/error-response';
import { serverEnv } from '@/config/server-env';
import { clearApiSessionCookie, getApiSessionToken } from '@/features/auth/server/api-session';
import { ApiErrorCode } from '@/http/codes';
import { privateNoStoreHeaders } from '@/server/http/private-response';
import { GoApiClient, type GoApiError, type GoApiMethod } from './go-api-client';

interface AuthenticatedGoApiRequest {
  method: GoApiMethod;
  path: string;
  accessToken?: string;
  body?: unknown;
  organizationId?: string;
  ifMatch?: string;
  ifNoneMatch?: string;
  idempotencyKey?: string;
  searchParams?: URLSearchParams;
  fieldMap?: Readonly<Record<string, string>>;
  responseType?: 'binary' | 'json';
  maxResponseBytes?: number;
  /** This request's upstream timeout instead of `API_UPSTREAM_TIMEOUT_MS`; see `GoApiRequest`. */
  timeoutMs?: number;
}

let client: GoApiClient | undefined;

export async function forwardAuthenticatedGoApi(
  request: Request,
  options: AuthenticatedGoApiRequest
): Promise<NextResponse> {
  const accessToken = options.accessToken ?? (await getApiSessionToken());
  if (!accessToken) {
    return apiErrorResponse({
      status: 401,
      errorCode: ApiErrorCode.AUTH_UNAUTHORIZED,
      message: 'Authentication required',
      headers: authenticatedResponseHeaders(),
    });
  }
  if (!serverEnv.API_UPSTREAM_URL) {
    return apiErrorResponse({
      status: 503,
      errorCode: ApiErrorCode.COMMON_SERVICE_UNAVAILABLE,
      message: 'API adapter is unavailable',
      headers: authenticatedResponseHeaders(),
    });
  }

  client ??= new GoApiClient({
    apiUrl: serverEnv.API_UPSTREAM_URL,
    timeoutMs: serverEnv.API_UPSTREAM_TIMEOUT_MS,
    maxResponseBytes: serverEnv.API_UPSTREAM_MAX_RESPONSE_BYTES,
    clientIpHeader: serverEnv.API_CLIENT_IP_HEADER,
  });
  const result = await client.request({
    ...options,
    accessToken,
    incomingHeaders: request.headers,
    signal: request.signal,
  });

  if (!result.ok) {
    if (result.error.status === 401) {
      await clearApiSessionCookie();
    }
    return goApiErrorResponse(result.error);
  }

  const headers = authenticatedResponseHeaders(result.data.responseHeaders, result.data.requestId);
  if (result.data.binary) {
    // An upstream file is always an opaque download, whatever it claims to be.
    headers.set('content-type', 'application/octet-stream');
    headers.set('x-content-type-options', 'nosniff');
    headers.set('content-security-policy', "default-src 'none'; sandbox");
    headers.set('content-disposition', result.data.binary.disposition ?? 'attachment');
    return new NextResponse(new Uint8Array(result.data.binary.bytes), {
      status: result.data.status,
      headers,
    });
  }
  if (result.data.envelope === null) {
    return new NextResponse(null, { status: result.data.status, headers });
  }
  return NextResponse.json(result.data.envelope, {
    status: result.data.status,
    headers,
  });
}

/**
 * The only error codes whose upstream `error` text reaches the browser: the text is meant for the
 * user, such as Cloudflare's reason for refusing a DNS record of the organization's own zone.
 */
const FORWARDED_DETAIL_CODES: ReadonlySet<string> = new Set([
  ApiErrorCode.MAILBOX_DNS_RECORD_REJECTED,
]);

function goApiErrorResponse(error: GoApiError): NextResponse {
  const detail =
    error.cause === 'upstream' && FORWARDED_DETAIL_CODES.has(error.errorCode)
      ? error.detail
      : undefined;
  const message =
    error.cause === 'timeout'
      ? 'API request timed out'
      : error.cause === 'unavailable'
        ? 'API service unavailable'
        : (detail ?? genericUpstreamMessage(error.status));

  return apiErrorResponse({
    status: error.status,
    errorCode: error.errorCode,
    message,
    ...(detail ? { error: detail } : {}),
    errors: error.fieldErrors,
    requestId: error.requestId,
    headers: authenticatedResponseHeaders(error.responseHeaders, error.requestId),
  });
}

function genericUpstreamMessage(status: number): string {
  if (status === 401) return 'Authentication required';
  if (status === 403) return 'Operation forbidden';
  if (status === 404) return 'Resource not found';
  if (status === 409) return 'Resource state conflict';
  if (status === 412) return 'Resource version conflict';
  if (status === 428) return 'Resource version required';
  if (status === 400 || status === 422) return 'Invalid API input';
  if (status === 429) return 'Too many requests';
  return 'API service unavailable';
}

function authenticatedResponseHeaders(
  upstream: Record<string, string> | undefined = undefined,
  requestId?: string
): Headers {
  const headers = privateNoStoreHeaders(upstream, ['Cookie']);
  if (requestId) {
    headers.set('x-request-id', requestId);
  }
  return headers;
}
