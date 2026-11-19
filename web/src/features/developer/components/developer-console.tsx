'use client';

import { Hammer, PlugZap } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { TransactionalTemplatesTab } from '@/features/developer/components/transactional-templates-tab';
import { useDeveloperTab } from '@/features/developer/hooks/use-developer-tab';
import { DEVELOPER_MODES, DEVELOPER_TABS, type DeveloperTab } from '@/features/developer/types';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
} from '@/features/mailbox/components/mail-organization';
import { EmptyState, ErrorAlert } from '@/features/mailbox/components/mail-shared';
import {
  useMailOrganization,
  type MailOrganization,
} from '@/features/mailbox/hooks/use-mail-organization';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/**
 * The developer console: transactional mail for the organization's own backend. The tab and the
 * key mode live in the URL. It shows only while the instance has transactional mail enabled.
 */
export function DeveloperConsole() {
  const t = useT('developer');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader title={t('title')} description={t('description')} scope={scope} />
      <MailOrganizationBoundary scope={scope}>
        {organization => <DeveloperWorkspace organization={organization} />}
      </MailOrganizationBoundary>
    </div>
  );
}

function DeveloperWorkspace({ organization }: { organization: MailOrganization }) {
  const t = useT('developer');
  const tm = useT('mailbox');
  const domains = useMailboxDomains(organization.id);

  if (domains.isPending) {
    return (
      <div className="space-y-4" aria-hidden="true">
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-72 w-full" />
      </div>
    );
  }
  if (domains.error || !domains.data) {
    if (isOrganizationLost(domains.error)) return <OrganizationLostState />;
    return (
      <ErrorAlert
        message={tm(resolveMailboxErrorKey(domains.error))}
        retryLabel={tm('actions.retry')}
        onRetry={() => void domains.refetch()}
      />
    );
  }
  // An API that does not report the flag yet has transactional mail off.
  if (!domains.data.some(domain => domain.transactional_enabled === true)) {
    return (
      <EmptyState
        icon={<PlugZap className="size-5" aria-hidden="true" />}
        title={t('disabled.title')}
        description={t('disabled.description')}
      />
    );
  }
  return <DeveloperTabs organization={organization} />;
}

function DeveloperTabs({ organization }: { organization: MailOrganization }) {
  const t = useT('developer');
  const { tab, mode, setTab, setMode } = useDeveloperTab();

  return (
    <Tabs value={tab} onValueChange={value => setTab(value as DeveloperTab)} className="gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="max-w-full overflow-x-auto">
          <TabsList>
            {DEVELOPER_TABS.map(item => (
              <TabsTrigger key={item} value={item}>
                {t(`tabs.${item}`)}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
        <div className="flex items-center gap-2">
          <div
            role="group"
            aria-label={t('mode.label')}
            className="inline-flex rounded-md border p-0.5"
          >
            {DEVELOPER_MODES.map(item => (
              <Button
                key={item}
                type="button"
                size="sm"
                variant={mode === item ? 'secondary' : 'ghost'}
                aria-pressed={mode === item}
                onClick={() => setMode(item)}
              >
                {t(`mode.${item}`)}
              </Button>
            ))}
          </div>
          <span className="hidden text-xs text-muted-foreground md:inline">
            {t(mode === 'live' ? 'mode.liveHint' : 'mode.testHint')}
          </span>
        </div>
      </div>
      {DEVELOPER_TABS.map(item => (
        <TabsContent key={item} value={item}>
          {item === 'templates' ? (
            <TransactionalTemplatesTab organization={organization} />
          ) : (
            <EmptyState
              icon={<Hammer className="size-5" aria-hidden="true" />}
              title={t('placeholder.title')}
              description={t('placeholder.description')}
            />
          )}
        </TabsContent>
      ))}
    </Tabs>
  );
}
