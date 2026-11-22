import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { converter, parse, wcagContrast, type Color } from 'culori';
import { describe, expect, it } from 'vitest';

// Issue #116: inbox timestamps and nav unread counts use `--text-faint` at 12px, so it must meet
// WCAG AA 4.5:1 on every surface it sits on in both themes, including the inbox's selected row,
// whose translucent blue the theme contrast guard cannot resolve.
const themes = join(process.cwd(), 'src/themes');
const toRgb = converter('rgb');

/** Every `--name: value` of a stylesheet, later declarations winning. */
function declarations(file: string): Map<string, string> {
  const tokens = new Map<string, string>();
  const source = readFileSync(join(themes, file), 'utf8').replace(/\/\*[\s\S]*?\*\//gu, '');
  for (const match of source.matchAll(/--([\w-]+):\s*([^;]+);/gu)) {
    tokens.set(match[1], match[2].trim());
  }
  return tokens;
}

function themeColor(mode: 'light' | 'dark', token: string): Color {
  const tokens = new Map([...declarations('primitives.css'), ...declarations(`${mode}.css`)]);
  let value = tokens.get(token);
  for (let depth = 0; value?.startsWith('var(') && depth < 10; depth += 1) {
    value = tokens.get(value.slice('var(--'.length, -1));
  }
  const color = value ? parse(value) : undefined;
  if (!color) throw new Error(`--${token} does not resolve to a color in ${mode}`);
  return color;
}

/** `top` at `alpha` over the opaque `bottom`, as the browser composites it. */
function over(top: string, alpha: number, bottom: Color): Color {
  const a = toRgb(parse(top));
  const b = toRgb(bottom);
  if (!a || !b) throw new Error('unparsable color');
  const mix = (x: number, y: number) => x * alpha + y * (1 - alpha);
  return { mode: 'rgb', r: mix(a.r, b.r), g: mix(a.g, b.g), b: mix(a.b, b.b) };
}

describe('faint text contrast (#116)', () => {
  for (const mode of ['light', 'dark'] as const) {
    it(`meets 4.5:1 on every surface and selected row in ${mode} mode`, () => {
      const faint = themeColor(mode, 'text-faint');
      const surface = themeColor(mode, 'bg-surface');
      const backgrounds: Record<string, Color> = {
        canvas: themeColor(mode, 'bg-canvas'),
        surface,
        rowSelected: themeColor(mode, 'row-selected'),
        navSelected: themeColor(mode, 'nav-selected'),
        // The selected inbox row: `bg-blue-50`, or `dark:bg-blue-950/65`, over the list surface.
        inboxSelected:
          mode === 'dark'
            ? over('oklch(28.2% 0.091 267.935)', 0.65, surface)
            : over('oklch(97% 0.014 254.604)', 1, surface),
      };
      for (const [name, background] of Object.entries(backgrounds)) {
        expect(wcagContrast(faint, background), name).toBeGreaterThanOrEqual(4.5);
      }
    });
  }
});
