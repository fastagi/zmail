import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

import { describe, expect, it } from 'vitest';

const SOURCE_ROOT = join(__dirname, '..');

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return name === 'test' ? [] : sourceFiles(path);
    return path.endsWith('.tsx') ? [path] : [];
  });
}

describe('scrollable tab lists', () => {
  // TabsList centers its triggers. Once they overflow on a phone, a centered row clips its
  // first tabs on the left where scrolling cannot reach them, so a scrolling row must start left.
  it('start at the left edge whenever they scroll sideways', () => {
    const offenders = sourceFiles(SOURCE_ROOT).flatMap((file) =>
      [...readFileSync(file, 'utf8').matchAll(/<TabsList\b[^>]*className="([^"]*)"/g)]
        .filter(([, className]) => className.includes('overflow-x-auto') && !className.includes('justify-start'))
        .map(() => relative(SOURCE_ROOT, file)),
    );
    expect(offenders).toEqual([]);
  });
});
