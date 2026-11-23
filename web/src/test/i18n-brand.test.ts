import { describe, expect, it } from 'vitest';

const localeModules = import.meta.glob<unknown>('../i18n/modules/*/{en-US,zh-Hans}.ts', {
  eager: true,
  import: 'default',
});

// The product brand is Zmail; FastBuild names only the scaffold underneath and never reaches users.
describe('user-visible copy', () => {
  it('covers every locale module', () => {
    expect(Object.keys(localeModules).length).toBeGreaterThan(20);
  });

  it.each(Object.entries(localeModules))(
    '%s never shows the scaffold name or scaffold examples',
    (_path, messages) => {
      const copy = JSON.stringify(messages);
      expect(copy).not.toMatch(/fastbuild/iu);
      expect(copy).not.toMatch(/YOUR_API_HOST|models:(?:read|invoke)/u);
    }
  );
});
