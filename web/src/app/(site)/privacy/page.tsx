import type { Metadata } from 'next';
import { LegalDocument } from '@/components/features/site/legal-document';
import { getT } from '@/i18n/server';

const SECTION_IDS = [
  'collect',
  'use',
  'google',
  'sharing',
  'cookies',
  'retention',
  'security',
  'rights',
  'children',
  'changes',
] as const;

export async function generateMetadata(): Promise<Metadata> {
  const t = await getT('legal');
  return { title: t('privacy.title'), description: t('privacy.description') };
}

/** Public Privacy Policy, linked from the site footer and the Google consent screen. */
export default async function PrivacyPage() {
  const t = await getT('legal');
  return (
    <LegalDocument
      title={t('privacy.title')}
      intro={t('privacy.intro')}
      sections={SECTION_IDS.map((id) => ({
        id,
        heading: t(`privacy.sections.${id}.heading`),
        body: t(`privacy.sections.${id}.body`),
      }))}
    />
  );
}
