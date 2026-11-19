// Errors translations - English (US)
const messages = {
  notFound: 'Page not found',
  serverError: 'Server error',
  networkError: 'Network error, please check your connection',
  rateLimited: 'Too many attempts, please try again later',
  unauthorized: 'Please login to continue',
  validationFailed: 'Please review the highlighted fields',
  forbidden: "You don't have permission to access this resource",
  authForbiddenDescription:
    'This session cannot access the protected application. Contact an administrator if this is unexpected.',
  authUnavailable: 'Unable to verify your session',
  authUnavailableDescription: 'Your session was not changed. Check your connection and try again.',
  transactional: {
    disabled: 'Transactional mail is not enabled on this instance.',
    rateLimited: 'Too many test sends.',
    recipientRateLimited: 'This template went to the same address too often.',
    retryIn: 'Please try again in {seconds} s.',
    retryLater: 'Please try again later.',
    messageNotFound: 'This message no longer exists. The send log keeps messages for 30 days.',
    idempotencyConflict: 'This idempotency key was already used for different content.',
    idempotencyKeyInvalid: 'The idempotency key is missing or malformed.',
  },
} as const;

export default messages;
export type ErrorsMessages = typeof messages;
