import type { Metadata } from 'next';
import { LegalDocument } from '@/components/features/site/legal-document';
import { getT } from '@/i18n/server';

const SECTION_IDS = [
  'account',
  'use',
  'content',
  'domains',
  'availability',
  'fees',
  'termination',
  'disclaimer',
  'changes',
] as const;

export async function generateMetadata(): Promise<Metadata> {
  const t = await getT('legal');
  return { title: t('terms.title'), description: t('terms.description') };
}

/** Public Terms of Service, linked from the site footer and the Google consent screen. */
export default async function TermsPage() {
  const t = await getT('legal');
  return (
    <LegalDocument
      title={t('terms.title')}
      intro={t('terms.intro')}
      sections={SECTION_IDS.map((id) => ({
        id,
        heading: t(`terms.sections.${id}.heading`),
        body: t(`terms.sections.${id}.body`),
      }))}
    />
  );
}
