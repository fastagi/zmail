'use client';

import { useId, useState, type ChangeEvent } from 'react';
import Link from 'next/link';
import {
  AlertCircle,
  Download,
  FileSpreadsheet,
  Globe,
  RotateCcw,
  Upload,
} from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
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
import { Textarea } from '@/components/ui/textarea';
import { ROUTES } from '@/constants/routes';
import {
  canEditPrefix,
  mergeRetry,
  needsAttention,
  rosterSummary,
  rosterToast,
  rowNote,
} from '@/features/mailbox/components/admin/roster-import-helpers';
import {
  RosterResultTable,
  WelcomePreviewNote,
} from '@/features/mailbox/components/admin/roster-result-table';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import { saveFile, useImportMembers } from '@/features/mailbox/hooks/use-mailbox-admin';
import type {
  MemberImportFeatures,
  MemberImportResult,
  MemberImportRowInput,
} from '@/features/mailbox/types';
import { toCsv } from '@/features/mailbox/utils/csv';
import { guardFormulaCell } from '@/features/mailbox/utils/mailbox-import';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ROSTER_IMPORT_TEMPLATE, readRosterRows } from '@/features/mailbox/utils/roster-csv';
import { useT } from '@/i18n';

export {
  mergeRetry,
  needsAttention,
  rosterRowErrorKey,
  rosterSummary,
  rosterToast,
} from '@/features/mailbox/components/admin/roster-import-helpers';

/** Lets spreadsheet programs read the file as UTF-8. */
const BYTE_ORDER_MARK = '﻿';

/** New members get contacts, templates, and mail apps, as an ordinary invitation does. */
const DEFAULT_FEATURES: MemberImportFeatures = {
  broadcasts: false,
  contacts: true,
  templates: true,
  api_keys: false,
  mail_clients: true,
  developer: false,
};
const FEATURE_KEYS = [
  'broadcasts',
  'contacts',
  'templates',
  'api_keys',
  'mail_clients',
  'developer',
] as const;

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
      const counts = rosterSummary(result);
      toast[counts.attention > 0 ? 'warning' : 'success'](rosterToast(t, counts));
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
      const counts = rosterSummary(next);
      toast[counts.attention > 0 ? 'warning' : 'success'](
        rosterToast(t, counts, { retried: true })
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
            {step.kind === 'preview' && actionable > 0 ? (
              <WelcomePreviewNote
                organizationId={organizationId}
                count={actionable}
                onNavigate={onClose}
              />
            ) : null}
            {step.kind === 'done' &&
            (step.result.welcome_mails_queued > 0 || step.result.welcome_mails_failed > 0) ? (
              <p className="text-sm text-muted-foreground">
                {t('admin.rosterImport.welcomeSummary', {
                  queued: step.result.welcome_mails_queued,
                  failed: step.result.welcome_mails_failed,
                })}
              </p>
            ) : null}
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
