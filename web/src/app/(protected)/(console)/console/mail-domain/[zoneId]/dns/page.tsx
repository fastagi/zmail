import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { MailDnsRecordsPage } from '@/features/mailbox/components/mail-dns-records';

interface MailZoneDnsPageProps {
  params: Promise<{ zoneId: string }>;
}

const zoneIdPattern = /^[1-9]\d{0,15}$/u;

export default async function MailZoneDnsPage({ params }: MailZoneDnsPageProps) {
  if (!isWebFeatureEnabled('mailbox')) notFound();
  const { zoneId } = await params;
  const id = Number(zoneId);
  if (!zoneIdPattern.test(zoneId) || !Number.isSafeInteger(id)) notFound();

  return (
    <div className="mx-auto w-full max-w-5xl px-4 py-6 sm:px-6 lg:py-8">
      <MailDnsRecordsPage zoneId={id} />
    </div>
  );
}
