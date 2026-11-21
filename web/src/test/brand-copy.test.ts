import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const sourceRoot = resolve(process.cwd(), 'src');

function filesUnder(directory: string): string[] {
  return readdirSync(directory).flatMap(name => {
    const path = join(directory, name);
    return statSync(path).isDirectory() ? filesUnder(path) : [path];
  });
}

// The product brand is Zmail; FastBuild names only the scaffold underneath (#75).
describe('user-facing brand', () => {
  it('keeps the scaffold name out of every locale message', () => {
    const offenders = filesUnder(join(sourceRoot, 'i18n/modules'))
      .filter(path => readFileSync(path, 'utf8').includes('FastBuild'))
      .map(path => relative(sourceRoot, path));

    expect(offenders).toEqual([]);
  });

  it('keeps the scaffold name out of the mock data users see in mock mode', () => {
    const offenders = [
      'features/notification/server/mock-notification-store.ts',
      'features/organization/server/mock-organization-store.ts',
      'features/setting/server/mock-setting-store.ts',
    ].filter(path => /fastbuild/iu.test(readFileSync(join(sourceRoot, path), 'utf8')));

    expect(offenders).toEqual([]);
  });
});
