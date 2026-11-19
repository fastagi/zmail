'use client';

import { useState, type FormEvent } from 'react';
import Link from 'next/link';
import { ArrowLeft, Lock, Pencil, Plus, Trash2 } from 'lucide-react';
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
import { Switch } from '@/components/ui/switch';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ROUTES } from '@/constants/routes';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import { ErrorAlert, nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import {
  useCreateDnsRecord,
  useDeleteDnsRecord,
  useListDnsRecords,
  useMailZone,
  useUpdateDnsRecord,
} from '@/features/mailbox/hooks/use-mailbox';
import {
  mailOrganizationHref,
  useMailOrganization,
} from '@/features/mailbox/hooks/use-mail-organization';
import {
  MAIL_DNS_RECORD_CONTENT_MAX_LENGTH,
  MAIL_DNS_RECORD_NAME_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import type {
  MailDnsLockedReason,
  MailDnsRecord,
  MailDnsRecordInput,
  MailDnsRecordType,
  MailZone,
} from '@/features/mailbox/types';
import {
  dnsRecordRejectionReason,
  isOrganizationLost,
  resolveMailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const editableTypes: readonly MailDnsRecordType[] = ['A', 'AAAA', 'CNAME', 'TXT', 'MX'];
const proxiableTypes: readonly MailDnsRecordType[] = ['A', 'AAAA', 'CNAME'];

const lockedReasonKey = {
  mail_receiving: 'dns.lockedReceiving',
  mail_spf: 'dns.lockedSpf',
  mail_dmarc: 'dns.lockedDmarc',
  mail_dkim: 'dns.lockedDkim',
  mail_sending: 'dns.lockedSending',
} as const satisfies Record<MailDnsLockedReason, string>;

const contentLabelKey = {
  A: 'dns.contentA',
  AAAA: 'dns.contentAAAA',
  CNAME: 'dns.contentCNAME',
  TXT: 'dns.contentTXT',
  MX: 'dns.contentMX',
} as const satisfies Record<MailDnsRecordType, string>;

const contentPlaceholder: Record<MailDnsRecordType, string> = {
  A: '192.0.2.1',
  AAAA: '2001:db8::1',
  CNAME: 'target.example.com',
  TXT: 'v=spf1 include:example.net ~all',
  MX: 'mx.example.net',
};

type RecordField = 'type' | 'name' | 'content' | 'ttl' | 'priority' | 'proxied';

/** A whole number written with digits only, or null; `''`, `1.5`, and `soon` are not. */
function wholeNumber(raw: string): number | null {
  const value = raw.trim();
  return /^\d{1,9}$/u.test(value) ? Number(value) : null;
}

function isEditableType(type: string): type is MailDnsRecordType {
  return (editableTypes as readonly string[]).includes(type);
}

/**
 * Every DNS record of one managed zone, read from Cloudflare. Owners and admins add, change, and
 * delete records; records Zmail mail relies on are locked, and members only read.
 */
export function MailDnsRecordsPage({ zoneId }: { zoneId: number }) {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader title={t('dns.title')} description={t('dns.description')} scope={scope}>
        <Button asChild size="sm" variant="outline">
          <Link
            href={
              scope.status === 'ready'
                ? mailOrganizationHref(ROUTES.CONSOLE.MAIL_DOMAIN, scope.organization.id)
                : ROUTES.CONSOLE.MAIL_DOMAIN
            }
          >
            <ArrowLeft className="size-4" aria-hidden="true" />
            {t('dns.back')}
          </Link>
        </Button>
      </MailPageHeader>
      <MailOrganizationBoundary scope={scope}>
        {organization => <ZoneRecords zoneId={zoneId} canManage={organization.canManage} />}
      </MailOrganizationBoundary>
    </div>
  );
}

type DialogState = { mode: 'create' } | { mode: 'edit'; record: MailDnsRecord } | null;

function ZoneRecords({ zoneId, canManage }: { zoneId: number; canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const zone = useMailZone(organizationId, zoneId);
  const records = useListDnsRecords(organizationId, zoneId);
  const [dialog, setDialog] = useState<DialogState>(null);
  const [deleting, setDeleting] = useState<MailDnsRecord | null>(null);

  if (zone.isPending || records.isPending) {
    return (
      <div className="space-y-4" aria-hidden="true">
        <Skeleton className="h-10 w-64" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  if (isOrganizationLost(zone.error) || isOrganizationLost(records.error)) {
    return <OrganizationLostState />;
  }
  if (zone.error || !zone.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(zone.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void zone.refetch()}
      />
    );
  }
  if (records.error || !records.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(records.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void records.refetch()}
      />
    );
  }

  const current = zone.data;
  const active = current.status === 'active';
  return (
    <section aria-labelledby="mail-dns-zone" className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h2 id="mail-dns-zone" className="text-lg font-semibold break-all">
          {current.name}
        </h2>
        <Badge
          variant="outline"
          className={cn(
            'border-transparent',
            active ? 'bg-success-subtle text-success' : 'bg-warning-subtle text-warning'
          )}
        >
          {t(active ? 'domain.zoneStatusActive' : 'domain.zoneStatusPending')}
        </Badge>
        {canManage ? (
          <Button
            type="button"
            size="sm"
            className="ml-auto"
            icon={<Plus className="size-4" />}
            onClick={() => setDialog({ mode: 'create' })}
          >
            {t('dns.add')}
          </Button>
        ) : null}
      </div>
      {active ? null : (
        <Alert className="border-warning/40 bg-warning-subtle text-warning">
          <AlertDescription>{t('dns.zonePending', { zone: current.name })}</AlertDescription>
        </Alert>
      )}
      <p className="text-sm text-muted-foreground">
        {canManage ? t('dns.lockedHint') : t('dns.readOnly')}
      </p>
      {records.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('dns.empty')}</p>
      ) : (
        <RecordsTable
          records={records.data}
          canManage={canManage}
          onEdit={record => setDialog({ mode: 'edit', record })}
          onDelete={setDeleting}
        />
      )}
      {dialog ? (
        <RecordDialog
          key={dialog.mode === 'edit' ? dialog.record.id : 'create'}
          zone={current}
          record={dialog.mode === 'edit' ? dialog.record : null}
          onClose={() => setDialog(null)}
        />
      ) : null}
      {deleting ? (
        <DeleteRecordDialog
          zoneId={current.id}
          record={deleting}
          onClose={() => setDeleting(null)}
        />
      ) : null}
    </section>
  );
}

function RecordsTable({
  records,
  canManage,
  onEdit,
  onDelete,
}: {
  records: MailDnsRecord[];
  canManage: boolean;
  onEdit: (record: MailDnsRecord) => void;
  onDelete: (record: MailDnsRecord) => void;
}) {
  const t = useT('mailbox');
  return (
    <Table aria-label={t('dns.tableLabel')}>
      <TableHeader>
        <TableRow>
          <TableHead>{t('dns.type')}</TableHead>
          <TableHead>{t('dns.name')}</TableHead>
          <TableHead>{t('dns.content')}</TableHead>
          <TableHead>{t('dns.ttl')}</TableHead>
          <TableHead>{t('dns.proxy')}</TableHead>
          <TableHead>
            <span className="sr-only">{t('dns.actions')}</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {records.map(record => {
          const label = { type: record.type, name: record.name };
          return (
            <TableRow key={record.id} data-record-id={record.id} data-locked={record.locked}>
              <TableCell className="font-mono text-xs">{record.type}</TableCell>
              <TableCell className="max-w-56 min-w-40 font-mono text-xs whitespace-normal [overflow-wrap:anywhere]">
                {record.name}
              </TableCell>
              <TableCell className="max-w-md min-w-48 font-mono text-xs break-all whitespace-normal">
                {record.priority !== undefined && record.type === 'MX'
                  ? `${record.priority} ${record.content}`
                  : record.content}
              </TableCell>
              <TableCell className="text-xs whitespace-nowrap">
                {record.ttl === 1 ? t('dns.ttlAuto') : t('dns.ttlSeconds', { count: record.ttl })}
              </TableCell>
              <TableCell className="text-xs whitespace-nowrap">
                {record.proxied ? t('dns.proxied') : t('dns.dnsOnly')}
              </TableCell>
              <TableCell className="text-right">
                {record.locked ? (
                  <Badge
                    variant="outline"
                    className="gap-1 font-normal whitespace-nowrap text-muted-foreground"
                  >
                    <Lock className="size-3" aria-hidden="true" />
                    {record.locked_reason
                      ? t('dns.locked', { reason: t(lockedReasonKey[record.locked_reason]) })
                      : t('dns.lockedGeneric')}
                  </Badge>
                ) : canManage && isEditableType(record.type) ? (
                  <span className="inline-flex gap-1">
                    <Button
                      type="button"
                      isIcon
                      size="sm"
                      variant="ghost"
                      aria-label={t('dns.edit', label)}
                      title={t('dns.edit', label)}
                      onClick={() => onEdit(record)}
                    >
                      <Pencil className="size-4" />
                    </Button>
                    <Button
                      type="button"
                      isIcon
                      size="sm"
                      variant="ghost"
                      aria-label={t('dns.delete', label)}
                      title={t('dns.delete', label)}
                      onClick={() => onDelete(record)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </span>
                ) : null}
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}

/** Adds a record, or changes one; the fields follow the record type. */
function RecordDialog({
  zone,
  record,
  onClose,
}: {
  zone: MailZone;
  record: MailDnsRecord | null;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const create = useCreateDnsRecord(organizationId, zone.id);
  const update = useUpdateDnsRecord(organizationId, zone.id);
  const editing = record !== null;
  const [type, setType] = useState<MailDnsRecordType>(
    record && isEditableType(record.type) ? record.type : 'A'
  );
  const [name, setName] = useState(record?.name ?? '');
  const [content, setContent] = useState(record?.content ?? '');
  const [ttl, setTtl] = useState(record && record.ttl !== 1 ? String(record.ttl) : '');
  const [priority, setPriority] = useState(
    record?.priority !== undefined ? String(record.priority) : '10'
  );
  const [proxied, setProxied] = useState(record?.proxied ?? false);
  const [error, setError] = useState<string | null>(null);
  // Each invalid field with the message shown under it.
  const [invalid, setInvalid] = useState<ReadonlyMap<RecordField, string>>(new Map());
  const pending = create.isPending || update.isPending;
  const proxiable = proxiableTypes.includes(type);

  const explain = (failure: unknown): string => {
    if (!(failure instanceof ApiError)) return t(resolveMailboxErrorKey(failure));
    if (failure.errorCode === ApiErrorCode.MAILBOX_DNS_RECORD_REJECTED) {
      const reason = dnsRecordRejectionReason(failure);
      return reason ? t('dns.rejected', { reason }) : t('errors.dnsRecordRejected');
    }
    if (failure.errorCode === ApiErrorCode.MAILBOX_DNS_RECORD_LOCKED && type === 'MX') {
      // Email Routing owns the MX of a receiving domain: another service's MX there can only go.
      return `${t('errors.dnsRecordLocked')} ${t(editing ? 'dns.mxDeleteOnly' : 'dns.mxReceiving')}`;
    }
    return t(resolveMailboxErrorKey(failure));
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    // Numbers are checked here: an empty or malformed one must never reach the API as 0 or null.
    const problems = new Map<RecordField, string>();
    const ttlValue = ttl.trim() === '' ? 1 : wholeNumber(ttl);
    if (ttlValue === null || (ttlValue !== 1 && (ttlValue < 60 || ttlValue > 86_400))) {
      problems.set('ttl', t('dns.ttlInvalid'));
    }
    const priorityValue = wholeNumber(priority);
    if (type === 'MX' && (priorityValue === null || priorityValue > 65_535)) {
      problems.set('priority', t('dns.priorityInvalid'));
    }
    setInvalid(problems);
    if (problems.size > 0 || ttlValue === null) return;
    const input: MailDnsRecordInput = {
      type,
      name: name.trim(),
      content: content.trim(),
      ttl: ttlValue,
      ...(type === 'MX' && priorityValue !== null ? { priority: priorityValue } : {}),
      proxied: proxiable && proxied,
    };
    try {
      if (record) {
        await update.mutateAsync({ recordId: record.id, input });
        toast.success(t('dns.updated'));
      } else {
        await create.mutateAsync(input);
        toast.success(t('dns.created'));
      }
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.fieldErrors) {
        setInvalid(
          new Map(
            Object.keys(failure.fieldErrors).map(field => [
              field as RecordField,
              t('dns.fieldInvalid'),
            ])
          )
        );
      }
      setError(explain(failure));
    }
  };

  const fieldError = (field: RecordField) =>
    invalid.has(field) ? (
      <p id={`mail-dns-${field}-error`} className="text-xs text-error">
        {invalid.get(field)}
      </p>
    ) : null;
  const described = (field: RecordField, hint?: string) =>
    [invalid.has(field) ? `mail-dns-${field}-error` : null, hint ?? null]
      .filter(Boolean)
      .join(' ') || undefined;

  return (
    <Dialog open onOpenChange={open => !open && !pending && onClose()}>
      <DialogContent size="sm">
        <form onSubmit={submit} noValidate>
          <DialogHeader>
            <DialogTitle>
              {record
                ? t('dns.editTitle', { type: record.type, name: record.name })
                : t('dns.addTitle')}
            </DialogTitle>
            <DialogDescription>{t('dns.fieldNameHint', { zone: zone.name })}</DialogDescription>
          </DialogHeader>
          <DialogBody className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="mail-dns-type">{t('dns.fieldType')}</Label>
              <select
                id="mail-dns-type"
                className={cn(nativeSelectClassName, 'w-full')}
                value={type}
                disabled={editing || pending}
                aria-invalid={invalid.has('type') || undefined}
                aria-describedby={described('type')}
                onChange={event => setType(event.target.value as MailDnsRecordType)}
              >
                {editableTypes.map(option => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
              {fieldError('type')}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="mail-dns-name">{t('dns.fieldName')}</Label>
              <Input
                id="mail-dns-name"
                value={name}
                maxLength={MAIL_DNS_RECORD_NAME_MAX_LENGTH}
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                placeholder="@"
                disabled={pending}
                aria-invalid={invalid.has('name') || undefined}
                aria-describedby={described('name')}
                onChange={event => setName(event.target.value)}
              />
              {fieldError('name')}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="mail-dns-content">{t(contentLabelKey[type])}</Label>
              <Input
                id="mail-dns-content"
                value={content}
                maxLength={MAIL_DNS_RECORD_CONTENT_MAX_LENGTH}
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                placeholder={contentPlaceholder[type]}
                disabled={pending}
                aria-invalid={invalid.has('content') || undefined}
                aria-describedby={described('content')}
                onChange={event => setContent(event.target.value)}
              />
              {fieldError('content')}
            </div>
            <div className="flex flex-wrap gap-3">
              <div className="min-w-32 flex-1 space-y-1.5">
                <Label htmlFor="mail-dns-ttl">{t('dns.fieldTtl')}</Label>
                <Input
                  id="mail-dns-ttl"
                  inputMode="numeric"
                  value={ttl}
                  placeholder={t('dns.ttlAuto')}
                  disabled={pending}
                  aria-invalid={invalid.has('ttl') || undefined}
                  aria-describedby={described('ttl', 'mail-dns-ttl-hint')}
                  onChange={event => setTtl(event.target.value)}
                />
                <p id="mail-dns-ttl-hint" className="text-xs text-muted-foreground">
                  {t('dns.fieldTtlHint')}
                </p>
                {fieldError('ttl')}
              </div>
              {type === 'MX' ? (
                <div className="min-w-24 flex-1 space-y-1.5">
                  <Label htmlFor="mail-dns-priority">{t('dns.fieldPriority')}</Label>
                  <Input
                    id="mail-dns-priority"
                    inputMode="numeric"
                    value={priority}
                    disabled={pending}
                    aria-invalid={invalid.has('priority') || undefined}
                    aria-describedby={described('priority')}
                    onChange={event => setPriority(event.target.value)}
                  />
                  {fieldError('priority')}
                </div>
              ) : null}
            </div>
            {proxiable ? (
              <div className="flex items-center gap-2">
                <Switch
                  id="mail-dns-proxied"
                  checked={proxied}
                  disabled={pending}
                  onCheckedChange={setProxied}
                />
                <Label htmlFor="mail-dns-proxied">{t('dns.fieldProxied')}</Label>
              </div>
            ) : null}
            {error ? (
              <p role="alert" className="text-sm break-words text-error">
                {error}
              </p>
            ) : null}
          </DialogBody>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={pending} onClick={onClose}>
              {t('actions.cancel')}
            </Button>
            <Button type="submit" loading={pending}>
              {t('dns.save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function DeleteRecordDialog({
  zoneId,
  record,
  onClose,
}: {
  zoneId: number;
  record: MailDnsRecord;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const remove = useDeleteDnsRecord(useMailOrganizationId(), zoneId);
  const [error, setError] = useState<string | null>(null);
  const label = { type: record.type, name: record.name };

  const confirm = async () => {
    setError(null);
    try {
      await remove.mutateAsync(record.id);
      toast.success(t('dns.deleted'));
      onClose();
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !remove.isPending && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle className="break-all">{t('dns.deleteTitle', label)}</DialogTitle>
          <DialogDescription>{t('dns.deleteDescription')}</DialogDescription>
        </DialogHeader>
        <DialogBody>
          <p className="font-mono text-xs break-all text-muted-foreground">{record.content}</p>
          {error ? (
            <p role="alert" className="mt-3 text-sm text-error">
              {error}
            </p>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={remove.isPending} onClick={onClose}>
            {t('actions.cancel')}
          </Button>
          <Button
            type="button"
            variant="destructive"
            loading={remove.isPending}
            onClick={() => void confirm()}
          >
            {t('actions.delete')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
