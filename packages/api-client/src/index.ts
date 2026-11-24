export {
  ApiClientError,
  ClientErrorCode,
  HTTP_STATUS_ERROR_CODES,
  type ApiClientErrorOptions,
  type ApiErrorResponse,
  type ApiFieldErrors,
  type ApiSuccessResponse,
  type CursorPage,
  type CursorPageMeta,
  type PaginatedResponse,
  type PaginationLinks,
  type PaginationMeta,
  type ClientErrorCodeValue,
} from './types.ts';

export {
  HttpClient,
  createHttpClient,
  parseApiError,
  type AuthMode,
  type FetchLike,
  type HttpClientConfig,
  type HttpMethod,
  type HttpRequestOptions,
  type QueryParams,
  type QueryValue,
} from './client.ts';

export {
  createInboxClient,
  type InboxApiClient,
  type InboxBulkUpdatePayload,
  type InboxBulkUpdateResult,
  type InboxListParams,
  type InboxMessagePatchPayload,
  type InboxMessageRecord,
} from './inbox.ts';
