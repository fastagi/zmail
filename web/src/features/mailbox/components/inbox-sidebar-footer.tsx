'use client';

import Link from 'next/link';
import { ROUTES } from '@/constants/routes';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { SendQuotaCard } from '@/features/mailbox/components/send-quota-card';
import { mailOrganizationHref } from '@/features/mailbox/hooks/use-mail-organization';
import { useIsPersonalMode } from '@/features/mailbox/stores/workspace-mode';
import { useT } from '@/i18n';

/** The other mail pages, linked from the sidebar footer. */
const footerLinks = [
  ['mail', ROUTES.CONSOLE.EMAILS],
  ['broadcasts', ROUTES.CONSOLE.MAIL_BROADCASTS],
  ['contacts', ROUTES.CONSOLE.MAIL_CONTACTS],
  ['developers', ROUTES.CONSOLE.DEVELOPERS],
  ['manage', ROUTES.CONSOLE.MAIL_DOMAIN],
] as const;

export default function InboxSidebarFooter({
  canManage,
  sentWaiting,
  onShortcuts,
}: {
  canManage: boolean;
  sentWaiting: number;
  onShortcuts: () => void;
}) {
  const organizationId = useMailOrganizationId();
  const isPersonalMode = useIsPersonalMode();
  const t = useT('mailbox');
  return (
    <>
      <SendQuotaCard canManage={canManage} sentWaiting={sentWaiting} />
      <nav
        aria-label={t('inbox.footer.label')}
        className="flex flex-wrap gap-x-2.5 gap-y-1 px-2 pt-2.5 text-text-muted"
      >
        {!isPersonalMode
          ? footerLinks.map(([key, path]) => (
              <Link
                key={key}
                href={mailOrganizationHref(path, organizationId)}
                className="rounded-sm transition-colors focus-ring first:mr-auto hover:text-text-main motion-reduce:transition-none"
              >
                {t(`inbox.footer.${key}`)}
              </Link>
            ))
          : null}
        <button
          type="button"
          className="rounded-sm transition-colors focus-ring hover:text-text-main motion-reduce:transition-none"
          onClick={onShortcuts}
        >
          {t('inbox.footer.shortcuts')}
        </button>
      </nav>
    </>
  );
}
