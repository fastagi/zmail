'use client';

import { useId } from 'react';
import Link from 'next/link';

import { ROUTES } from '@/constants/routes';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { mailOrganizationHref } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailboxDomains, useMailStats } from '@/features/mailbox/hooks/use-mailbox';
import { useT } from '@/i18n';

/**
 * Today's sending, degraded to the numbers the caller may already see (D24): what the mailboxes
 * they can read sent and failed in the UTC day, and for owners and admins the organization's
 * rolling 24-hour limit. While mail waits for that limit (`sentWaiting > 0`), everyone sees that
 * it is queued, with a link to Sent, never how much. No progress bar and no breakdown: the
 * organization's usage is not exposed to the browser.
 */
export function SendQuotaCard({
  canManage,
  sentWaiting,
}: {
  canManage: boolean;
  /** `sent_waiting` of the inbox counts; 0 until they load. */
  sentWaiting: number;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const stats = useMailStats(organizationId, 1);
  const domains = useMailboxDomains(organizationId);
  const titleId = useId();
  const today = stats.data?.at(-1);
  // The limit is the organization's; every domain entry carries the same value.
  const limit = domains.data?.[0]?.daily_send_limit;

  return (
    <section
      aria-labelledby={titleId}
      className="mx-1 rounded-[10px] border border-border-main bg-bg-surface px-3 py-2.5 text-xs leading-[1.6] text-text-muted"
    >
      <h2 id={titleId} className="font-normal text-text-subtle">
        {t('quota.title')}
      </h2>
      {today ? <p>{t('quota.own', { sent: today.sent, failed: today.failed })}</p> : null}
      {canManage && limit !== undefined ? <p>{t('quota.limit', { limit })}</p> : null}
      {sentWaiting > 0 ? (
        <Link
          href={`${mailOrganizationHref(ROUTES.CONSOLE.MAILBOXES, organizationId)}&view=sent`}
          className="text-warning underline-offset-2 focus-ring hover:underline"
        >
          {t('quota.queued')}
        </Link>
      ) : null}
    </section>
  );
}
