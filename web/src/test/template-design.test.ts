import { describe, expect, it } from 'vitest';

import {
  emptyDesign,
  inlineHtml,
  inlineText,
  readDesign,
  renderDesignHtml,
  renderDesignText,
  safeImageSource,
  safeUrl,
  type DesignBlock,
} from '@/features/mailbox/utils/template-design';
import { renderTemplate, templateVariables } from '@/features/mailbox/utils/template-render';

const accent = '#2563eb';

function design(blocks: DesignBlock[]) {
  return emptyDesign(blocks);
}

describe('visual template design', () => {
  it('escapes text and keeps placeholders for the sender to fill', () => {
    const html = inlineHtml('<b>Hi</b> {{first_name|there}} & "you"', accent);
    expect(html).toBe('&lt;b&gt;Hi&lt;/b&gt; {{first_name|there}} &amp; &quot;you&quot;');
  });

  it('turns bold, italic, and safe links into HTML and refuses unsafe links', () => {
    expect(inlineHtml('**New** and *fast*', accent)).toBe('<strong>New</strong> and <em>fast</em>');
    expect(inlineHtml('[Docs](https://example.com/a?b=1&c=2)', accent)).toBe(
      `<a href="https://example.com/a?b=1&amp;c=2" style="color:${accent};text-decoration:underline;">Docs</a>`
    );
    expect(inlineHtml('[Go]({{action_url}})', accent)).toContain('href="{{action_url}}"');
    expect(inlineHtml('[Bad](javascript:alert(1))', accent)).not.toContain('<a');
    expect(inlineText('Read [Docs](https://example.com) **now**')).toBe(
      'Read Docs (https://example.com) now'
    );
  });

  it('accepts only web, mail, and placeholder links, and https or placeholder images', () => {
    for (const url of [
      'https://a.example',
      'http://a.example/x',
      'mailto:a@b.example',
      '{{action_url}}',
    ]) {
      expect(safeUrl(url), url).toBe(true);
    }
    for (const url of [
      'javascript:alert(1)',
      'data:text/html,x',
      'https://',
      'example.com',
      'https://a.example/"x',
    ]) {
      expect(safeUrl(url), url).toBe(false);
    }
    expect(safeImageSource('https://cdn.example/logo.png')).toBe(true);
    expect(safeImageSource('http://cdn.example/logo.png')).toBe(false);
    expect(safeImageSource('{{logo_url}}')).toBe(true);
  });

  it('renders every block to an email-safe layout and a plain-text version', () => {
    const product = design([
      { id: 'h', type: 'heading', text: 'November update', size: 'large', align: 'left' },
      {
        id: 't',
        type: 'text',
        text: 'Hi {{first_name|there}},\n\nThree **new** features.',
        align: 'left',
      },
      {
        id: 'i',
        type: 'image',
        src: 'https://cdn.example/hero.png',
        alt: 'Hero',
        href: 'https://example.com',
        width: 75,
        align: 'center',
      },
      { id: 'b', type: 'button', label: 'Read more', url: '{{action_url}}', align: 'center' },
      { id: 'd', type: 'divider' },
      { id: 's', type: 'spacer', height: 16 },
      {
        id: 'f',
        type: 'footer',
        text: 'You follow [Acme](https://acme.example).',
        align: 'center',
      },
    ]);
    const html = renderDesignHtml(product);
    expect(html).toMatch(/^<!doctype html>/u);
    expect(html).toContain('role="presentation"');
    expect(html).toContain('<h1 style=');
    expect(html).toContain('<p style="margin:0 0 14px;');
    expect(html).toContain('Hi {{first_name|there}},</p>');
    expect(html).toContain('<strong>new</strong>');
    expect(html).toContain('src="https://cdn.example/hero.png" alt="Hero" width="402"');
    expect(html).toContain('href="{{action_url}}"');
    expect(html).not.toMatch(/<script|<style|class=/u);

    expect(renderDesignText(product)).toBe(
      [
        'November update',
        'Hi {{first_name|there}},\n\nThree new features.',
        'Hero: https://example.com',
        'Read more: {{action_url}}',
        '---',
        'You follow Acme (https://acme.example).',
      ].join('\n\n')
    );

    // The rendered bodies are valid template source, and the fields are found.
    const source = { subject: 'News', html, text: renderDesignText(product) };
    expect(templateVariables(source)).toEqual(['action_url', 'first_name']);
    expect(renderTemplate(source, { action_url: 'https://x.example' })?.html).toContain(
      'href="https://x.example"'
    );
  });

  it('leaves out buttons and images that are not ready', () => {
    const html = renderDesignHtml(
      design([
        { id: 'b', type: 'button', label: '', url: 'https://a.example', align: 'left' },
        { id: 'c', type: 'button', label: 'Go', url: 'javascript:x', align: 'left' },
        {
          id: 'i',
          type: 'image',
          src: 'http://insecure.example/a.png',
          alt: '',
          href: '',
          width: 100,
          align: 'left',
        },
      ])
    );
    expect(html).not.toContain('<a ');
    expect(html).not.toContain('<img');
  });

  it('reads stored designs back and rejects foreign documents', () => {
    const original = design([
      { id: 'h', type: 'heading', text: 'Hi', size: 'medium', align: 'center' },
    ]);
    expect(readDesign(JSON.parse(JSON.stringify(original)))).toEqual(original);
    expect(readDesign(null)).toBeNull();
    expect(readDesign({ version: 2, blocks: [] })).toBeNull();
    expect(readDesign({ version: 1, blocks: [{ id: 'x', type: 'video' }] })).toBeNull();
    expect(
      readDesign({
        version: 1,
        blocks: [
          { id: 'a', type: 'divider' },
          { id: 'a', type: 'divider' },
        ],
      })
    ).toBeNull();
    // Unknown colors fall back to the defaults instead of reaching the markup.
    expect(
      readDesign({ version: 1, settings: { accent: 'red;background:url(x)' }, blocks: [] })
        ?.settings.accent
    ).toBe('#2563eb');
  });
});
