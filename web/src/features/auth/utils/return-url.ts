import { authConfig } from '@/config/auth';

const SAME_ORIGIN_BASE = 'https://fastbuild.local';

/**
 * @util resolveReturnUrl
 * @description Normalizes post-login return URLs to same-origin paths only.
 */
export function resolveReturnUrl(value: string | null): string {
  const raw = value?.trim();
  if (!raw || !raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) {
    return authConfig.routes.afterLogin;
  }

  try {
    const url = new URL(raw, SAME_ORIGIN_BASE);
    if (url.origin !== SAME_ORIGIN_BASE) {
      return authConfig.routes.afterLogin;
    }
    return `${url.pathname}${url.search}${url.hash}`;
  } catch {
    return authConfig.routes.afterLogin;
  }
}

/**
 * @util withReturnUrl
 * @description Carries the page a visitor was sent from (an invitation link, say) from the sign-in
 * page to the sign-up page and back. Only same-origin paths are carried.
 */
export function withReturnUrl(route: string, value: string | null): string {
  const resolved = resolveReturnUrl(value);
  if (!value || resolved === authConfig.routes.afterLogin) return route;
  return `${route}?returnUrl=${encodeURIComponent(resolved)}`;
}
