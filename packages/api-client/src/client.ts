import {
  ApiClientError,
  ClientErrorCode,
  HTTP_STATUS_ERROR_CODES,
  type ApiErrorResponse,
  type ApiFieldErrors,
  type ApiSuccessResponse,
  type CursorPage,
  type PaginatedResponse,
} from './types.ts';

export type AuthMode = 'cookie' | 'bearer';
export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
export type QueryValue = string | number | boolean | null | undefined;
export type QueryParams = Record<string, QueryValue | readonly QueryValue[]>;
export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export interface HttpClientConfig {
  /** Base URL for API requests (e.g. `https://api.zmail.app/api/v1` or `/api`). */
  baseUrl: string;
  /**
   * Authentication transport mode:
   * - `cookie`: Web browser / same-origin cookie session (`credentials: 'include'` by default).
   * - `bearer`: Desktop / Mobile token auth (`Authorization: Bearer <token>`, `credentials: 'omit'`).
   */
  authMode?: AuthMode;
  /** Fetch credentials policy override when `authMode === 'cookie'`. Defaults to `'include'`. */
  cookieCredentials?: 'include' | 'same-origin';
  /** Token provider used when `authMode === 'bearer'`. */
  getAccessToken?: () => string | null | undefined | Promise<string | null | undefined>;
  /** Default organization ID sent via the `Organization-Id` header. */
  organizationId?: number;
  /** Default request timeout in milliseconds. Defaults to `30000`. */
  timeoutMs?: number;
  /** Default headers merged into every request. */
  defaultHeaders?: Record<string, string>;
  /** Optional custom `X-Request-Id` generator. */
  generateRequestId?: () => string;
  /** Optional custom fetch implementation (useful for tests or non-browser runtimes). */
  fetch?: FetchLike;
}

export interface HttpRequestOptions {
  query?: QueryParams;
  headers?: Record<string, string>;
  organizationId?: number;
  timeoutMs?: number;
  signal?: AbortSignal;
  requestId?: string;
}

const DEFAULT_TIMEOUT_MS = 30_000;
const MAX_RETRY_AFTER_SECONDS = 86_400;

function defaultRequestId(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') {
    return `req_${globalThis.crypto.randomUUID()}`;
  }
  const timestamp = Date.now().toString(36);
  const random = Math.random().toString(36).slice(2, 10);
  return `req_${timestamp}_${random}`;
}

function parseRetryAfterSeconds(headers: Headers): number | undefined {
  const raw = headers.get('retry-after');
  if (!raw || !/^\d{1,6}$/u.test(raw.trim())) {
    return undefined;
  }
  const seconds = Number(raw.trim());
  return seconds > 0 && seconds <= MAX_RETRY_AFTER_SECONDS ? seconds : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function parseFieldErrors(value: unknown): ApiFieldErrors | undefined {
  if (!isRecord(value)) return undefined;
  const result: ApiFieldErrors = {};
  let hasEntries = false;
  for (const [field, messages] of Object.entries(value)) {
    if (Array.isArray(messages) && messages.every((item) => typeof item === 'string')) {
      result[field] = messages;
      hasEntries = true;
    }
  }
  return hasEntries ? result : undefined;
}

/**
 * Parses a non-2xx response payload into a canonical `ApiClientError`.
 */
export function parseApiError(
  status: number,
  body: unknown,
  headers: Headers,
  fallbackRequestId?: string
): ApiClientError {
  const envelope = isRecord(body) ? (body as Partial<ApiErrorResponse>) : undefined;
  const requestId =
    (typeof envelope?.request_id === 'string' && envelope.request_id.trim()) ||
    headers.get('x-request-id') ||
    fallbackRequestId;

  const errorCode =
    (typeof envelope?.error_code === 'string' && envelope.error_code.trim()) ||
    HTTP_STATUS_ERROR_CODES[status] ||
    ClientErrorCode.FETCH_ERROR;

  const message =
    (typeof envelope?.message === 'string' && envelope.message.trim()) ||
    (typeof envelope?.error === 'string' && envelope.error.trim()) ||
    `HTTP request failed with status ${status}`;

  return new ApiClientError(message, {
    errorCode,
    status,
    requestId,
    fieldErrors: parseFieldErrors(envelope?.errors),
    retryAfterSeconds: parseRetryAfterSeconds(headers),
  });
}

function buildUrl(baseUrl: string, path: string, query?: QueryParams): string {
  const normalizedBase = baseUrl.replace(/\/+$/u, '');
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  const rawUrl = `${normalizedBase}${normalizedPath}`;
  if (!query) return rawUrl;

  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item !== undefined && item !== null) {
          params.append(key, String(item));
        }
      }
    } else if (value !== undefined && value !== null) {
      params.append(key, String(value));
    }
  }

  const serialized = params.toString();
  if (!serialized) return rawUrl;
  return `${rawUrl}${rawUrl.includes('?') ? '&' : '?'}${serialized}`;
}

