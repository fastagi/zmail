import { afterEach, describe, expect, it, vi } from 'vitest';

const env = vi.hoisted(() => ({
  NODE_ENV: 'production' as 'development' | 'production' | 'test',
  NEXT_PUBLIC_DEVELOPER_API_ORIGIN: undefined as string | undefined,
}));
vi.mock('@/config/env', () => ({ env }));

import {
  resolveDeveloperApiOrigin,
  showsDeveloperApiOriginHint,
} from '@/hooks/use-developer-api-origin';

describe('developer API origin (#144)', () => {
  afterEach(() => {
    env.NODE_ENV = 'production';
    env.NEXT_PUBLIC_DEVELOPER_API_ORIGIN = undefined;
  });

  it('prefers the configured origin, trimmed', () => {
    expect(resolveDeveloperApiOrigin('https://zmail.im/', 'https://app.zmail.im')).toEqual({
      status: 'ready',
      origin: 'https://zmail.im',
    });
  });

  it('falls back to the site origin when nothing is configured', () => {
    expect(resolveDeveloperApiOrigin(undefined, 'https://zmail.im')).toEqual({
      status: 'ready',
      origin: 'https://zmail.im',
    });
  });

  it('waits for hydration before using the site origin', () => {
    expect(resolveDeveloperApiOrigin(undefined, undefined)).toEqual({ status: 'pending' });
  });

  it('is unavailable when the site has no usable http(s) origin', () => {
    // An opaque origin (file:, sandboxed frames) reads as the string "null".
    expect(resolveDeveloperApiOrigin(undefined, 'null')).toEqual({ status: 'unavailable' });
    expect(resolveDeveloperApiOrigin('', '')).toEqual({ status: 'unavailable' });
  });

  it('shows the operator hint only in development without a configured origin', () => {
    expect(showsDeveloperApiOriginHint()).toBe(false);
    env.NODE_ENV = 'test';
    expect(showsDeveloperApiOriginHint()).toBe(false);
    env.NODE_ENV = 'development';
    expect(showsDeveloperApiOriginHint()).toBe(true);
    env.NEXT_PUBLIC_DEVELOPER_API_ORIGIN = 'https://zmail.im';
    expect(showsDeveloperApiOriginHint()).toBe(false);
  });
});
