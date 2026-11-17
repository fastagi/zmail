import type { MailTemplateInput } from '@/features/mailbox/types';
import { emptyDesign, type TemplateDesign } from '@/features/mailbox/utils/template-design';

/**
 * Starting points for the template editor. The layout and placeholders live here; the wording
 * comes from the message catalog, where `[[name|fallback]]` stands for a `{{name|fallback}}`
 * placeholder because ICU reserves single braces.
 */
export type TemplateStarter = 'blank' | 'welcome' | 'notification' | 'newsletter';

export const templateStarters: readonly TemplateStarter[] = [
  'blank',
  'welcome',
  'notification',
  'newsletter',
];

/** The catalog wording a starter needs, in the interface language. */
export interface StarterPhrases {
  welcomeSubject: string;
  welcomeHeading: string;
  welcomeBody: string;
  welcomeButton: string;
  welcomeFooter: string;
  greeting: string;
  notificationButton: string;
  newsletterKicker: string;
  newsletterButton: string;
  newsletterFooter: string;
  sampleFirstName: string;
  sampleCompany: string;
  sampleTitle: string;
  sampleMessage: string;
  sampleIssueTitle: string;
  sampleIntro: string;
}

export type StarterContent = Omit<MailTemplateInput, 'name'>;

/** `[[name|fallback]]` to `{{name|fallback}}`. */
function placeholders(phrase: string): string {
  return phrase.replace(/\[\[([^\]]*)\]\]/gu, '{{$1}}');
}

/** Catalog text placed in HTML: escaped, then placeholders restored. */
function html(phrase: string): string {
  const escaped = phrase
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
  return placeholders(escaped);
}

function layout(body: string): string {
  return `<!doctype html>
<html>
  <body style="margin:0;padding:24px;background:#f4f4f5;font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#18181b;">
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0">
      <tr>
        <td align="center">
          <table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:8px;padding:32px;">
            <tr>
              <td style="font-size:15px;line-height:1.6;">
${body}
              </td>
            </tr>
          </table>
        </td>
      </tr>
    </table>
  </body>
</html>
`;
}

const heading = (text: string) =>
  `                <h1 style="font-size:22px;margin:0 0 16px;">${text}</h1>`;
const paragraph = (text: string) => `                <p>${text}</p>`;
const note = (text: string) =>
  `                <p style="color:#71717a;font-size:13px;">${text}</p>`;
const button = (label: string) =>
  `                <p style="margin:24px 0;"><a href="{{action_url}}" style="background:#18181b;color:#ffffff;padding:10px 18px;border-radius:6px;text-decoration:none;display:inline-block;">${label}</a></p>`;

/** The content a starter fills in; blank is empty. */
export function starterContent(starter: TemplateStarter, phrases: StarterPhrases): StarterContent {
  const p = phrases;
  switch (starter) {
    case 'blank':
      return { subject: '', html: '', text: '', sample_variables: {} };
    case 'welcome':
      return {
        subject: placeholders(p.welcomeSubject),
        html: layout(
          [
            heading(html(p.welcomeHeading)),
            paragraph(html(p.welcomeBody)),
            button(html(p.welcomeButton)),
            note(html(p.welcomeFooter)),
          ].join('\n')
        ),
        text: [
          placeholders(p.welcomeHeading),
          placeholders(p.welcomeBody),
          `${p.welcomeButton}: {{action_url}}`,
        ].join('\n\n'),
        sample_variables: {
          first_name: p.sampleFirstName,
          company: p.sampleCompany,
          action_url: 'https://example.com/start',
        },
      };
    case 'notification':
      return {
        subject: '{{title}}',
        html: layout(
          [
            heading('{{title}}'),
            paragraph(html(p.greeting)),
            paragraph('{{message}}'),
            button(html(p.notificationButton)),
          ].join('\n')
        ),
        text: [
          '{{title}}',
          placeholders(p.greeting),
          '{{message}}',
          `${p.notificationButton}: {{action_url}}`,
        ].join('\n\n'),
        sample_variables: {
          title: p.sampleTitle,
          first_name: p.sampleFirstName,
          message: p.sampleMessage,
          action_url: 'https://example.com/orders/1042',
        },
      };
    case 'newsletter':
      return {
        subject: '{{issue_title}}',
        html: layout(
          [
            note(html(p.newsletterKicker)),
            heading('{{issue_title}}'),
            paragraph(html(p.greeting)),
            paragraph('{{intro}}'),
            button(html(p.newsletterButton)),
            note(html(p.newsletterFooter)),
          ].join('\n')
        ),
        text: [
          '{{issue_title}}',
          placeholders(p.greeting),
          '{{intro}}',
          `${p.newsletterButton}: {{action_url}}`,
        ].join('\n\n'),
        sample_variables: {
          company: p.sampleCompany,
          issue_title: p.sampleIssueTitle,
          first_name: p.sampleFirstName,
          intro: p.sampleIntro,
          action_url: 'https://example.com/blog/november',
        },
      };
  }
}