async function readResponseBody(response: Response): Promise<unknown> {
  if (response.status === 204 || response.status === 205) return undefined;
  const text = await response.text();
  if (!text) return undefined;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    const contentType = response.headers.get('content-type') ?? '';
    return contentType.includes('json') ? undefined : text;
  }
}

/**
 * Platform-agnostic HTTP client for Zmail Web, Admin, Desktop (Electron), and Mobile (RN).
 */
export class HttpClient {
  private readonly baseUrl: string;
  private readonly authMode: AuthMode;
  private readonly cookieCredentials: 'include' | 'same-origin';
  private readonly getAccessToken?: HttpClientConfig['getAccessToken'];
  private readonly organizationId?: number;
  private readonly timeoutMs: number;
  private readonly defaultHeaders: Record<string, string>;
  private readonly generateRequestId: () => string;
  private readonly fetchFn: FetchLike;

  constructor(config: HttpClientConfig) {
    this.baseUrl = config.baseUrl;
    this.authMode = config.authMode ?? 'cookie';
    this.cookieCredentials = config.cookieCredentials ?? 'include';
    this.getAccessToken = config.getAccessToken;
    this.organizationId = config.organizationId;
    this.timeoutMs = config.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.defaultHeaders = { ...(config.defaultHeaders ?? {}) };
    this.generateRequestId = config.generateRequestId ?? defaultRequestId;
    this.fetchFn = config.fetch ?? ((input, init) => globalThis.fetch(input, init));
  }

  public async requestEnvelope<T = unknown>(
    method: HttpMethod,
    path: string,
    body?: unknown,
    options: HttpRequestOptions = {}
  ): Promise<ApiSuccessResponse<T>> {
    const raw = await this.execute(method, path, body, options);
    if (!isRecord(raw) || raw.code !== 0 || !('data' in raw)) {
      throw new ApiClientError('API returned an invalid success envelope', {
        errorCode: ClientErrorCode.INVALID_RESPONSE,
        requestId: options.requestId,
      });
    }
    return raw as unknown as ApiSuccessResponse<T>;
  }

  public async get<T = unknown>(path: string, options?: HttpRequestOptions): Promise<T> {
    return this.unwrap<T>(await this.execute('GET', path, undefined, options));
  }

  public async getEnvelope<T = unknown>(
    path: string,
    options?: HttpRequestOptions
  ): Promise<ApiSuccessResponse<T>> {
    return this.requestEnvelope<T>('GET', path, undefined, options);
  }

  public async getPaginated<T = unknown>(
    path: string,
    options?: HttpRequestOptions
  ): Promise<PaginatedResponse<T>> {
    const raw = await this.execute('GET', path, undefined, options);
    if (!isRecord(raw) || raw.code !== 0 || !Array.isArray(raw.data) || !isRecord(raw.meta)) {
      throw new ApiClientError('API returned an invalid paginated envelope', {
        errorCode: ClientErrorCode.INVALID_RESPONSE,
        requestId: options?.requestId,
      });
    }
    return raw as unknown as PaginatedResponse<T>;
  }

  public async getCursorPage<T = unknown>(
    path: string,
    options?: HttpRequestOptions
  ): Promise<CursorPage<T>> {
    const raw = await this.execute('GET', path, undefined, options);
    if (!isRecord(raw) || raw.code !== 0 || !Array.isArray(raw.data) || !isRecord(raw.meta)) {
      throw new ApiClientError('API returned an invalid cursor page envelope', {
        errorCode: ClientErrorCode.INVALID_RESPONSE,
        requestId: options?.requestId,
      });
    }
    return raw as unknown as CursorPage<T>;
  }

