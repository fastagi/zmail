'use client';

import { useId, useState, type ChangeEvent } from 'react';
import { AlertCircle, Download, FileSpreadsheet } from 'lucide-react';
import { useLocale } from 'next-intl';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useAuthStore } from '@/features/auth/store/auth-store';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { saveFile, useImportMailboxes } from '@/features/mailbox/hooks/use-mailbox-admin';
import type {
  MailboxImportResult,
  MailboxImportRowInput,
  MailboxImportRowResult,
} from '@/features/mailbox/types';
import { MAILBOX_IMPORT_TEMPLATE, readImportRows, toCsv } from '@/features/mailbox/utils/csv';
import { guardFormulaCell } from '@/features/mailbox/utils/mailbox-import';
import { importRowErrorKey, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/** Lets spreadsheet programs read the file as UTF-8. */
const BYTE_ORDER_MARK = '\ufeff';
/** Commits larger batches in sequential chunks so slow server provisioning never hits 503 timeouts. */
const COMMIT_CHUNK_SIZE = 5;

type Step =
  | { kind: 'choose' }
  | {
      kind: 'preview';
      rows: MailboxImportRowInput[];
      result: MailboxImportResult;
      defaultedMembersCount: number;
    }
  | { kind: 'done'; rows: MailboxImportRowInput[]; result: MailboxImportResult };

interface CommitProgress {
  completed: number;
  total: number;
}

function useOptionalCurrentUserEmail(): string | null {
  try {
    const email = useAuthStore.use.user()?.email?.trim();
    return email || null;
  } catch {
    return null;
  }
}

function applyDefaultMembers(
  rows: MailboxImportRowInput[],
  currentUserEmail: string | null
): { rows: MailboxImportRowInput[]; defaultedCount: number } {
  if (!currentUserEmail) return { rows, defaultedCount: 0 };
  let defaultedCount = 0;
  const defaultMember = `${currentUserEmail}:manage`;
  const normalized = rows.map(row => {
    if (row.members.trim() !== '') return row;
    defaultedCount += 1;
    return { ...row, members: defaultMember };
  });
  return { rows: normalized, defaultedCount };
}

function saveCsv(text: string, filename: string) {
  saveFile(new Blob([BYTE_ORDER_MARK, text], { type: 'text/csv;charset=utf-8' }), filename);
}

/**
 * Imports shared mailboxes from a CSV file: the file is read here, checked by the API with a dry
 * run, previewed with the reason of each failed row, and created on confirmation. The failed rows
 * download as a file that imports again once fixed.
 */
export function ImportMailboxesDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useT('mailbox');
  return (
    <Dialog open={open} onOpenChange={next => !next && onClose()}>
      <DialogContent size="lg" className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t('admin.importDialog.title')}</DialogTitle>
          <DialogDescription>{t('admin.importDialog.description')}</DialogDescription>
        </DialogHeader>
        {open ? <ImportBody onClose={onClose} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function ImportBody({ onClose }: { onClose: () => void }) {
  const t = useT('mailbox');
  const isZh = useLocale().startsWith('zh');
  const currentUserEmail = useOptionalCurrentUserEmail();
  const importMailboxes = useImportMailboxes(useMailOrganizationId());
  const fileId = useId();
  const [step, setStep] = useState<Step>({ kind: 'choose' });
  const [error, setError] = useState<string | null>(null);
  const [progress, setProgress] = useState<CommitProgress | null>(null);
  const pending = importMailboxes.isPending || progress !== null;

  const reason = (row: MailboxImportRowResult) =>
    t(importRowErrorKey(row.error_code), { emails: row.missing_emails.join(', ') });

  const choose = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    setError(null);
    setStep({ kind: 'choose' });
    if (!file) return;
    let text: string;
    try {
      text = await file.text();
    } catch {
      setError(t('admin.importDialog.errors.unreadable'));
      return;
    }
    const read = readImportRows(text);
    if (!read.ok) {
      setError(t(`admin.importDialog.errors.${read.error}`));
      return;
    }
    const { rows: preparedRows, defaultedCount } = applyDefaultMembers(read.rows, currentUserEmail);
    try {
      const result = await importMailboxes.mutateAsync({ rows: preparedRows, dryRun: true });
      setStep({
        kind: 'preview',
        rows: preparedRows,
        result,
        defaultedMembersCount: defaultedCount,
      });
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  const confirm = async (rows: MailboxImportRowInput[]) => {
    setError(null);
    try {
      let result: MailboxImportResult;
      if (rows.length <= COMMIT_CHUNK_SIZE) {
        result = await importMailboxes.mutateAsync({ rows, dryRun: false });
      } else {
        setProgress({ completed: 0, total: rows.length });
        let succeeded = 0;
        let failed = 0;
        const aggregatedRows: MailboxImportRowResult[] = [];
        for (let offset = 0; offset < rows.length; offset += COMMIT_CHUNK_SIZE) {
          const chunk = rows.slice(offset, offset + COMMIT_CHUNK_SIZE);
          const chunkResult = await importMailboxes.mutateAsync({ rows: chunk, dryRun: false });
          succeeded += chunkResult.succeeded;
          failed += chunkResult.failed;
          for (const row of chunkResult.rows) {
            aggregatedRows.push({ ...row, row: offset + row.row });
          }
          setProgress({
            completed: Math.min(offset + chunk.length, rows.length),
            total: rows.length,
          });
        }
        result = { dry_run: false, succeeded, failed, rows: aggregatedRows };
      }
      setStep({ kind: 'done', rows, result });
      toast.success(t('admin.importDialog.result', countsOf(result)));
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    } finally {
      setProgress(null);
    }
  };

  const downloadFailed = (rows: MailboxImportRowInput[], result: MailboxImportResult) => {
    const failed = result.rows
      .filter(row => row.status === 'failed')
      .map(row => {
        const input = rows[row.row - 1];
        return [
          input?.address ?? row.address,
          input?.display_name ?? '',
          input?.members ?? '',
          reason(row),
        ].map(guardFormulaCell);
      });
    saveCsv(toCsv([['address', 'display_name', 'members', 'error'], ...failed]), 'failed-rows.csv');
  };

  const failedRows =
    step.kind === 'choose' ? [] : step.result.rows.filter(row => row.status === 'failed');
  const counts = step.kind === 'choose' ? null : countsOf(step.result);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 flex-1 space-y-2">
          <Label htmlFor={fileId}>{t('admin.importDialog.file')}</Label>
          <Input
            id={fileId}
            type="file"
            accept=".csv,text/csv"
            disabled={pending}
            onChange={event => void choose(event)}
          />
        </div>
        <Button
          type="button"
          variant="outline"
          icon={<FileSpreadsheet className="size-4" />}
          onClick={() => saveCsv(MAILBOX_IMPORT_TEMPLATE, 'shared-mailboxes-template.csv')}
        >
          {t('admin.importDialog.template')}
        </Button>
      </div>

      {pending ? (
        <p role="status" className="text-sm text-muted-foreground">
          {progress
            ? isZh
              ? `正在导入 ${progress.completed} / ${progress.total}…`
              : `Importing ${progress.completed} / ${progress.total}…`
            : t(
                step.kind === 'preview' ? 'admin.importDialog.creating' : 'admin.importDialog.checking'
              )}
        </p>
      ) : null}

      {error ? (
        <Alert variant="destructive" role="alert">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      {counts ? (
        <p role="status" className="text-sm font-medium">
          {step.kind === 'preview'
            ? t('admin.importDialog.preview', { valid: counts.created, failed: counts.failed })
            : t('admin.importDialog.result', counts)}
        </p>
      ) : null}

      {step.kind === 'preview' && step.defaultedMembersCount > 0 && currentUserEmail ? (
        <p className="text-xs text-muted-foreground">
          {isZh
            ? `未填写成员的 ${step.defaultedMembersCount} 行已默认设为当前管理员（${currentUserEmail}:manage）。`
            : `${step.defaultedMembersCount} ${step.defaultedMembersCount === 1 ? 'row' : 'rows'} with empty members defaulted to ${currentUserEmail}:manage.`}
        </p>
      ) : null}

      {failedRows.length > 0 ? (
        <ul className="max-h-64 divide-y overflow-y-auto rounded-md border text-sm">
          {failedRows.map(row => (
            <li key={row.row} className="space-y-0.5 px-3 py-2">
              <p className="font-medium">
                {t('admin.importDialog.row', { row: row.row, address: row.address })}
              </p>
              <p className="text-error">{reason(row)}</p>
            </li>
          ))}
        </ul>
      ) : null}

      <DialogFooter>
        {step.kind === 'done' && failedRows.length > 0 ? (
          <Button
            type="button"
            variant="outline"
            icon={<Download className="size-4" />}
            onClick={() => downloadFailed(step.rows, step.result)}
          >
            {t('admin.importDialog.downloadFailed')}
          </Button>
        ) : null}
        <Button type="button" variant="outline" disabled={pending} onClick={onClose}>
          {step.kind === 'done' ? t('admin.importDialog.close') : t('actions.cancel')}
        </Button>
        {step.kind === 'preview' ? (
          <Button
            type="button"
            loading={pending}
            disabled={counts === null || counts.created === 0}
            onClick={() => void confirm(step.rows)}
          >
            {t('admin.importDialog.confirm', { count: counts?.created ?? 0 })}
          </Button>
        ) : null}
      </DialogFooter>
    </div>
  );
}

/** Rows that are or were created (valid in a dry run) and rows that failed. */
function countsOf(result: MailboxImportResult) {
  const created = result.rows.filter(row => row.status !== 'failed').length;
  return { created, failed: result.rows.length - created };
}