export interface StarterDesign {
  subject: string;
  design: TemplateDesign;
  sample_variables: Record<string, string>;
}

/** The starter as a visual design; the same wording and placeholders as the HTML version. */
export function starterDesign(starter: TemplateStarter, phrases: StarterPhrases): StarterDesign {
  const p = phrases;
  switch (starter) {
    case 'blank':
      return { subject: '', design: emptyDesign(), sample_variables: {} };
    case 'welcome':
      return {
        subject: placeholders(p.welcomeSubject),
        design: emptyDesign([
          {
            id: 'heading',
            type: 'heading',
            text: placeholders(p.welcomeHeading),
            size: 'large',
            align: 'left',
          },
          { id: 'body', type: 'text', text: placeholders(p.welcomeBody), align: 'left' },
          {
            id: 'action',
            type: 'button',
            label: p.welcomeButton,
            url: '{{action_url}}',
            align: 'left',
          },
          { id: 'rule', type: 'divider' },
          { id: 'footer', type: 'footer', text: placeholders(p.welcomeFooter), align: 'left' },
        ]),
        sample_variables: {
          first_name: p.sampleFirstName,
          company: p.sampleCompany,
          action_url: 'https://example.com/start',
        },
      };
    case 'notification':
      return {
        subject: '{{title}}',
        design: emptyDesign([
          { id: 'heading', type: 'heading', text: '{{title}}', size: 'large', align: 'left' },
          {
            id: 'body',
            type: 'text',
            text: `${placeholders(p.greeting)}\n\n{{message}}`,
            align: 'left',
          },
          {
            id: 'action',
            type: 'button',
            label: p.notificationButton,
            url: '{{action_url}}',
            align: 'left',
          },
        ]),
        sample_variables: {
          title: p.sampleTitle,
          first_name: p.sampleFirstName,
          message: p.sampleMessage,
          action_url: 'https://example.com/orders/1042',
        },
      };
    case 'newsletter':
      return {
        subject: '{{issue_title}}',
        design: emptyDesign([
          { id: 'kicker', type: 'footer', text: placeholders(p.newsletterKicker), align: 'left' },
          { id: 'heading', type: 'heading', text: '{{issue_title}}', size: 'large', align: 'left' },
          {
            id: 'body',
            type: 'text',
            text: `${placeholders(p.greeting)}\n\n{{intro}}`,
            align: 'left',
          },
          {
            id: 'action',
            type: 'button',
            label: p.newsletterButton,
            url: '{{action_url}}',
            align: 'left',
          },
          { id: 'rule', type: 'divider' },
          { id: 'footer', type: 'footer', text: placeholders(p.newsletterFooter), align: 'center' },
        ]),
        sample_variables: {
          company: p.sampleCompany,
          issue_title: p.sampleIssueTitle,
          first_name: p.sampleFirstName,
          intro: p.sampleIntro,
          action_url: 'https://example.com/blog/november',
        },
      };
  }
}