  public async post<T = unknown, D = unknown>(
    path: string,
    body?: D,
    options?: HttpRequestOptions
  ): Promise<T> {
    return this.unwrap<T>(await this.execute('POST', path, body, options));
  }

  public async put<T = unknown, D = unknown>(
    path: string,
    body?: D,
    options?: HttpRequestOptions
  ): Promise<T> {
    return this.unwrap<T>(await this.execute('PUT', path, body, options));
  }

  public async patch<T = unknown, D = unknown>(
    path: string,
    body?: D,
    options?: HttpRequestOptions
  ): Promise<T> {
    return this.unwrap<T>(await this.execute('PATCH', path, body, options));
  }

  public async delete<T = void>(path: string, options?: HttpRequestOptions): Promise<T> {
    return this.unwrap<T>(await this.execute('DELETE', path, undefined, options));
  }

  private unwrap<T>(payload: unknown): T {
    if (isRecord(payload) && 'data' in payload && payload.code === 0) {
      return payload.data as T;
    }
    return payload as T;
  }

  private async execute(
    method: HttpMethod,
    path: string,
    body: unknown,
    options: HttpRequestOptions = {}
  ): Promise<unknown> {
    const requestId = options.requestId ?? this.generateRequestId();
    const url = buildUrl(this.baseUrl, path, options.query);
    const headers = new Headers(this.defaultHeaders);
    headers.set('Accept', 'application/json');
    headers.set('X-Request-Id', requestId);

    const orgId = options.organizationId ?? this.organizationId;
    if (orgId !== undefined) {
      headers.set('Organization-Id', String(orgId));
    }

    if (this.authMode === 'bearer' && this.getAccessToken) {
      const token = await this.getAccessToken();
      if (token) {
        headers.set('Authorization', `Bearer ${token}`);
      }
    }

    if (options.headers) {
      for (const [key, value] of Object.entries(options.headers)) {
        headers.set(key, value);
      }
    }

    let serializedBody: string | undefined;
    if (body !== undefined) {
      if (!headers.has('Content-Type')) {
        headers.set('Content-Type', 'application/json');
      }
      serializedBody = typeof body === 'string' ? body : JSON.stringify(body);
    }

    const controller = new AbortController();
    const timeoutMs = options.timeoutMs ?? this.timeoutMs;
    let timedOut = false;

    const timeoutHandle =
      timeoutMs > 0
        ? setTimeout(() => {
            timedOut = true;
            controller.abort();
          }, timeoutMs)
        : undefined;

    const onExternalAbort = (): void => {
      controller.abort();
    };

    if (options.signal) {
      if (options.signal.aborted) {
        if (timeoutHandle !== undefined) clearTimeout(timeoutHandle);
        throw new ApiClientError('Request was aborted', {
          errorCode: ClientErrorCode.ABORTED,
          requestId,
        });
      }
      options.signal.addEventListener('abort', onExternalAbort, { once: true });
    }

    try {
      const response = await this.fetchFn(url, {
        method,
        headers,
        body: serializedBody,
        credentials: this.authMode === 'cookie' ? this.cookieCredentials : 'omit',
        signal: controller.signal,
      });

      const responseBody = await readResponseBody(response);
      if (!response.ok) {
        throw parseApiError(response.status, responseBody, response.headers, requestId);
      }

      return responseBody;
    } catch (error: unknown) {
      if (error instanceof ApiClientError) {
        throw error;
      }
      if (timedOut) {
        throw new ApiClientError(`Request timed out after ${timeoutMs}ms`, {
          errorCode: ClientErrorCode.TIMEOUT,
          requestId,
          cause: error,
        });
      }
      if (options.signal?.aborted) {
        throw new ApiClientError('Request was aborted', {
          errorCode: ClientErrorCode.ABORTED,
          requestId,
          cause: error,
        });
      }
      const message = error instanceof Error ? error.message : 'Network request failed';
      throw new ApiClientError(message, {
        errorCode: ClientErrorCode.NETWORK_ERROR,
        requestId,
        cause: error,
      });
    } finally {
      if (timeoutHandle !== undefined) clearTimeout(timeoutHandle);
      if (options.signal) {
        options.signal.removeEventListener('abort', onExternalAbort);
      }
    }
  }
}

export function createHttpClient(config: HttpClientConfig): HttpClient {
  return new HttpClient(config);
}
