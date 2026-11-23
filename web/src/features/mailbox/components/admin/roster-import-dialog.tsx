'use client';

import { useId, useState, type ChangeEvent } from 'react';
import Link from 'next/link';
import { AlertCircle, Download, FileSpreadsheet, Globe, RotateCcw, Upload } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { ROUTES } from '@/constants/routes';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import { saveFile, useImportMembers } from '@/features/mailbox/hooks/use-mailbox-admin';
import type {
  MemberImportFeatures,
  MemberImportResult,
  MemberImportRowInput,
  MemberImportRowResult,
} from '@/features/mailbox/types';
import { toCsv } from '@/features/mailbox/utils/csv';
import { guardFormulaCell } from '@/features/mailbox/utils/mailbox-import';
import { importRowErrorKey, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ROSTER_IMPORT_TEMPLATE, readRosterRows } from '@/features/mailbox/utils/roster-csv';
import { ApiErrorCode } from '@/http/codes';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** Lets spreadsheet programs read the file as UTF-8. */
const BYTE_ORDER_MARK = '﻿';

/** New members get contacts and templates, as an ordinary invitation does. */
const DEFAULT_FEATURES: MemberImportFeatures = {
  broadcasts: false,
  contacts: true,
  templates: true,
  api_keys: false,
};
const FEATURE_KEYS = ['broadcasts', 'contacts', 'templates', 'api_keys'] as const;

type Step =
  | { kind: 'choose' }
  | { kind: 'preview'; rows: MemberImportRowInput[]; result: MemberImportResult }
  | { kind: 'done'; rows: MemberImportRowInput[]; result: MemberImportResult };

function saveCsv(text: string, filename: string) {
  saveFile(new Blob([BYTE_ORDER_MARK, text], { type: 'text/csv;charset=utf-8' }), filename);
}

/** Opens the roster import from the organization's Members tab; owners and admins only. */
export function RosterImportButton({ organizationId }: { organizationId: number }) {
  const t = useT('mailbox');
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        icon={<Upload className="size-4" />}
        onClick={() => setOpen(true)}
      >
        {t('admin.rosterImport.open')}
      </Button>
      <RosterImportDialog
        organizationId={organizationId}
        open={open}
        onClose={() => setOpen(false)}
      />
    </>
  );
}

/**
 * Imports a roster: every row is invited, gets a personal mailbox reserved on a verified domain,
 * and starts with the chosen grants. Rows are checked by the API with a dry run and previewed
 * first; after the import, only the failed rows can be sent again.
 */
