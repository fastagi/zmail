'use client';

import { Circle } from 'lucide-react';

import type { MailDomainCapability } from '@/features/mailbox/types';
import { useT } from '@/i18n';

/**
 * What to try once a domain verifies. It only suggests: Zmail sends nothing by itself, and a
 * send-only domain has nothing to receive, so only the outbound step applies there.
 */
export function MailDomainTestChecklist({
  domain,
  capability,
}: {
  domain: string;
  capability: MailDomainCapability;
}) {
  const t = useT('mailbox');
  const name = { domain };
  const receives = capability === 'send_receive';
  const steps = receives
    ? [
        { key: 'same', text: t('domain.testSameDomain', name) },
        { key: 'outbound', text: t('domain.testOutbound', name) },
        { key: 'inbound', text: t('domain.testInbound', name) },
        { key: 'reply', text: t('domain.testReply') },
      ]
    : [{ key: 'outbound', text: t('domain.testOutbound', name) }];

  return (
    <div className="space-y-2 rounded-md border p-3">
      <div className="space-y-0.5">
        <h4 className="text-sm font-semibold">{t('domain.testTitle')}</h4>
        <p className="text-sm text-muted-foreground">
          {receives ? t('domain.testDescription') : t('domain.testSendOnly')}
        </p>
      </div>
      <ul aria-label={t('domain.testTitle')} className="space-y-1.5">
        {steps.map(step => (
          <li key={step.key} data-test-step={step.key} className="flex gap-2 text-sm">
            <Circle className="mt-1 size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span>{step.text}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
