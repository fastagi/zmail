import { getFormatter } from 'next-intl/server';
import { getT } from '@/i18n/server';

/** Mailbox that answers privacy and terms questions; shown on both legal pages. */
export const LEGAL_CONTACT_EMAIL = 'support@zmail.im';

/** Date the current Privacy Policy and Terms of Service took effect. */
export const LEGAL_EFFECTIVE_DATE = new Date('2026-11-23T00:00:00Z');

type LegalBlock = { kind: 'paragraph'; text: string } | { kind: 'list'; items: string[] };

/**
 * Split a section body into blocks: blank lines separate blocks, and a block whose lines all start
 * with "- " is a bulleted list.
 */
export function parseLegalBody(body: string): LegalBlock[] {
  return body
    .split(/\n{2,}/)
    .map((block) => block.split('\n').map((line) => line.trim()).filter(Boolean))
    .filter((lines) => lines.length > 0)
    .flatMap((lines): LegalBlock[] => {
      const listItems = lines.filter((line) => line.startsWith('- '));
      if (listItems.length === lines.length) {
        return [{ kind: 'list', items: listItems.map((line) => line.slice(2)) }];
      }
      const intro = lines.filter((line) => !line.startsWith('- '));
      const blocks: LegalBlock[] = [{ kind: 'paragraph', text: intro.join(' ') }];
      if (listItems.length > 0) {
        blocks.push({ kind: 'list', items: listItems.map((line) => line.slice(2)) });
      }
      return blocks;
    });
}

// Stands in for the address while translating so the address itself can render as a link.
const EMAIL_MARKER = '\u0000email\u0000';

function ContactLine({ message }: { message: string }) {
  const [before, after = ''] = message.split(EMAIL_MARKER);
  return (
    <p>
      {before}
      <a href={`mailto:${LEGAL_CONTACT_EMAIL}`} className="font-medium text-primary hover:underline">
        {LEGAL_CONTACT_EMAIL}
      </a>
      {after}
    </p>
  );
}

interface LegalDocumentProps {
  title: string;
  intro: string;
  sections: ReadonlyArray<{ id: string; heading: string; body: string }>;
}

/** Long-form legal page: title, effective date, sections, and the contact line. */
export async function LegalDocument({ title, intro, sections }: LegalDocumentProps) {
  const t = await getT('legal');
  const format = await getFormatter();
  const date = format.dateTime(LEGAL_EFFECTIVE_DATE, { dateStyle: 'long', timeZone: 'UTC' });

  return (
    <article className="container max-w-3xl py-12 md:py-16">
      <header className="border-b border-border/60 pb-8">
        <h1 className="text-3xl font-bold tracking-tight md:text-4xl">{title}</h1>
        <p className="mt-3 text-sm text-text-muted">{t('updated', { date })}</p>
        <p className="mt-6 leading-relaxed text-text-subtle">{intro}</p>
      </header>

      <div className="divide-y divide-border/40">
        {sections.map((section) => (
          <section key={section.id} id={section.id} className="scroll-mt-24 py-8">
            <h2 className="text-xl font-semibold tracking-tight">{section.heading}</h2>
            <div className="mt-4 space-y-4 leading-relaxed text-text-subtle">
              {parseLegalBody(section.body).map((block, index) =>
                block.kind === 'list' ? (
                  <ul key={index} className="list-disc space-y-2 pl-5 marker:text-text-muted">
                    {block.items.map((item) => (
                      <li key={item}>{item}</li>
                    ))}
                  </ul>
                ) : (
                  <p key={index}>{block.text}</p>
                ),
              )}
            </div>
          </section>
        ))}
      </div>

      <footer className="border-t border-border/60 pt-8 text-sm text-text-muted">
        <ContactLine message={t('contact', { email: EMAIL_MARKER })} />
      </footer>
    </article>
  );
}
