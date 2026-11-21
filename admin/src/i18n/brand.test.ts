import { afterEach, describe, expect, it, vi } from 'vitest';

import html from '../../index.html?raw';
import { resources } from './resources';

// The product brand is Zmail; FastBuild names only the scaffold underneath (#75).
describe('Admin Console brand', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it('names the product in the document title and description', () => {
    expect(html).toContain('<title>Zmail Admin</title>');
    expect(html).not.toContain('FastBuild');
  });

  it('names the product in every locale', () => {
    for (const locale of Object.values(resources)) {
      expect(locale.translation.app.name).toBe('Zmail');
      expect(JSON.stringify(locale)).not.toContain('FastBuild');
    }
  });

  it('defaults the app name to Zmail', async () => {
    vi.stubEnv('VITE_APP_NAME', '');
    const { env } = await import('@/config/env');

    expect(env.APP_NAME).toBe('Zmail');
  });
});
