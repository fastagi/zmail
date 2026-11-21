import { describe, expect, it } from 'vitest';

import { authConfig } from '@/config/auth';
import { resolveReturnUrl, withReturnUrl } from '@/features/auth/utils/return-url';

describe('return URL between the sign-in and sign-up pages', () => {
  it('keeps a same-origin page the visitor was sent from', () => {
    expect(withReturnUrl('/register', '/console/organizations?invitation=oinv_a%2Bb')).toBe(
      '/register?returnUrl=%2Fconsole%2Forganizations%3Finvitation%3Doinv_a%252Bb'
    );
  });

  it('drops a missing or foreign return URL', () => {
    expect(withReturnUrl('/login', null)).toBe('/login');
    expect(withReturnUrl('/login', 'https://evil.example/x')).toBe('/login');
    expect(withReturnUrl('/login', '//evil.example')).toBe('/login');
  });

  it('still resolves foreign targets to the console', () => {
    expect(resolveReturnUrl('https://evil.example')).toBe(authConfig.routes.afterLogin);
  });
});
