/**
 * Shared HTTP envelope and error contract types matching `contracts/README.md`.
 * All JSON wire fields remain in `snake_case`.
 */

/** Standard non-paginated API success envelope. */
export interface ApiSuccessResponse<T = unknown> {
  code: 0;
  message: string;
  data: T;
  meta?: unknown;
  links?: unknown;
}

/** Field-level validation error map keyed by JSON field name. */
export type ApiFieldErrors = Record<string, string[]>;

/** Standard API error envelope. */
export interface ApiErrorResponse {
  code: number;
  error_code: string;
  message: string;
  error?: string;
  errors?: ApiFieldErrors;
  request_id?: string;
}

/** Keyset pagination metadata for unbounded collections. */
export interface CursorPageMeta {
  per_page: number;
  has_more: boolean;
  next_cursor: string | null;
}

/** Keyset-paginated success envelope. */
export interface CursorPage<T = unknown> {
  code: 0;
  message: string;
  data: T[];
  meta: CursorPageMeta;
}

/** Offset pagination metadata. */
export interface PaginationMeta {
  current_page: number;
  per_page: number;
  total: number;
  last_page: number;
  from: number;
  to: number;
}

/** Offset pagination navigation links. */
export interface PaginationLinks {
  first: string;
  last: string;
  prev: string | null;
  next: string | null;
}

/** Offset-paginated success envelope. */
export interface PaginatedResponse<T = unknown> {
  code: 0;
  message: string;
  data: T[];
  meta: PaginationMeta;
  links: PaginationLinks;
}

/** Client-synthesized error codes when no server error envelope is available. */
export const ClientErrorCode = {
  NETWORK_ERROR: 'CLIENT.NETWORK_ERROR',
  TIMEOUT: 'CLIENT.TIMEOUT',
  ABORTED: 'CLIENT.ABORTED',
  INVALID_RESPONSE: 'CLIENT.INVALID_RESPONSE',
  FETCH_ERROR: 'CLIENT.FETCH_ERROR',
} as const;

export type ClientErrorCodeValue = (typeof ClientErrorCode)[keyof typeof ClientErrorCode];

/** Fallback canonical error codes mapped from HTTP status codes (`contracts/README.md`). */
export const HTTP_STATUS_ERROR_CODES: Readonly<Record<number, string>> = {
  400: 'COMMON.INVALID_INPUT',
  401: 'AUTH.UNAUTHORIZED',
  403: 'AUTH.FORBIDDEN',
  404: 'COMMON.NOT_FOUND',
  409: 'COMMON.CONFLICT',
  413: 'COMMON.REQUEST_TOO_LARGE',
  422: 'COMMON.VALIDATION_FAILED',
  429: 'COMMON.RATE_LIMITED',
  500: 'COMMON.INTERNAL',
  503: 'COMMON.SERVICE_UNAVAILABLE',
  504: 'COMMON.TIMEOUT',
};

export interface ApiClientErrorOptions {
  errorCode: string;
  status?: number;
  requestId?: string;
  fieldErrors?: ApiFieldErrors;
  retryAfterSeconds?: number;
  cause?: unknown;
}

/**
 * Canonical error thrown by `HttpClient` for HTTP, transport, timeout, and envelope failures.
 */
export class ApiClientError extends Error {
  public readonly errorCode: string;
  public readonly status?: number;
  public readonly requestId?: string;
  public readonly fieldErrors?: ApiFieldErrors;
  /** Whole seconds parsed from a `Retry-After` delta-seconds header, capped at 24 hours. */
  public readonly retryAfterSeconds?: number;

  constructor(message: string, options: ApiClientErrorOptions) {
    super(message, options.cause !== undefined ? { cause: options.cause } : undefined);
    this.name = 'ApiClientError';
    this.errorCode = options.errorCode;
    this.status = options.status;
    this.requestId = options.requestId;
    this.fieldErrors = options.fieldErrors;
    this.retryAfterSeconds = options.retryAfterSeconds;
  }
}
