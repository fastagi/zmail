'use client';

import { useId } from 'react';

import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import type { Run } from '@/features/mailbox/components/mailbox-access-panel';
import {
  useMailTriageSetting,
  useSetMailboxTriage,
} from '@/features/mailbox/hooks/use-mail-triage';
import type { Mailbox } from '@/features/mailbox/types';
import { useT } from '@/i18n';

/**
 * Whether the caller may switch the mailbox's AI triage: a personal mailbox's owner alone (the
 * only caller with `organize` on a Zmail one, and the only one who sees a connected one at all,
 * never an organization owner or admin by role), a shared mailbox's `manage_members`. A connected
 * mailbox never grants `organize`, so its owner is recognized by `view`. The API remains the
 * authority.
 */
export function mayToggleMailboxTriage(mailbox: Mailbox): boolean {
  if (mailbox.type === 'personal') {
    if (mailbox.origin === 'connected') {
      return mailbox.access.view;
    }
    return mailbox.access.organize && !mailbox.awaiting_handover;
  }
  return mailbox.access.manage_members;
}

/**
 * The mailbox's AI triage switch in its access panel. Shown only to those who may change it, and
 * only while the organization has triage on and the instance has a model.
 */
export function MailboxTriageSwitch({
  organizationId,
  mailbox,
  run,
}: {
  organizationId: number;
  mailbox: Mailbox;
  run: Run;
}) {
  const t = useT('mailbox');
  const allowed = mayToggleMailboxTriage(mailbox);
  const setting = useMailTriageSetting(organizationId, allowed);
  const setTriage = useSetMailboxTriage(organizationId, mailbox.id);
  const ids = { label: useId(), hint: useId() };

  if (!allowed || !setting.data?.available || !setting.data.enabled) return null;

  return (
    <section className="flex items-start justify-between gap-4 rounded-md border p-3">
      <div className="min-w-0 space-y-1">
        <Label id={ids.label} htmlFor={`${ids.label}-switch`}>
          {t('access.triage.label')}
        </Label>
        <p id={ids.hint} className="text-xs text-muted-foreground">
          {t('access.triage.hint')}
        </p>
      </div>
      <Switch
        id={`${ids.label}-switch`}
        aria-describedby={ids.hint}
        checked={mailbox.ai_triage_enabled}
        disabled={setTriage.isPending}
        onCheckedChange={enabled =>
          void run(
            () => setTriage.mutateAsync(enabled),
            t(enabled ? 'messages.triageOn' : 'messages.triageOff')
          )
        }
      />
    </section>
  );
}
