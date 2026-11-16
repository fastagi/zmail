import { describe, expect, it } from 'vitest';

import {
  parseTemplate,
  renderTemplate,
  templateVariables,
} from '@/features/mailbox/utils/template-render';

/** The same cases as the API's renderer tests, so the editor preview matches what is sent. */
describe('template renderer', () => {
  it('accepts placeholders and rejects typos', () => {
    for (const source of [
      '',
      'plain',
      'Hi {{name}}',
      'Hi {{ name }}!',
      '{{name|there}}',
      '{{ name | dear customer }}',
      '{{first_name}} {{last_name}}',
      'a {single} brace and }} alone',
      '{{name|}}',
    ]) {
      expect(parseTemplate(source), source).not.toBeNull();
    }
    for (const source of [
      '{{',
      'Hi {{name',
      '{{}}',
      '{{ }}',
      '{{1st}}',
      '{{first-name}}',
      '{{na me}}',
      '{{a{{b}}',
      '{{name\n}}',
      '{{_hidden}}',
      `{{${'a'.repeat(65)}}}`,
      `{{name|${'x'.repeat(501)}}}`,
    ]) {
      expect(parseTemplate(source), source).toBeNull();
    }
  });

  it('fills, escapes, and reports missing variables like the API', () => {
    const source = {
      subject: 'Hi {{ name }}, about {{topic}}',
      html: '<p>{{name|there}} &mdash; {{code}}</p>',
      text: 'Hello {{name|there}}, code {{code}}',
    };
    expect(templateVariables(source)).toEqual(['code', 'name', 'topic']);

    const rendered = renderTemplate(source, {
      name: 'Ann <script>"x"</script>',
      topic: 'line one\nline two',
      code: 'A&B',
    });
    expect(rendered).toEqual({
      subject: 'Hi Ann <script>"x"</script>, about line one line two',
      html: '<p>Ann &lt;script&gt;&#34;x&#34;&lt;/script&gt; &mdash; A&amp;B</p>',
      text: 'Hello Ann <script>"x"</script>, code A&B',
      variables: ['code', 'name', 'topic'],
      missing: [],
    });

    const fallback = renderTemplate(source, { name: '' });
    expect(fallback?.html).toBe('<p>there &mdash; </p>');
    expect(fallback?.missing).toEqual(['code', 'name', 'topic']);
    expect(renderTemplate({ ...source, text: 'Hi {{name' }, {})).toBeNull();
  });
});
