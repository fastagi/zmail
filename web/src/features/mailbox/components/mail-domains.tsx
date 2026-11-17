'use client';

import { useState, type FormEvent } from 'react';
import { Check, Copy, Globe, Plus, RefreshCw, Trash2, Wrench } from 'lucide-react';
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
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import {
  useConnectMailDomain,
  useMailDomains,
  useRefreshMailDomain,
  useRemoveMailDomain,
} from '@/features/mailbox/hooks/use-mailbox';
import { MAIL_DOMAIN_NAME_MAX_LENGTH } from '@/features/mailbox/schemas';
import type { MailDomain, MailDomainRecord } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
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
          {domains.data.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t('domain.connectedEmpty')}</p>
          ) : (
            <ul className="space-y-4">
              {domains.data.map(domain => (
                <li key={domain.id}>
                  <DomainCard domain={domain} canManage={canManage} />
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
  const connect = useConnectMailDomain(useMailOrganizationId());
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    const value = name.trim();
    if (value === '') {
      setError(t('errors.domainInvalid'));
      return;
    }
    try {
      const domain = await connect.mutateAsync(value);
      setName('');
      toast.success(t(domain.status === 'verified' ? 'domain.verified' : 'domain.connected'));
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <form className="space-y-2 border-t pt-4" onSubmit={submit}>
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
  const [error, setError] = useState<string | null>(null);
  const verified = domain.status === 'verified';
  const headingId = `mail-domain-${domain.id}`;

  const run = async (action: 'setups' | 'checks') => {
    setError(null);
    try {
      const result = await refresh.mutateAsync({ domain, action });
      if (result.status === 'verified') toast.success(t('domain.verified'));
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
          {t(verified ? 'domain.statusVerified' : 'domain.statusPending')}
        </Badge>
        <span className="text-xs text-muted-foreground">
          {t('domain.zone', { zone: domain.zone_name })}
        </span>
        {canManage ? (
          <div className="ml-auto flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              loading={refresh.isPending && refresh.variables?.action === 'checks'}
              disabled={refresh.isPending || remove.isPending}
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
                disabled={refresh.isPending || remove.isPending}
                icon={<Wrench className="size-4" />}
                onClick={() => void run('setups')}
              >
                {t('domain.setupAction')}
              </Button>
            )}
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
          </div>
        ) : null}
      </div>
      <div className="space-y-3 p-3">
        {!verified ? (
          <p className="text-sm text-muted-foreground">{t('domain.pendingHint')}</p>
        ) : null}
        {domain.last_error ? (
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
        <RecordTable records={domain.records} />
      </div>
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

function CopyButton({ value, label }: { value: string; label: string }) {
  const t = useT('mailbox');
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1_500);
    } catch {
      setCopied(false);
    }
  };
  return (
    <Button
      type="button"
      isIcon
      size="sm"
      variant="ghost"
      className="size-6 shrink-0"
      aria-label={copied ? t('domain.copied') : label}
      title={copied ? t('domain.copied') : label}
      onClick={() => void copy()}
    >
      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
    </Button>
  );
}
