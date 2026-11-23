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
  const pending = importMembers.isPending;

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
      setStep({ kind: 'preview', rows: read.rows, result });
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  const submit = async (rows: MemberImportRowInput[]) => {
    setError(null);
    try {
      const result = await importMembers.mutateAsync({ dryRun: false, domain, features, rows });
      setStep({ kind: 'done', rows, result });
      const { invited, created, failed } = result;
      toast[failed > 0 ? 'warning' : 'success'](
        t('admin.rosterImport.doneToast', { invited, created, failed })
      );
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  /** Sends only the failed rows again and puts their new outcome in place of the old one. */
  const retryFailed = async (rows: MemberImportRowInput[], previous: MemberImportResult) => {
    const indexes = previous.rows.filter(row => row.status === 'failed').map(row => row.row - 1);
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
      setStep({ kind: 'done', rows, result: merged });
      const { invited, created, failed } = next;
      toast[failed > 0 ? 'warning' : 'success'](
        t('admin.rosterImport.retryToast', { invited, created, failed })
      );
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    } finally {
      setRetrying(false);
    }
  };

  const downloadFailed = (rows: MemberImportRowInput[], result: MemberImportResult) => {
    const failed = result.rows
      .filter(row => row.status === 'failed')
      .map(row => {
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

  const summary = step.kind === 'choose' ? null : summaryOf(step.result);
  const failedCount = summary?.failed ?? 0;
  const actionable = step.kind === 'preview' ? step.result.valid : 0;

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
                  count:
                    step.kind === 'done'
                      ? failedCount
                      : step.kind === 'preview'
                        ? step.result.valid
                        : 0,
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
                    skipped: summary.skipped,
                    failed: summary.failed,
                  })
                : t('admin.rosterImport.doneSummary', {
                    total: summary.total,
                    invited: summary.invited,
                    created: summary.created,
                    skipped: summary.skipped,
                    failed: summary.failed,
                  })}
            </p>
            <RosterResultTable rows={step.result.rows} />
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
            {failedCount > 0 ? (
              <>
                <Button
                  type="button"
                  variant="outline"
                  icon={<Download className="size-4" />}
                  disabled={pending}
                  onClick={() => downloadFailed(step.rows, step.result)}
                >
                  {t('admin.rosterImport.downloadFailed')}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  icon={<RotateCcw className="size-4" />}
                  loading={retrying}
                  onClick={() => void retryFailed(step.rows, step.result)}
                >
                  {t('admin.rosterImport.retryFailed', { count: failedCount })}
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

function RosterResultTable({ rows }: { rows: MemberImportRowResult[] }) {
  const t = useT('mailbox');
  return (
    <div className="max-h-80 overflow-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-12">{t('admin.rosterImport.columns.row')}</TableHead>
            <TableHead>{t('admin.rosterImport.columns.email')}</TableHead>
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
          {rows.map(row => (
            <TableRow
              key={row.row}
              data-status={row.status}
              className={cn(row.status === 'failed' && 'bg-destructive/5 hover:bg-destructive/10')}
            >
              <TableCell className="tabular-nums text-muted-foreground">{row.row}</TableCell>
              <TableCell className="max-w-48 whitespace-normal sm:truncate" title={row.email}>
                <span className="break-all sm:break-normal">{row.email || '—'}</span>
                {/* On a phone the other columns stack here instead of scrolling sideways. */}
                <div className="mt-1 space-y-1 sm:hidden">
                  {row.address ? (
                    <p className="break-all text-xs text-muted-foreground">{row.address}</p>
                  ) : null}
                  <Badge variant={statusVariant(row.status)}>
                    {t(`admin.rosterImport.status.${row.status}`)}
                  </Badge>
                  <p className={cn('text-xs', noteTone(row))}>{rowNote(t, row)}</p>
                </div>
              </TableCell>
              <TableCell className="hidden max-w-48 truncate sm:table-cell" title={row.address}>
                {row.address || '—'}
              </TableCell>
              <TableCell className="hidden sm:table-cell">
                <Badge variant={statusVariant(row.status)}>
                  {t(`admin.rosterImport.status.${row.status}`)}
                </Badge>
              </TableCell>
              <TableCell
                className={cn(
                  'hidden min-w-48 whitespace-normal text-sm sm:table-cell',
                  noteTone(row)
                )}
              >
                {rowNote(t, row)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function noteTone(row: MemberImportRowResult) {
  return row.status === 'failed' ? 'text-error' : 'text-muted-foreground';
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
      return t('admin.rosterImport.notes.valid');
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

/** Counts for the summary lines: `ready` is what a confirmed dry run would process. */
function summaryOf(result: MemberImportResult) {
  return {
    total: result.total,
    ready: result.valid,
    invited: result.invited,
    created: result.created,
    skipped: result.skipped,
    failed: result.failed,
  };
}

/** Puts the retried rows' outcomes in place of the failed ones and counts again. */
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
