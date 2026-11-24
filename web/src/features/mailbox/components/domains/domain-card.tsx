'use client';

import { useState } from 'react';
import Link from 'next/link';
import {
  ChevronDown,
  Globe,
  Inbox,
  ListTree,
  Loader2,
  Plus,
  RefreshCw,
  Star,
  Trash2,
  Wrench,
} from 'lucide-react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { getMailZoneDnsRoute } from '@/constants/routes';
import { MailDomainTestChecklist } from '@/features/mailbox/components/mail-domain-test-checklist';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { MailZoneNameservers } from '@/features/mailbox/components/mail-zone-nameservers';
import {
  useRefreshMailDomain,
  useRemoveMailDomain,
  useSetDefaultMailDomain,
} from '@/features/mailbox/hooks/use-mailbox';
import { mailOrganizationHref } from '@/features/mailbox/hooks/use-mail-organization';
import type { MailDomain } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

import { DomainMailboxForm, EnableReceivingDialog } from './domain-dialogs';
import { DomainSetupError, DomainSetupSteps, RecordHelp, RecordTable } from './domain-records';

export function DomainCard({ domain, canManage }: { domain: MailDomain; canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const refresh = useRefreshMailDomain(organizationId);
  const remove = useRemoveMailDomain(organizationId);
  const setDefault = useSetDefaultMailDomain(organizationId);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [confirmReceiving, setConfirmReceiving] = useState(false);
  const [creatingMailbox, setCreatingMailbox] = useState(false);
  // Cloudflare refused the last re-check of the pending zone.
  const [limited, setLimited] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // A ready domain folds into one row; its records open on request.
  const [expanded, setExpanded] = useState(false);
  const verified = domain.status === 'verified';
  const headingId = `mail-domain-${domain.id}`;
  const detailsId = `${headingId}-details`;
  const zonePending = domain.zone?.status === 'pending';

  const settingUp = domain.setting_up;
  const ready = verified && !settingUp;
  // Until a domain is ready its next step stays in view.
  const open = !ready || expanded;
  const busy = settingUp || refresh.isPending || remove.isPending || setDefault.isPending;

  const run = async (action: 'setups' | 'checks') => {
    setError(null);
    try {
      const result = await refresh.mutateAsync({ domain, action });
      setLimited(result.zone_check_limited);
      if (result.zone_check_limited) return;
      if (result.zone?.status === 'pending') toast.message(t('domain.zoneStillPending'));
      else if (result.setting_up) toast.message(t('domain.settingUpToast'));
      else if (result.status === 'verified') toast.success(t('domain.verified'));
      else toast.message(t('domain.stillPending'));
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  const makeDefault = async () => {
    setError(null);
    try {
      await setDefault.mutateAsync(domain.id);
      toast.success(t('domain.defaultSet', { domain: domain.name }));
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  const confirm = async () => {
    setError(null);
    try {
      await remove.mutateAsync(domain.id);
      setConfirmRemove(false);
      toast.success(t('domain.removed'));
    } catch (failure) {
      setConfirmRemove(false);
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <article
      aria-labelledby={headingId}
      className="rounded-md border"
      data-domain={domain.name}
      data-expanded={open}
    >
      <div className={cn('flex flex-wrap items-center gap-x-3 gap-y-2 p-3', open && 'border-b')}>
        <Globe className="size-4 text-muted-foreground" aria-hidden="true" />
        <h3 id={headingId} className="min-w-0 font-semibold break-all">
          {domain.name}
        </h3>
        {domain.default ? (
          <Badge
            variant="outline"
            data-testid="mail-domain-default"
            title={t('domain.defaultHint')}
            className="gap-1 border-transparent bg-primary/10 text-primary"
          >
            <Star className="size-3" aria-hidden="true" />
            {t('domain.defaultBadge')}
          </Badge>
        ) : null}
        <Badge
          variant="outline"
          data-testid="mail-domain-status"
          data-state={domain.status}
          className={cn(
            'border-transparent',
            verified ? 'bg-success-subtle text-success' : 'bg-warning-subtle text-warning'
          )}
        >
          {t(
            settingUp
              ? 'domain.statusSettingUp'
              : verified
                ? 'domain.statusVerified'
                : 'domain.statusPending'
          )}
        </Badge>
        {domain.capability === 'send_only' ? (
          <Badge variant="outline" className="font-normal text-muted-foreground">
            {t('domain.sendOnlyBadge')}
          </Badge>
        ) : ready ? (
          <Badge variant="outline" className="font-normal text-muted-foreground">
            {t('domain.capabilitySendReceive')}
          </Badge>
        ) : null}
        {domain.mailbox_count !== undefined ? (
          <span className="text-xs text-muted-foreground">
            {t('domain.mailboxCount', { count: domain.mailbox_count })}
          </span>
        ) : null}
        {open ? (
          <span className="text-xs text-muted-foreground">
            {t('domain.zone', { zone: domain.zone_name })}
          </span>
        ) : null}
        <div className="ml-auto flex flex-wrap gap-2">
          {ready ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              aria-expanded={expanded}
              aria-controls={expanded ? detailsId : undefined}
              icon={
                <ChevronDown
                  className={cn('size-4 transition-transform', expanded && 'rotate-180')}
                />
              }
              onClick={() => setExpanded(current => !current)}
            >
              {t(expanded ? 'domain.hideRecords' : 'domain.showRecords')}
            </Button>
          ) : null}
          {open && domain.zone ? (
            <Button asChild size="sm" variant="outline">
              <Link
                href={mailOrganizationHref(getMailZoneDnsRoute(domain.zone.id), organizationId)}
              >
                <ListTree className="size-4" aria-hidden="true" />
                {t('domain.dnsRecords')}
              </Link>
            </Button>
          ) : null}
          {canManage && !zonePending ? (
            <>
              <Button
                type="button"
                size="sm"
                variant="outline"
                loading={refresh.isPending && refresh.variables?.action === 'checks'}
                disabled={busy}
                icon={<RefreshCw className="size-4" />}
                onClick={() => void run('checks')}
              >
                {t('domain.checkAction')}
              </Button>
              {verified ? null : (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  loading={refresh.isPending && refresh.variables?.action === 'setups'}
                  disabled={busy}
                  icon={<Wrench className="size-4" />}
                  onClick={() => void run('setups')}
                >
                  {t('domain.setupAction')}
                </Button>
              )}
            </>
          ) : null}
          {ready && canManage ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Plus className="size-4" />}
              onClick={() => setCreatingMailbox(true)}
            >
              {t('domain.createMailboxAction', { domain: domain.name })}
            </Button>
          ) : null}
          {ready && canManage && !domain.default ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              loading={setDefault.isPending}
              disabled={busy}
              icon={<Star className="size-4" />}
              onClick={() => void makeDefault()}
            >
              {t('domain.makeDefaultAction')}
            </Button>
          ) : null}
          {canManage ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={refresh.isPending || remove.isPending || setDefault.isPending}
              icon={<Trash2 className="size-4" />}
              onClick={() => setConfirmRemove(true)}
            >
              {t('domain.removeAction')}
            </Button>
          ) : null}
        </div>
      </div>
      {!open && error ? (
        <p role="alert" className="px-3 pb-3 text-sm text-error">
          {error}
        </p>
      ) : null}
      {open ? (
        <div id={detailsId} className="space-y-3 p-3">
          <DomainSetupSteps current={zonePending ? 1 : ready ? 3 : 2} />
          {settingUp ? (
            <p className="flex items-start gap-2 text-sm text-muted-foreground" role="status">
              <Loader2 className="mt-0.5 size-4 shrink-0 animate-spin" aria-hidden="true" />
              <span>{t('domain.settingUpHint')}</span>
            </p>
          ) : !verified && !zonePending ? (
            <p className="text-sm text-muted-foreground">{t('domain.pendingHint')}</p>
          ) : null}
          {domain.last_error && !settingUp ? (
            <DomainSetupError code={domain.last_error_code} detail={domain.last_error} />
          ) : null}
          {error ? (
            <p role="alert" className="text-sm text-error">
              {error}
            </p>
          ) : null}
          {domain.zone && zonePending ? (
            <MailZoneNameservers
              zone={domain.zone}
              canManage={canManage}
              checking={refresh.isPending && refresh.variables?.action === 'checks'}
              checkDisabled={busy}
              limited={limited || domain.zone_check_limited}
              onCheck={() => void run('checks')}
            />
          ) : null}
          {domain.records.length > 0 ? (
            <>
              <RecordTable records={domain.records} />
              <RecordHelp domain={domain} />
            </>
          ) : null}
          {verified && !settingUp && canManage && domain.capability === 'send_only' ? (
            <div className="flex flex-wrap items-center gap-3">
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={busy}
                icon={<Inbox className="size-4" />}
                onClick={() => setConfirmReceiving(true)}
              >
                {t('domain.receivingAction')}
              </Button>
            </div>
          ) : null}
          {ready ? (
            <MailDomainTestChecklist domain={domain.name} capability={domain.capability} />
          ) : null}
        </div>
      ) : null}
      <Dialog open={creatingMailbox} onOpenChange={setCreatingMailbox}>
        <DialogContent size="lg" className="max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t('domain.createMailboxTitle', { domain: domain.name })}</DialogTitle>
            <DialogDescription>{t('domain.createMailboxDescription')}</DialogDescription>
          </DialogHeader>
          {creatingMailbox ? (
            <DomainMailboxForm domain={domain.name} onCreated={() => setCreatingMailbox(false)} />
          ) : null}
        </DialogContent>
      </Dialog>
      {confirmReceiving ? (
        <EnableReceivingDialog domain={domain} onClose={() => setConfirmReceiving(false)} />
      ) : null}
      {confirmRemove ? (
        <Dialog open onOpenChange={open => !open && !remove.isPending && setConfirmRemove(false)}>
          <DialogContent size="sm">
            <DialogHeader>
              <DialogTitle>{t('domain.removeTitle', { domain: domain.name })}</DialogTitle>
              <DialogDescription>{t('domain.removeDescription')}</DialogDescription>
            </DialogHeader>
            <DialogBody />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={remove.isPending}
                onClick={() => setConfirmRemove(false)}
              >
                {t('actions.cancel')}
              </Button>
              <Button
                type="button"
                variant="destructive"
                loading={remove.isPending}
                onClick={() => void confirm()}
              >
                {t('domain.removeAction')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
    </article>
  );
}
