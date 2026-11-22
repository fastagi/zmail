'use client';

import { useState, useSyncExternalStore } from 'react';
import { AlertTriangle, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import type {
  ExternalMailConnection,
  ExternalMailConnector,
} from '@/features/mailbox/external-mail-schemas';
import { useBeginExternalAuthorization } from '@/features/mailbox/hooks/use-external-mail-actions';
import { providerName, reconnectKind } from '@/features/mailbox/utils/external-mail';
import { useT } from '@/i18n';

const DISMISSED_KEY = 'zmail.externalReconnectDismissed';

/** A connection's failure as the banner remembers it: once dismissed, the same failure stays quiet. */
function failureKey(connection: ExternalMailConnection): string {
  return `${connection.id}:${connection.last_synced_at ?? 'never'}`;
}

function readDismissed(): string[] {
  try {
    const raw = window.localStorage.getItem(DISMISSED_KEY);
    const value: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(value) ? value.filter(item => typeof item === 'string').slice(-20) : [];
  } catch {
    return [];
  }
}

function writeDismissed(keys: string[]): void {
  try {
    window.localStorage.setItem(DISMISSED_KEY, JSON.stringify(keys.slice(-20)));
  } catch {
    // The dismissal lasts for this page only.
  }
}

const subscribeToNothing = () => () => {};

/**
 * The one-time notice at the top of the inbox (roadmap §5.1, item 6) for the first connection that
 * lost its grant: when it last synced and a Reconnect button. Dismissing it hides that failure for
 * good; a later failure of the same connection shows again.
 */
export function ExternalReconnectBanner({
  organizationId,
  connections,
  connectors,
  onReconnectWithPassword = null,
}: {
  organizationId: number;
  connections: readonly ExternalMailConnection[];
  /** The instance's connectors; undefined while the list is unknown. */
  connectors?: readonly ExternalMailConnector[];
  /** Opens the connect form for an app password connection, which has no sign-in page. */
  onReconnectWithPassword?:
    | ((preset: { provider: string; address: string; connectionId: number }) => void)
    | null;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const reconnect = useBeginExternalAuthorization(organizationId);
  // Storage is read only after hydration, so the server and the first client render agree.
  const hydrated = useSyncExternalStore(
    subscribeToNothing,
    () => true,
    () => false
  );
  const [dismissed, setDismissed] = useState<string[] | null>(null);
  const known = dismissed ?? (hydrated ? readDismissed() : null);
  if (known === null) return null;
  const connection = connections.find(
    item => item.status === 'needs_reconnect' && !known.includes(failureKey(item))
  );
  if (!connection) return null;

  const provider = providerName(connection.provider);
  const address = connection.account_address;
  const kind = reconnectKind(connection.provider, connectors);
  const password = kind === 'password';
  const dismiss = () => {
    const next = [...known, failureKey(connection)];
    writeDismissed(next);
    setDismissed(next);
  };

  return (
    <div
      role="status"
      className="mx-3 mb-2 flex flex-wrap items-start gap-x-3 gap-y-2 rounded-lg border border-warning/30 bg-warning-subtle px-3 py-2.5 text-[13px] text-text-main"
    >
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden="true" />
      <div className="min-w-0 flex-1 basis-48">
        <p className="font-medium">{t('external.banner.title', { provider })}</p>
        <p className="text-xs text-text-muted">
          {connection.last_synced_at
            ? t('external.banner.lastSync', {
                address,
                time: formatter.format(new Date(connection.last_synced_at)),
              })
            : t('external.banner.neverSynced', { address })}
        </p>
        {reconnect.error ? (
          <p role="alert" className="mt-1 text-xs text-error">
            {t('external.connect.failed')}
          </p>
        ) : null}
        {kind === 'unavailable' ? (
          <p className="mt-1 text-xs text-text-muted">{t('external.manage.connectorOff')}</p>
        ) : null}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {kind === 'unavailable' || (password && !onReconnectWithPassword) ? null : (
          <Button
            type="button"
            size="sm"
            variant="outline"
            loading={reconnect.isPending}
            onClick={() =>
              password && onReconnectWithPassword
                ? onReconnectWithPassword({
                    provider: connection.provider,
                    address,
                    connectionId: connection.id,
                  })
                : reconnect.begin({
                    kind: 'reconnect',
                    provider: connection.provider,
                    connectionId: connection.id,
                  })
            }
          >
            {t('external.banner.reconnect')}
          </Button>
        )}
        <Button
          type="button"
          isIcon
          size="sm"
          variant="ghost"
          aria-label={t('external.banner.dismiss')}
          title={t('external.banner.dismiss')}
          onClick={dismiss}
        >
          <X className="size-4" />
        </Button>
      </div>
    </div>
  );
}
