'use client';

import { useEffect, useState, type FormEvent } from 'react';
import Link from 'next/link';
import { Globe, Inbox, ListTree, Plus, RefreshCw, Trash2, Wrench } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
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
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { getMailZoneDnsRoute } from '@/constants/routes';
import { MailDomainTestChecklist } from '@/features/mailbox/components/mail-domain-test-checklist';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { CopyButton, ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { MailZoneNameservers } from '@/features/mailbox/components/mail-zone-nameservers';
import {
  useConnectMailDomain,
  useDeleteMailZone,
  useEnableReceiving,
  useListMailZones,
  useMailDomains,
  usePreviewMailDomain,
  useRefreshMailDomain,
  useRemoveMailDomain,
} from '@/features/mailbox/hooks/use-mailbox';
import { MAIL_DOMAIN_NAME_MAX_LENGTH } from '@/features/mailbox/schemas';
import type {
  MailDomain,
  MailDomainCapability,
  MailDomainPreview,
  MailDomainRecord,
  MailZone,
} from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const purposeKey = {
  receiving: 'domain.purposeReceiving',
  sending: 'domain.purposeSending',
  dmarc: 'domain.purposeDmarc',
} as const;

const recordStatusKey = {
  ok: 'domain.recordOk',
  missing: 'domain.recordMissing',
  conflict: 'domain.recordConflict',
} as const;

/**
 * Domains the organization connected: connect one, see every DNS record it needs with its status,
 * run the setup or the check again, and remove a domain no mailbox uses. Members see the same
 * list without the actions.
 */
export function ConnectedDomains({ enabled, canManage }: { enabled: boolean; canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const domains = useMailDomains(organizationId, enabled);
  const zones = useListMailZones(organizationId, enabled);
  // A managed zone no domain lives in any more can be edited or removed on its own.
  const unusedZones =
    zones.data && domains.data
      ? zones.data.filter(zone => !domains.data.some(domain => domain.zone?.id === zone.id))
      : [];

  return (
    <section aria-labelledby="mail-domains-connected" className="space-y-4 rounded-md border p-4">
      <div className="space-y-1">
        <h2 id="mail-domains-connected" className="text-base font-semibold">
          {t('domain.connectedTitle')}
        </h2>
        <p className="max-w-2xl text-sm text-muted-foreground">
          {t(enabled ? 'domain.connectedDescription' : 'domain.connectedDisabled')}
        </p>
      </div>
      {!enabled ? null : domains.isPending ? (
        <Skeleton className="h-24 w-full" aria-hidden="true" />
      ) : domains.error || !domains.data ? (
        <ErrorAlert
          message={t(resolveMailboxErrorKey(domains.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void domains.refetch()}
        />
      ) : (
        <>
          {domains.data.length === 0 && unusedZones.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t('domain.connectedEmpty')}</p>
          ) : (
            <ul className="space-y-4">
              {domains.data.map(domain => (
                <li key={domain.id}>
                  <DomainCard domain={domain} canManage={canManage} />
                </li>
              ))}
              {unusedZones.map(zone => (
                <li key={`zone-${zone.id}`}>
                  <ZoneCard zone={zone} canManage={canManage} />
                </li>
              ))}
            </ul>
          )}
          {canManage ? (
            <ConnectDomainForm />
          ) : (
            <p className="text-xs text-muted-foreground">{t('domain.connectReadOnly')}</p>
          )}
        </>
      )}
    </section>
  );
}

function ConnectDomainForm() {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const connect = useConnectMailDomain(organizationId);
  const preview = usePreviewMailDomain(organizationId);
  const [name, setName] = useState('');
  const [capability, setCapability] = useState<MailDomainCapability>('send_receive');
  // The last preview, shown only while the name it was asked for is still in the field.
  const [found, setFound] = useState<{ query: string; preview: MailDomainPreview } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const value = name.trim();
  const current = found && found.query === value ? found.preview : null;

  const lookUp = async () => {
    if (value === '' || current) return;
    try {
      const result = await preview.mutateAsync(value);
      setFound({ query: value, preview: result });
      setCapability(result.suggested_capability);
    } catch {
      // The preview only suggests; connecting reports a bad name itself.
      setFound(null);
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    if (value === '') {
      setError(t('errors.domainInvalid'));
      return;
    }
    try {
      const domain = await connect.mutateAsync({ name: value, capability });
      setName('');
      setFound(null);
      setCapability('send_receive');
      toast.success(
        t(
          domain.setting_up
            ? 'domain.settingUpToast'
            : domain.status === 'verified'
              ? 'domain.verified'
              : 'domain.connected'
        )
      );
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  const capabilities: { value: MailDomainCapability; label: string; hint: string }[] = [
    {
      value: 'send_receive',
      label: t('domain.capabilitySendReceive'),
      hint: t('domain.capabilitySendReceiveHint'),
    },
    {
      value: 'send_only',
      label: t('domain.capabilitySendOnly'),
      hint: t('domain.capabilitySendOnlyHint'),
    },
  ];

  return (
    <form className="space-y-3 border-t pt-4" onSubmit={submit}>
      <div className="space-y-2">
        <Label htmlFor="mail-domain-name-input">{t('domain.connectLabel')}</Label>
        <div className="flex flex-wrap items-center gap-2">
          <Input
            id="mail-domain-name-input"
            className="w-full max-w-sm flex-1"
            value={name}
            maxLength={MAIL_DOMAIN_NAME_MAX_LENGTH}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            disabled={connect.isPending}
            placeholder={t('domain.connectPlaceholder')}
            aria-describedby="mail-domain-name-hint"
            onChange={event => setName(event.target.value)}
            onBlur={() => void lookUp()}
          />
          <Button
            type="submit"
            size="sm"
            loading={connect.isPending}
            icon={<Plus className="size-4" />}
          >
            {t('domain.connectAction')}
          </Button>
        </div>
        <p id="mail-domain-name-hint" className="text-xs text-muted-foreground">
          {t('domain.connectHint')}
        </p>
      </div>
      <fieldset className="space-y-2" disabled={connect.isPending}>
        <legend className="text-sm font-medium">{t('domain.capabilityLabel')}</legend>
        <div className="flex flex-wrap gap-2">
          {capabilities.map(option => (
            <label
              key={option.value}
              className={cn(
                'flex max-w-xs min-w-56 flex-1 cursor-pointer gap-2 rounded-md border p-2.5 text-sm has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50',
                capability === option.value && 'border-primary bg-primary/5'
              )}
            >
              <input
                type="radio"
                name="mail-domain-capability"
                value={option.value}
                checked={capability === option.value}
                className="mt-0.5 accent-primary"
                onChange={() => setCapability(option.value)}
              />
              <span className="space-y-0.5">
                <span className="block font-medium">{option.label}</span>
                <span className="block text-xs text-muted-foreground">{option.hint}</span>
              </span>
            </label>
          ))}
        </div>
      </fieldset>
      {current && current.suggested_capability === 'send_only' ? (
        <Alert
          data-testid="mail-domain-subdomain-hint"
          className="border-warning/40 bg-warning-subtle text-warning"
        >
          <AlertDescription className="break-words">
            {t('domain.subdomainHint', {
              domain: current.name,
              hosts: current.existing_mx.join(', '),
              apex: current.apex,
            })}
          </AlertDescription>
        </Alert>
      ) : null}
      {error ? (
        <p role="alert" className="text-sm text-error">
          {error}
        </p>
      ) : null}
    </form>
  );
}

function DomainCard({ domain, canManage }: { domain: MailDomain; canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const refresh = useRefreshMailDomain(organizationId);
  const remove = useRemoveMailDomain(organizationId);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [confirmReceiving, setConfirmReceiving] = useState(false);
  // Cloudflare refused the last re-check of the pending zone.
  const [limited, setLimited] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const verified = domain.status === 'verified';
  const headingId = `mail-domain-${domain.id}`;
  const zonePending = domain.zone?.status === 'pending';

  const settingUp = domain.setting_up;
  const busy = settingUp || refresh.isPending || remove.isPending;
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
    <article aria-labelledby={headingId} className="rounded-md border" data-domain={domain.name}>
      <div className="flex flex-wrap items-center gap-3 border-b p-3">
        <Globe className="size-4 text-muted-foreground" aria-hidden="true" />
        <h3 id={headingId} className="min-w-0 font-semibold break-all">
          {domain.name}
        </h3>
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
        ) : null}
        <span className="text-xs text-muted-foreground">
          {t('domain.zone', { zone: domain.zone_name })}
        </span>
        <div className="ml-auto flex flex-wrap gap-2">
          {domain.zone ? (
            <Button asChild size="sm" variant="outline">
              <Link href={getMailZoneDnsRoute(domain.zone.id)}>
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
          {canManage ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={refresh.isPending || remove.isPending}
              icon={<Trash2 className="size-4" />}
              onClick={() => setConfirmRemove(true)}
            >
              {t('domain.removeAction')}
            </Button>
          ) : null}
        </div>
      </div>
      <div className="space-y-3 p-3">
        {settingUp ? (
          <p className="text-sm text-muted-foreground" role="status">
            {t('domain.settingUpHint')}
          </p>
        ) : !verified && !zonePending ? (
          <p className="text-sm text-muted-foreground">{t('domain.pendingHint')}</p>
        ) : null}
        {domain.last_error && !settingUp ? (
          <Alert className="border-warning/40 bg-warning-subtle text-warning">
            <AlertDescription className="break-words">
              {t('domain.lastError', { error: domain.last_error })}
            </AlertDescription>
          </Alert>
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
        {domain.records.length > 0 ? <RecordTable records={domain.records} /> : null}
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
        {verified && !settingUp ? (
          <MailDomainTestChecklist domain={domain.name} capability={domain.capability} />
        ) : null}
      </div>
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

/**
 * Confirms turning a send-only domain into one that receives in Zmail, listing the MX records of
 * the current mail service that Email Routing replaces.
 */
function EnableReceivingDialog({ domain, onClose }: { domain: MailDomain; onClose: () => void }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const preview = usePreviewMailDomain(organizationId);
  const receiving = useEnableReceiving(organizationId);
  const [mx, setMx] = useState<string[] | null>(null);
  const [lookupFailed, setLookupFailed] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { mutateAsync: lookUp } = preview;
  const name = { domain: domain.name };

  useEffect(() => {
    let active = true;
    lookUp(domain.name).then(
      result => active && setMx(result.existing_mx),
      () => active && setLookupFailed(true)
    );
    return () => {
      active = false;
    };
  }, [domain.name, lookUp]);

  const confirm = async () => {
    setError(null);
    try {
      await receiving.mutateAsync(domain.id);
      toast.success(t('domain.receivingStarted'));
      onClose();
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !receiving.isPending && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{t('domain.receivingTitle', name)}</DialogTitle>
          <DialogDescription>{t('domain.receivingDescription', name)}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3 text-sm">
          {lookupFailed ? (
            <p className="text-muted-foreground">{t('domain.receivingLookupFailed')}</p>
          ) : mx === null ? (
            <p className="text-muted-foreground" role="status">
              {t('domain.receivingLoading')}
            </p>
          ) : mx.length === 0 ? (
            <p className="text-muted-foreground">{t('domain.receivingNoMx', name)}</p>
          ) : (
            <div className="space-y-1">
              <p className="font-medium">{t('domain.receivingCurrentMx')}</p>
              <ul className="space-y-1 font-mono text-xs">
                {mx.map(host => (
                  <li key={host} className="rounded-sm bg-muted px-2 py-1 break-all">
                    {host}
                  </li>
                ))}
              </ul>
            </div>
          )}
          <p className="text-muted-foreground">{t('domain.receivingDeleteHint')}</p>
          {error ? (
            <p role="alert" className="text-error">
              {error}
            </p>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={receiving.isPending} onClick={onClose}>
            {t('actions.cancel')}
          </Button>
          <Button type="button" loading={receiving.isPending} onClick={() => void confirm()}>
            {t('domain.receivingConfirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A managed zone no domain uses: its DNS can still be edited, and it can be removed from Zmail
 * once the registrar no longer points at Cloudflare.
 */
function ZoneCard({ zone, canManage }: { zone: MailZone; canManage: boolean }) {
  const t = useT('mailbox');
  const remove = useDeleteMailZone(useMailOrganizationId());
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const headingId = `mail-zone-${zone.id}`;
  const active = zone.status === 'active';

  const confirm = async () => {
    setError(null);
    try {
      await remove.mutateAsync(zone.id);
      setConfirmRemove(false);
      toast.success(t('domain.zoneRemoved'));
    } catch (failure) {
      setConfirmRemove(false);
      if (
        failure instanceof ApiError &&
        failure.errorCode === ApiErrorCode.MAILBOX_ZONE_STILL_DELEGATED
      ) {
        setError(
          zone.original_name_servers.length > 0
            ? t('domain.zoneStillDelegated', {
                zone: zone.name,
                servers: zone.original_name_servers.join(', '),
              })
            : t('domain.zoneStillDelegatedUnknown', { zone: zone.name })
        );
      } else {
        setError(t(resolveMailboxErrorKey(failure)));
      }
    }
  };

  return (
    <article aria-labelledby={headingId} className="rounded-md border" data-zone={zone.name}>
      <div className="flex flex-wrap items-center gap-3 border-b p-3">
        <Globe className="size-4 text-muted-foreground" aria-hidden="true" />
        <h3 id={headingId} className="min-w-0 font-semibold break-all">
          {zone.name}
        </h3>
        <Badge
          variant="outline"
          className={cn(
            'border-transparent',
            active ? 'bg-success-subtle text-success' : 'bg-warning-subtle text-warning'
          )}
        >
          {t(active ? 'domain.zoneStatusActive' : 'domain.zoneStatusPending')}
        </Badge>
        <div className="ml-auto flex flex-wrap gap-2">
          <Button asChild size="sm" variant="outline">
            <Link href={getMailZoneDnsRoute(zone.id)}>
              <ListTree className="size-4" aria-hidden="true" />
              {t('domain.dnsRecords')}
            </Link>
          </Button>
          {canManage ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={remove.isPending}
              icon={<Trash2 className="size-4" />}
              onClick={() => setConfirmRemove(true)}
            >
              {t('domain.zoneRemoveAction')}
            </Button>
          ) : null}
        </div>
      </div>
      <div className="space-y-3 p-3">
        <p className="text-sm text-muted-foreground">{t('domain.zoneUnused')}</p>
        {error ? (
          <p role="alert" className="text-sm break-words text-error">
            {error}
          </p>
        ) : null}
      </div>
      {confirmRemove ? (
        <Dialog open onOpenChange={open => !open && !remove.isPending && setConfirmRemove(false)}>
          <DialogContent size="sm">
            <DialogHeader>
              <DialogTitle>{t('domain.zoneRemoveTitle', { zone: zone.name })}</DialogTitle>
              <DialogDescription>
                {t('domain.zoneRemoveDescription', { zone: zone.name })}
              </DialogDescription>
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
                {t('domain.zoneRemoveAction')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
    </article>
  );
}

function RecordTable({ records }: { records: MailDomainRecord[] }) {
  const t = useT('mailbox');
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('domain.recordPurpose')}</TableHead>
            <TableHead>{t('domain.recordType')}</TableHead>
            <TableHead>{t('domain.recordName')}</TableHead>
            <TableHead>{t('domain.recordValue')}</TableHead>
            <TableHead>{t('domain.recordStatus')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {records.map((record, index) => (
            <TableRow
              key={`${record.purpose}-${record.type}-${record.name}-${index}`}
              data-record-status={record.status}
            >
              <TableCell className="whitespace-nowrap">{t(purposeKey[record.purpose])}</TableCell>
              <TableCell className="font-mono text-xs">{record.type}</TableCell>
              <TableCell className="max-w-56 min-w-40 font-mono text-xs whitespace-normal [overflow-wrap:anywhere]">
                {record.name}
              </TableCell>
              <TableCell className="max-w-md min-w-48 font-mono text-xs break-all whitespace-normal">
                {record.content === '' ? (
                  <span className="font-sans text-muted-foreground">
                    {t('domain.sendingNotEnabled')}
                  </span>
                ) : (
                  <span className="flex items-start gap-1">
                    <span className="min-w-0 flex-1">
                      {record.priority !== undefined && record.type === 'MX'
                        ? `${record.priority} ${record.content}`
                        : record.content}
                    </span>
                    <CopyButton
                      value={record.content}
                      label={t('domain.copyValue', { name: record.name })}
                      copiedLabel={t('domain.copied')}
                    />
                  </span>
                )}
                {record.found ? (
                  <span className="mt-1 block font-sans text-warning">
                    {t('domain.recordFound', { value: record.found })}
                  </span>
                ) : null}
              </TableCell>
              <TableCell>
                <Badge
                  variant="outline"
                  className={cn(
                    'border-transparent whitespace-nowrap',
                    record.status === 'ok'
                      ? 'bg-success-subtle text-success'
                      : record.status === 'conflict'
                        ? 'bg-error-subtle text-error'
                        : 'bg-warning-subtle text-warning'
                  )}
                >
                  {t(recordStatusKey[record.status])}
                </Badge>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
