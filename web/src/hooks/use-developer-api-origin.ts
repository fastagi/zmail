'use client';

import { useSyncExternalStore } from 'react';

import { env } from '@/config/env';

/**
 * The API origin code samples print:
 * - `pending` before hydration, while the page does not yet know its own address;
 * - `ready` with the configured public API origin, or else this site's own origin;
 * - `unavailable` when neither is a usable http(s) origin, so no sample can be copied.
 */
export type DeveloperApiOrigin =
  | { status: 'pending' }
  | { status: 'ready'; origin: string }
  | { status: 'unavailable' };

/** An http(s) origin without a trailing slash, or null. */
function httpOrigin(value: string | null | undefined): string | null {
  const trimmed = (value ?? '').trim().replace(/\/+$/u, '');
  if (trimmed === '') return null;
  try {
    const url = new URL(trimmed);
    return url.protocol === 'http:' || url.protocol === 'https:' ? trimmed : null;
  } catch {
    return null;
  }
}

/**
 * The configured origin wins; without one the samples use the site's own origin. `siteOrigin` is
 * `undefined` before hydration.
 */
export function resolveDeveloperApiOrigin(
  configured: string | undefined,
  siteOrigin: string | undefined
): DeveloperApiOrigin {
  const fromConfig = httpOrigin(configured);
  if (fromConfig) return { status: 'ready', origin: fromConfig };
  if (siteOrigin === undefined) return { status: 'pending' };
  const fromSite = httpOrigin(siteOrigin);
  return fromSite ? { status: 'ready', origin: fromSite } : { status: 'unavailable' };
}

const subscribeNever = () => () => {};
const readSiteOrigin = () => window.location.origin;
const readNoSiteOrigin = () => undefined;

/** The origin the developer code samples print; see {@link DeveloperApiOrigin}. */
export function useDeveloperApiOrigin(): DeveloperApiOrigin {
  const siteOrigin = useSyncExternalStore(subscribeNever, readSiteOrigin, readNoSiteOrigin);
  return resolveDeveloperApiOrigin(env.NEXT_PUBLIC_DEVELOPER_API_ORIGIN, siteOrigin);
}

/** True when an operator should be told to configure the API origin: development only. */
export function showsDeveloperApiOriginHint(): boolean {
  return env.NODE_ENV === 'development' && !env.NEXT_PUBLIC_DEVELOPER_API_ORIGIN;
}