export function RosterImportDialog({
  organizationId,
  open,
  onClose,
}: {
  organizationId: number;
  open: boolean;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open={open} onOpenChange={next => !next && onClose()}>
      <DialogContent size="xl">
        <DialogHeader>
          <DialogTitle>{t('admin.rosterImport.title')}</DialogTitle>
          <DialogDescription>{t('admin.rosterImport.description')}</DialogDescription>
        </DialogHeader>
        {open ? <RosterImportBody organizationId={organizationId} onClose={onClose} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function RosterImportBody({
  organizationId,
  onClose,
}: {
  organizationId: number;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const domains = useMailboxDomains(organizationId);
  const verified = (domains.data ?? []).filter(item => item.kind === 'organization');

  if (domains.isPending) {
    return (
      <DialogBody className="space-y-3" aria-busy="true">
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-32 w-full" />
      </DialogBody>
    );
  }
  if (domains.isError) {
    return (
      <>
        <DialogBody>
          <Alert variant="destructive" role="alert">
            <AlertCircle className="size-4" aria-hidden="true" />
            <AlertDescription>{t(resolveMailboxErrorKey(domains.error))}</AlertDescription>
          </Alert>
        </DialogBody>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('admin.rosterImport.close')}
          </Button>
          <Button type="button" onClick={() => void domains.refetch()}>
            {t('admin.rosterImport.retryLoad')}
          </Button>
        </DialogFooter>
      </>
    );
  }
  if (verified.length === 0) {
    return (
      <>
        <DialogBody>
          <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed px-4 py-8 text-center">
            <Globe className="size-8 text-muted-foreground" aria-hidden="true" />
            <p className="font-medium">{t('admin.rosterImport.noDomainTitle')}</p>
            <p className="max-w-md text-sm text-muted-foreground">
              {t('admin.rosterImport.noDomainDescription')}
            </p>
          </div>
        </DialogBody>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('admin.rosterImport.close')}
          </Button>
          <Button asChild>
            <Link href={ROUTES.CONSOLE.MAIL_DOMAIN}>{t('admin.rosterImport.connectDomain')}</Link>
          </Button>
        </DialogFooter>
      </>
    );
  }
  return (
    <RosterImportSteps
      organizationId={organizationId}
      domains={verified.map(item => item.domain)}
      onClose={onClose}
    />
  );
}

function RosterImportSteps({
  organizationId,
  domains,
  onClose,
}: {
  organizationId: number;
  domains: string[];
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const organization = useT('organization');
  const importMembers = useImportMembers(organizationId);
  const fileId = useId();
  const textId = useId();
  const domainId = useId();
  const [text, setText] = useState('');
  const [fileName, setFileName] = useState<string | null>(null);
  const [domain, setDomain] = useState(domains[0] ?? '');
  const [features, setFeatures] = useState<MemberImportFeatures>(DEFAULT_FEATURES);
  const [step, setStep] = useState<Step>({ kind: 'choose' });
  const [error, setError] = useState<string | null>(null);
  const [retrying, setRetrying] = useState(false);
  /** New mailbox prefixes the admin typed for rows whose address was taken, by row index. */
  const [edits, setEdits] = useState<Record<number, string>>({});
  const pending = importMembers.isPending;

  const withEdits = (rows: MemberImportRowInput[]) =>
    rows.map((row, index) => {
      const prefix = edits[index]?.trim();
      return prefix ? { ...row, mailbox: prefix } : row;
    });

  const chooseFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    setError(null);
    if (!file) return;
    try {
      setText(await file.text());
      setFileName(file.name);
    } catch {
      setError(t('admin.rosterImport.errors.unreadable'));
    }
  };

  const check = async () => {
    setError(null);
    const read = readRosterRows(text);
    if (!read.ok) {
      setError(t(`admin.rosterImport.errors.${read.error}`));
      return;
    }
    try {
      const result = await importMembers.mutateAsync({
        dryRun: true,
        domain,
        features,
        rows: read.rows,
      });
      setEdits({});
      setStep({ kind: 'preview', rows: read.rows, result });
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  const submit = async (original: MemberImportRowInput[]) => {
    setError(null);
    const rows = withEdits(original);
    try {
      const result = await importMembers.mutateAsync({ dryRun: false, domain, features, rows });
      setEdits({});
      setStep({ kind: 'done', rows, result });
      const { invited, reserved, created, attention } = rosterSummary(result);
      toast[attention > 0 ? 'warning' : 'success'](
        t('admin.rosterImport.doneToast', { invited, reserved, created, attention })
      );
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  /**
   * Sends only the rows that need attention again, with any new prefix typed for a taken address,
   * and puts their new outcome in place of the old one.
   */
  const retryAttention = async (original: MemberImportRowInput[], previous: MemberImportResult) => {
    const rows = withEdits(original);
    const indexes = previous.rows.filter(needsAttention).map(row => row.row - 1);
    const subset = indexes.map(index => rows[index]).filter(row => row !== undefined);
    if (subset.length === 0) return;
    setError(null);
    setRetrying(true);
    try {
      const next = await importMembers.mutateAsync({
        dryRun: false,
        domain,
        features,
        rows: subset,
      });
      const merged = mergeRetry(previous, indexes, next);
      setEdits({});
      setStep({ kind: 'done', rows, result: merged });
      const { invited, reserved, created, attention } = rosterSummary(next);
      toast[attention > 0 ? 'warning' : 'success'](
        t('admin.rosterImport.retryToast', { invited, reserved, created, attention })
      );
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    } finally {
      setRetrying(false);
    }
  };

  const downloadAttention = (original: MemberImportRowInput[], result: MemberImportResult) => {
    const rows = withEdits(original);
    const failed = result.rows.filter(needsAttention).map(row => {
      const input = rows[row.row - 1];
      return [
        input?.email ?? row.email,
        input?.name ?? '',
        input?.role ?? '',
        input?.mailbox ?? '',
        rowNote(t, row),
      ].map(guardFormulaCell);
    });
    saveCsv(
      toCsv([['email', 'name', 'role', 'mailbox', 'error'], ...failed]),
      'roster-failed-rows.csv'
    );
  };

  const summary = step.kind === 'choose' ? null : rosterSummary(step.result);
  const attentionCount = summary?.attention ?? 0;
  // Ready rows, plus taken addresses the admin gave a new prefix.
  const actionable =
    step.kind === 'preview'
      ? step.result.valid +
        step.result.rows.filter(
          row => canEditPrefix(row) && (edits[row.row - 1]?.trim() ?? '') !== ''
        ).length
      : 0;

  return (
    <>
      <DialogBody className="space-y-4">
        {step.kind === 'choose' ? (
          <div className="space-y-4">
            <div className="flex flex-wrap items-end gap-3">
              <div className="min-w-0 flex-1 basis-56 space-y-2">
                <p className="text-sm font-medium">{t('admin.rosterImport.file')}</p>
                {/* The native picker's own text follows the browser, not the console language. */}
                <div className="flex min-w-0 items-center gap-3">
                  <input
                    id={fileId}
                    type="file"
                    className="peer sr-only"
                    accept=".csv,.tsv,.txt,text/csv,text/tab-separated-values,text/plain"
                    disabled={pending}
                    onChange={event => void chooseFile(event)}
                  />
                  <Button
                    asChild
                    variant="outline"
                    className="shrink-0 cursor-pointer peer-focus-visible:ring-2 peer-focus-visible:ring-ring"
                  >
                    <label htmlFor={fileId}>
                      <Upload className="size-4" aria-hidden="true" />
                      {t('admin.rosterImport.chooseFile')}
                    </label>
                  </Button>
                  <span className="min-w-0 truncate text-sm text-muted-foreground">
                    {fileName ?? t('admin.rosterImport.noFile')}
                  </span>
                </div>
              </div>
              <Button
                type="button"
                variant="outline"
                icon={<FileSpreadsheet className="size-4" />}
                onClick={() => saveCsv(ROSTER_IMPORT_TEMPLATE, 'roster-template.csv')}
              >
                {t('admin.rosterImport.template')}
              </Button>
            </div>
            <div className="space-y-2">
              <Label htmlFor={textId}>{t('admin.rosterImport.paste')}</Label>
              <Textarea
                id={textId}
                rows={6}
                value={text}
                disabled={pending}
                spellCheck={false}
                className="font-mono text-xs"
                placeholder={'email,name,role,mailbox\nalice@example.com,Alice Wang,member,alice'}
                onChange={event => setText(event.target.value)}
              />
              <p className="text-xs text-muted-foreground">{t('admin.rosterImport.columnsHint')}</p>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor={domainId}>{t('admin.rosterImport.domain')}</Label>
                <Select value={domain} onValueChange={setDomain} disabled={pending}>
                  <SelectTrigger id={domainId} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {domains.map(name => (
                      <SelectItem key={name} value={name}>
                        {name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">
                  {t('admin.rosterImport.domainHint', { domain: domain || domains[0] || '' })}
                </p>
              </div>
              <fieldset className="space-y-2">
                <legend className="text-sm font-medium">{t('admin.rosterImport.features')}</legend>
                <div className="grid grid-cols-2 gap-2">
                  {FEATURE_KEYS.map(key => {
                    const id = `${domainId}-${key}`;
                    return (
                      <div key={key} className="flex items-center gap-2">
                        <Checkbox
                          id={id}
                          checked={features[key]}
                          disabled={pending}
                          onCheckedChange={checked =>
                            setFeatures(current => ({ ...current, [key]: checked === true }))
                          }
                        />
                        <Label htmlFor={id} className="font-normal">
                          {organization(`features.${key}`)}
                        </Label>
                      </div>
                    );
                  })}
                </div>
                <p className="text-xs text-muted-foreground">
                  {t('admin.rosterImport.featuresHint')}
                </p>
              </fieldset>
            </div>
          </div>
        ) : null}

        {pending ? (
          <p role="status" className="text-sm text-muted-foreground">
            {step.kind === 'choose'
              ? t('admin.rosterImport.checking')
              : t('admin.rosterImport.importing', {
                  count: step.kind === 'done' ? attentionCount : actionable,
                })}
          </p>
        ) : null}

        {error ? (
          <Alert variant="destructive" role="alert">
            <AlertCircle className="size-4" aria-hidden="true" />
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        {step.kind !== 'choose' && summary ? (
          <>
            <p role="status" className="text-sm font-medium">
              {step.kind === 'preview'
                ? t('admin.rosterImport.previewSummary', {
                    total: summary.total,
                    ready: summary.ready,
                    noop: summary.noop,
                    attention: summary.attention,
                  })
                : t('admin.rosterImport.doneSummary', {
                    total: summary.total,
                    invited: summary.invited,
                    reserved: summary.reserved,
                    created: summary.created,
                    noop: summary.noop,
                    attention: summary.attention,
                  })}
            </p>
            <RosterResultTable
              rows={step.result.rows}
              inputs={step.rows}
              edits={edits}
              disabled={pending}
              onEdit={(index, value) => setEdits(current => ({ ...current, [index]: value }))}
            />
          </>
        ) : null}
      </DialogBody>

      <DialogFooter>
        {step.kind === 'choose' ? (
          <>
            <Button type="button" variant="outline" disabled={pending} onClick={onClose}>
              {t('actions.cancel')}
            </Button>
            <Button
              type="button"
              loading={pending}
              disabled={text.trim() === '' || domain === ''}
              onClick={() => void check()}
            >
              {t('admin.rosterImport.check')}
            </Button>
          </>
        ) : null}
        {step.kind === 'preview' ? (
          <>
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={() => setStep({ kind: 'choose' })}
            >
              {t('admin.rosterImport.back')}
            </Button>
            <Button
              type="button"
              loading={pending}
              disabled={actionable === 0}
              onClick={() => void submit(step.rows)}
            >
              {t('admin.rosterImport.confirm', { count: actionable })}
            </Button>
          </>
        ) : null}
        {step.kind === 'done' ? (
          <>
            {attentionCount > 0 ? (
              <>
                <Button
                  type="button"
                  variant="outline"
                  icon={<Download className="size-4" />}
                  disabled={pending}
                  onClick={() => downloadAttention(step.rows, step.result)}
                >
                  {t('admin.rosterImport.downloadFailed')}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  icon={<RotateCcw className="size-4" />}
                  loading={retrying}
                  onClick={() => void retryAttention(step.rows, step.result)}
                >
                  {t('admin.rosterImport.retryFailed', { count: attentionCount })}
                </Button>
              </>
            ) : null}
            <Button type="button" disabled={pending} onClick={onClose}>
              {t('admin.rosterImport.done')}
            </Button>
          </>
        ) : null}
      </DialogFooter>
    </>
  );
}

function RosterResultTable({
  rows,
  inputs,
  edits,
  disabled,
  onEdit,
}: {
  rows: MemberImportRowResult[];
  inputs: MemberImportRowInput[];
  edits: Record<number, string>;
  disabled: boolean;
  onEdit: (index: number, value: string) => void;
}) {
  const t = useT('mailbox');
  const roleOf = (row: MemberImportRowResult) => {
    const raw = (inputs[row.row - 1]?.role ?? '').trim();
    const role = raw.toLowerCase();
    if (role === '' || role === 'member') return t('admin.rosterImport.roles.member');
    if (role === 'admin') return t('admin.rosterImport.roles.admin');
    return raw;
  };
  const statusBadge = (row: MemberImportRowResult) => {
    const taken = isTaken(row);
    return (
      <Badge
        variant={taken ? 'outline' : statusVariant(row.status)}
        className={cn(taken && 'border-warning/40 bg-warning-subtle text-warning')}
      >
        {t(taken ? 'admin.rosterImport.status.taken' : `admin.rosterImport.status.${row.status}`)}
      </Badge>
    );
  };
  return (
    <div className="max-h-80 overflow-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-12">{t('admin.rosterImport.columns.row')}</TableHead>
            <TableHead>{t('admin.rosterImport.columns.email')}</TableHead>
            <TableHead className="hidden sm:table-cell">
              {t('admin.rosterImport.columns.role')}
            </TableHead>
            <TableHead className="hidden sm:table-cell">
              {t('admin.rosterImport.columns.address')}
            </TableHead>
            <TableHead className="hidden sm:table-cell">
              {t('admin.rosterImport.columns.status')}
            </TableHead>
            <TableHead className="hidden sm:table-cell">
              {t('admin.rosterImport.columns.note')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map(row => {
            const index = row.row - 1;
            return (
              <TableRow
                key={row.row}
                data-status={row.status}
                data-attention={needsAttention(row) ? 'true' : 'false'}
                className={cn(
                  row.status === 'failed' && 'bg-destructive/5 hover:bg-destructive/10',
                  isTaken(row) && 'bg-warning-subtle hover:bg-warning-subtle'
                )}
              >
                <TableCell className="align-top tabular-nums text-muted-foreground">
                  {row.row}
                </TableCell>
                <TableCell className="max-w-56 whitespace-normal align-top" title={row.email}>
                  <span className="break-all">{row.email || '—'}</span>
                  {/* On a phone the other columns stack here instead of scrolling sideways. */}
                  <div className="mt-1 space-y-1 sm:hidden">
                    <p className="text-xs text-muted-foreground">{roleOf(row)}</p>
                    {row.address ? (
                      <p className="break-all text-xs text-muted-foreground">{row.address}</p>
                    ) : null}
                    {statusBadge(row)}
                    <p className={cn('text-xs', noteTone(row))}>{rowNote(t, row)}</p>
                  </div>
                  {canEditPrefix(row) ? (
                    <Input
                      className="mt-2 h-8 max-w-48 border-warning/40 text-sm dark:bg-background"
                      aria-label={t('admin.rosterImport.newPrefixLabel', { email: row.email })}
                      placeholder={t('admin.rosterImport.newPrefixPlaceholder')}
                      value={edits[index] ?? ''}
                      disabled={disabled}
                      spellCheck={false}
                      autoCapitalize="off"
                      onChange={event => onEdit(index, event.target.value)}
                    />
                  ) : null}
                </TableCell>
                <TableCell className="hidden align-top text-sm sm:table-cell">
                  {roleOf(row)}
                </TableCell>
                <TableCell
                  className="hidden max-w-48 truncate align-top sm:table-cell"
                  title={row.address}
                >
                  {row.address || '—'}
                </TableCell>
                <TableCell className="hidden align-top sm:table-cell">{statusBadge(row)}</TableCell>
                <TableCell
                  className={cn(
                    'hidden min-w-48 whitespace-normal align-top text-sm sm:table-cell',
                    noteTone(row)
                  )}
                >
                  {rowNote(t, row)}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}

/** A taken address leaves the person un-invited: the admin has to choose another prefix. */
/** Row codes a different mailbox prefix fixes; the row can take a new one inline. */
const PREFIX_ERROR_CODES: readonly string[] = [
  ApiErrorCode.MAILBOX_IMPORT_DUPLICATE_ROW,
  ApiErrorCode.MAILBOX_ADDRESS_INVALID,
  ApiErrorCode.MAILBOX_ADDRESS_RESERVED,
  ApiErrorCode.MAILBOX_ADDRESS_TAKEN,
];

function canEditPrefix(row: MemberImportRowResult) {
  return (
    isTaken(row) ||
    (row.status === 'failed' &&
      row.error_code !== null &&
      PREFIX_ERROR_CODES.includes(row.error_code))
  );
}

function isTaken(row: MemberImportRowResult) {
  return row.status === 'skipped' && row.skip_reason === 'mailbox_taken';
}

/** Rows the admin must act on: refused rows and taken addresses. Other skips are no-ops. */
export function needsAttention(row: MemberImportRowResult) {
  return row.status === 'failed' || isTaken(row);
}

function noteTone(row: MemberImportRowResult) {
  if (row.status === 'failed') return 'text-error';
  return isTaken(row) ? 'text-warning' : 'text-muted-foreground';
}

function statusVariant(status: MemberImportRowResult['status']) {
  switch (status) {
    case 'failed':
      return 'destructive' as const;
    case 'skipped':
      return 'outline' as const;
    case 'valid':
      return 'secondary' as const;
    default:
      return 'default' as const;
  }
}

type MailboxT = ReturnType<typeof useT<'mailbox'>>;

/** The reason or next step of one row, in local copy; backend text is never shown. */
function rowNote(t: MailboxT, row: MemberImportRowResult): string {
  switch (row.status) {
    case 'failed':
      return t(rosterRowErrorKey(row.error_code), { emails: '' });
    case 'skipped':
      return t(`admin.rosterImport.skip.${row.skip_reason ?? 'already_member'}`);
    case 'invited':
      return row.email_send_status === 'failed' || row.email_send_status === 'not_configured'
        ? t('admin.rosterImport.notes.invitedNoEmail')
        : t('admin.rosterImport.notes.invited');
    case 'created':
      return t('admin.rosterImport.notes.created');
    default:
      return t('admin.rosterImport.notes.valid', { address: row.address });
  }
}

/** Roster-only row codes have their own copy; the rest share the mailbox import's. */
export function rosterRowErrorKey(code: string | null) {
  switch (code) {
    case ApiErrorCode.MAILBOX_IMPORT_EMAIL_INVALID:
      return 'admin.rosterImport.rowErrors.emailInvalid' as const;
    case ApiErrorCode.MAILBOX_IMPORT_ROLE_INVALID:
      return 'admin.rosterImport.rowErrors.roleInvalid' as const;
    case ApiErrorCode.MAILBOX_IMPORT_DOMAIN_REQUIRED:
      return 'admin.rosterImport.rowErrors.domainRequired' as const;
    case ApiErrorCode.MAILBOX_IMPORT_DUPLICATE_ROW:
      return 'admin.rosterImport.rowErrors.duplicate' as const;
    default:
      return importRowErrorKey(code);
  }
}

/**
 * Counts for the summary lines. `reserved` counts the mailboxes held for invited people,
 * `noop` the skips that need nothing, and `attention` the failed rows plus taken addresses.
 */
export function rosterSummary(result: MemberImportResult) {
  const attention = result.rows.filter(needsAttention).length;
  return {
    total: result.total,
    ready: result.valid,
    invited: result.invited,
    reserved: result.rows.filter(row => row.status === 'invited' && row.mailbox_id !== null).length,
    created: result.created,
    noop: result.rows.filter(row => row.status === 'skipped' && !isTaken(row)).length,
    attention,
  };
}

/** Puts the retried rows' outcomes in place of the old ones and counts again. */
export function mergeRetry(
  previous: MemberImportResult,
  indexes: number[],
  next: MemberImportResult
): MemberImportResult {
  const rows = previous.rows.map(row => ({ ...row }));
  next.rows.forEach((row, position) => {
    const index = indexes[position];
    if (index !== undefined && rows[index]) rows[index] = { ...row, row: index + 1 };
  });
  const count = (status: MemberImportRowResult['status']) =>
    rows.filter(row => row.status === status).length;
  return {
    dry_run: false,
    total: rows.length,
    valid: count('valid'),
    invited: count('invited'),
    created: count('created'),
    skipped: count('skipped'),
    failed: count('failed'),
    rows,
  };
}
