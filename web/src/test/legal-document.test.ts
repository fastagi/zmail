import { describe, expect, it } from 'vitest';
import { parseLegalBody } from '@/components/features/site/legal-document';
import enUS from '@/i18n/modules/legal/en-US';
import zhHans from '@/i18n/modules/legal/zh-Hans';

describe('parseLegalBody', () => {
  it('splits paragraphs on blank lines', () => {
    expect(parseLegalBody('First.\n\nSecond.')).toEqual([
      { kind: 'paragraph', text: 'First.' },
      { kind: 'paragraph', text: 'Second.' },
    ]);
  });

  it('turns "- " lines into a list', () => {
    expect(parseLegalBody('- one\n- two')).toEqual([{ kind: 'list', items: ['one', 'two'] }]);
  });

  it('keeps a lead-in line before its list', () => {
    expect(parseLegalBody('You may not:\n- spam\n- phish\n\nAfter.')).toEqual([
      { kind: 'paragraph', text: 'You may not:' },
      { kind: 'list', items: ['spam', 'phish'] },
      { kind: 'paragraph', text: 'After.' },
    ]);
  });
});

describe('legal copy', () => {
  const documents = ['privacy', 'terms'] as const;

  it.each(documents)('%s renders the same block shape in both locales', (document) => {
    const english = enUS[document].sections;
    const chinese = zhHans[document].sections;
    for (const id of Object.keys(english) as Array<keyof typeof english>) {
      const shape = (body: string) =>
        parseLegalBody(body).map((block) => (block.kind === 'list' ? `list:${block.items.length}` : 'p'));
      expect(shape(chinese[id].body), `${document}.${String(id)}`).toEqual(shape(english[id].body));
    }
  });

  it('the privacy policy discloses Google Limited Use and no AI/ML training', () => {
    expect(enUS.privacy.sections.google.heading).toContain('Limited Use');
    expect(enUS.privacy.sections.google.body).toContain('Google API Services User Data Policy');
    expect(enUS.privacy.sections.google.body).toContain('Limited Use');
    expect(enUS.privacy.sections.google.body).toContain('AI/ML');
    expect(zhHans.privacy.sections.google.heading).toContain('Limited Use');
    expect(zhHans.privacy.sections.google.body).toContain('Limited Use');
    expect(zhHans.privacy.sections.google.body).toContain('AI/ML');
  });
});
