'use client';

import { useState } from 'react';
import { useSearchParams } from 'next/navigation';
import { AlertCircle } from 'lucide-react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { BroadcastDetail } from '@/features/mailbox/components/broadcasts/broadcast-detail';
import { BroadcastEditor } from '@/features/mailbox/components/broadcasts/broadcast-editor';
import { BroadcastList } from '@/features/mailbox/components/broadcasts/broadcast-list';
import { BackButton } from '@/features/mailbox/components/broadcasts/broadcast-shared';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { useMailBroadcast } from '@/features/mailbox/hooks/use-mail-broadcasts';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/**
 * Broadcasts console: drafts, sending, scheduling, progress, and per-recipient outcomes. The
 * worker sends in the background; running broadcasts refresh on their own.
 */
export function MailBroadcasts() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader
        title={t('broadcasts.title')}
        description={t('broadcasts.description')}
        scope={scope}
      />
      <MailOrganizationBoundary scope={scope}>
        {organization => <BroadcastWorkspace canManage={organization.canManage} />}
      </MailOrganizationBoundary>
    </div>
  );
}

type View = { kind: 'list' } | { kind: 'new' } | { kind: 'open'; broadcastId: number };

/** Query parameter a broadcast notice uses to open one broadcast. */
const OPEN_BROADCAST_PARAM = 'open';
const openBroadcastPattern = /^[1-9]\d{0,15}$/u;

/** Members draft broadcasts; only owners and admins start, stop, pause, and resume them. */
function BroadcastWorkspace({ canManage }: { canManage: boolean }) {
  const t = useT('mailbox');
  const domains = useMailboxDomains(useMailOrganizationId());
  // A notice links to one broadcast with `open`; the organization stays in `org`.
  const openParam = useSearchParams().get(OPEN_BROADCAST_PARAM) ?? '';
  const [view, setView] = useState<View>(() =>
    openBroadcastPattern.test(openParam)
      ? { kind: 'open', broadcastId: Number(openParam) }
      : { kind: 'list' }
  );
  const enabled = domains.data?.[0]?.broadcasts_enabled ?? true;
  const back = () => setView({ kind: 'list' });

  return (
    <div className="space-y-4">
      {!enabled ? (
        <Alert>
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle>{t('broadcasts.disabledTitle')}</AlertTitle>
          <AlertDescription>{t('broadcasts.disabledDescription')}</AlertDescription>
        </Alert>
      ) : null}
      {view.kind === 'new' ? (
        <BroadcastEditor
          broadcast={null}
          enabled={enabled}
          canManage={canManage}
          onBack={back}
          onSaved={broadcastId => setView({ kind: 'open', broadcastId })}
        />
      ) : view.kind === 'open' ? (
        <OpenBroadcast
          broadcastId={view.broadcastId}
          enabled={enabled}
          canManage={canManage}
          onBack={back}
        />
      ) : (
        <BroadcastList
          enabled={enabled}
          onCreate={() => setView({ kind: 'new' })}
          onOpen={broadcastId => setView({ kind: 'open', broadcastId })}
        />
      )}
    </div>
  );
}

function OpenBroadcast({
  broadcastId,
  enabled,
  canManage,
  onBack,
}: {
  broadcastId: number;
  enabled: boolean;
  canManage: boolean;
  onBack: () => void;
}) {
  const t = useT('mailbox');
  const broadcast = useMailBroadcast(useMailOrganizationId(), broadcastId);
  if (broadcast.isPending) return <Skeleton className="h-64 w-full" aria-hidden="true" />;
  if (broadcast.error || !broadcast.data) {
    return (
      <div className="space-y-3">
        <BackButton onBack={onBack} />
        <ErrorAlert
          message={t(resolveMailboxErrorKey(broadcast.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void broadcast.refetch()}
        />
      </div>
    );
  }
  if (broadcast.data.status === 'draft') {
    return (
      <BroadcastEditor
        key={broadcast.data.id}
        broadcast={broadcast.data}
        enabled={enabled}
        canManage={canManage}
        onBack={onBack}
        onSaved={() => undefined}
      />
    );
  }
  return <BroadcastDetail broadcast={broadcast.data} canManage={canManage} onBack={onBack} />;
}
