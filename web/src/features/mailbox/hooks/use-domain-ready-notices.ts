'use client';

import { useEffect, useRef } from 'react';

import type { MailDomain } from '@/features/mailbox/types';

interface DomainSnapshot {
  zonePending: boolean;
  ready: boolean;
}

const snapshot = (domain: MailDomain): DomainSnapshot => ({
  zonePending: domain.zone?.status === 'pending',
  ready: domain.status === 'verified' && !domain.setting_up,
});

/**
 * Tells the page when a connected domain moves on by itself while it is open: the registrar's
 * name-server change went live (`onZoneActive`), or the domain finished its setup and verified
 * (`onReady`). The list refreshes on its own while a zone is pending or a setup runs; the first
 * load and domains that appear later only set the baseline, so nothing is announced twice.
 */
export function useDomainReadyNotices(
  domains: MailDomain[] | undefined,
  handlers: { onZoneActive: (zone: string) => void; onReady: (domain: string) => void }
) {
  const seen = useRef<Map<number, DomainSnapshot> | null>(null);
  const latest = useRef(handlers);
  useEffect(() => {
    latest.current = handlers;
  });

  useEffect(() => {
    if (!domains) return;
    const previous = seen.current;
    const next = new Map(domains.map(domain => [domain.id, snapshot(domain)]));
    seen.current = next;
    if (!previous) return;
    for (const domain of domains) {
      const before = previous.get(domain.id);
      const now = next.get(domain.id);
      if (!before || !now) continue;
      if (before.zonePending && !now.zonePending) {
        latest.current.onZoneActive(domain.zone?.name ?? domain.zone_name);
      }
      if (!before.ready && now.ready) latest.current.onReady(domain.name);
    }
  }, [domains]);
}
