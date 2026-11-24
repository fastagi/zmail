'use client';

import { lazy, Suspense, type RefObject } from 'react';

import { ReaderSkeleton } from '@/features/mailbox/components/inbox/inbox-dialogs';
import type {
  InboxPaneActions,
  InboxReaderNavigation,
} from '@/features/mailbox/components/inbox-reading-pane';
import { MailTimeZoneContext } from '@/features/mailbox/components/mail-time';
import type { InboxItem, MailMessage, Mailbox } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

// The reading pane, with the thread and message readers, loads when a row first opens.
const InboxReadingPane = lazy(async () => ({
  default: (await import('@/features/mailbox/components/inbox-reading-pane')).InboxReadingPane,
}));

export function InboxReadingSection({
  openItem,
  openMailbox,
  timeZone,
  externalProvider,
  roleName,
  navigation,
  onUnavailable,
  actionsRef,
  triageEnabled,
  triageMenuOpen,
  onTriageMenuOpenChange,
  onUseTriageDraft,
}: {
  openItem: InboxItem | null;
  openMailbox: Mailbox | null;
  timeZone: string;
  externalProvider: string | null;
  roleName: string | null;
  navigation: InboxReaderNavigation;
  onUnavailable: () => void;
  actionsRef: RefObject<InboxPaneActions | null>;
  triageEnabled: boolean;
  triageMenuOpen: boolean;
  onTriageMenuOpenChange: (open: boolean) => void;
  onUseTriageDraft: (message: MailMessage, body: string) => void;
}) {
  const t = useT('mailbox');
  return (
    <section
      aria-label={t('inbox.readingPane')}
      data-reading-pane=""
      className={cn(
        'my-2.5 mr-2.5 flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-[14px] border border-border-main bg-bg-surface max-md:ml-2.5',
        'lg:rounded-l-none lg:border-l-0',
        !openItem && 'max-lg:hidden'
      )}
    >
      {openItem ? (
        <Suspense fallback={<ReaderSkeleton />}>
          {/* The reader shows times in the list's zone, so a row and its mail agree. */}
          <MailTimeZoneContext value={timeZone}>
            <InboxReadingPane
              key={openItem.id}
              item={openItem}
              access={openMailbox?.access ?? null}
              shared={openMailbox?.type === 'shared'}
              externalProvider={externalProvider}
              roleName={roleName}
              navigation={navigation}
              onUnavailable={onUnavailable}
              actionsRef={actionsRef}
              triage={
                triageEnabled
                  ? {
                      menuOpen: triageMenuOpen,
                      onMenuOpenChange: onTriageMenuOpenChange,
                      onUseDraft: onUseTriageDraft,
                    }
                  : null
              }
            />
          </MailTimeZoneContext>
        </Suspense>
      ) : (
        <div className="flex h-full min-h-48 flex-col items-center justify-center gap-5 px-6 text-center">
          <span
            aria-hidden="true"
            className="h-28 w-40 bg-[url('/inbox-letters.svg')] bg-contain bg-center bg-no-repeat motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-2 motion-safe:duration-300 dark:brightness-75"
          />
          <p className="text-sm text-text-muted">{t('list.readerEmpty')}</p>
        </div>
      )}
    </section>
  );
}
