'use client';

import { useId, useRef, useState, type DragEvent, type FormEvent } from 'react';
import { AlertCircle, Download, FileSpreadsheet, Upload, X } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
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
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useImportMailContacts } from '@/features/mailbox/hooks/use-mail-contacts';
import { MAIL_CONTACT_IMPORT_MAX_BYTES } from '@/features/mailbox/schemas';
import type { MailContactImport, MailContactList } from '@/features/mailbox/types';
import {
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

import { reasonKey } from './contact-shared';
import { buildCsvChunks, formatFileSize, parseCsvRecords } from './csv-chunks';

const NO_LIST_VALUE = '__none__';
const MAX_AGGREGATED_SKIPPED_ITEMS = 200;

const sampleContactsCsv =
  'email,first_name,last_name,company,code\nann@example.com,Ann,Lee,Acme,VIP20\nbob@example.com,Bob,Chen,Globex,\n';
const sampleContactsFilename = 'contacts-sample.csv';

type ContactImportErrorKey =
  | MailboxErrorKey
  | 'contacts.importTooLarge'
  | 'contacts.importPayloadTooLarge';

function resolveContactImportErrorKey(failure: unknown): ContactImportErrorKey {
  if (
    failure instanceof ApiError &&
    (failure.status === 413 || failure.errorCode === ApiErrorCode.COMMON_REQUEST_TOO_LARGE)
  ) {
    return 'contacts.importPayloadTooLarge';
  }
  const key = resolveMailboxErrorKey(failure);
  return key === 'errors.tooLarge' ? 'contacts.importPayloadTooLarge' : key;
}

function downloadSampleCsv() {
  const blob = new Blob([sampleContactsCsv], { type: 'text/csv;charset=utf-8' });
  const objectUrl = URL.createObjectURL(blob);
  try {
    const anchor = document.createElement('a');
    anchor.href = objectUrl;
    anchor.download = sampleContactsFilename;
    anchor.rel = 'noopener noreferrer';
    anchor.click();
  } finally {
    URL.revokeObjectURL(objectUrl);
  }
}

export function ImportDialog({
  lists,
  defaultListId,
  onClose,
}: {
  lists: MailContactList[];
  defaultListId: number | undefined;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const importContacts = useImportMailContacts(useMailOrganizationId());
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [previewRowCount, setPreviewRowCount] = useState<number | null>(null);
  const [isDragging, setIsDragging] = useState(false);
  const [listId, setListId] = useState<number | undefined>(defaultListId);
  const [isImporting, setIsImporting] = useState(false);
  const [progress, setProgress] = useState<{ completed: number; total: number } | null>(null);
  const [partialFailure, setPartialFailure] = useState<{
    completed: number;
    total: number;
  } | null>(null);
  const [errorKey, setErrorKey] = useState<ContactImportErrorKey | null>(null);
  const [result, setResult] = useState<MailContactImport | null>(null);
  const ids = { file: useId(), list: useId() };

  const busy = isImporting || importContacts.isPending;

  const selectFile = (nextFile: File | null) => {
    setFile(nextFile);
    setPreviewRowCount(null);
    setErrorKey(null);
    setPartialFailure(null);
    if (!nextFile) return;
    void nextFile
      .text()
      .then(text => {
        const parsed = parseCsvRecords(text);
        setPreviewRowCount(parsed.rows.length);
      })
      .catch(() => {
        setPreviewRowCount(null);
      });
  };

  const clearFile = () => {
    selectFile(null);
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  const handleDragOver = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    if (!busy) setIsDragging(true);
  };

  const handleDragLeave = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setIsDragging(false);
    }
  };

  const handleDrop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setIsDragging(false);
    if (busy) return;
    const dropped = event.dataTransfer.files?.[0] ?? null;
    if (dropped) {
      selectFile(dropped);
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorKey(null);
    setPartialFailure(null);
    if (!file) return;
    if (file.size > MAIL_CONTACT_IMPORT_MAX_BYTES) {
      setErrorKey('contacts.importTooLarge');
      return;
    }

    setIsImporting(true);
    let completedRows = 0;
    let totalRows = 0;
    try {
      const csv = await file.text();
      const chunks = buildCsvChunks(csv);
      totalRows = chunks.reduce((sum, chunk) => sum + chunk.rowCount, 0);

      if (chunks.length === 1) {
        const imported = await importContacts.mutateAsync({
          csv: chunks[0]!.csv,
          ...(listId === undefined ? {} : { list_id: listId }),
        });
        setResult(imported);
        toast.success(t('contacts.importDone'));
        return;
      }

      setProgress({ completed: 0, total: totalRows });
      const ignoredSet = new Set<string>();
      const aggregated: MailContactImport = {
        rows: 0,
        created: 0,
        updated: 0,
        skipped_count: 0,
        skipped: [],
        ignored_columns: [],
      };

      for (const chunk of chunks) {
        const imported = await importContacts.mutateAsync({
          csv: chunk.csv,
          ...(listId === undefined ? {} : { list_id: listId }),
        });
        aggregated.rows += imported.rows;
        aggregated.created += imported.created;
        aggregated.updated += imported.updated;
        aggregated.skipped_count += imported.skipped_count;
        for (const item of imported.skipped) {
          if (aggregated.skipped.length < MAX_AGGREGATED_SKIPPED_ITEMS) {
            aggregated.skipped.push({
              ...item,
              line: item.line + chunk.startRowIndex,
            });
          }
        }
        for (const column of imported.ignored_columns) {
          ignoredSet.add(column);
        }
        completedRows += chunk.rowCount;
        setProgress({ completed: completedRows, total: totalRows });
      }

      aggregated.ignored_columns = Array.from(ignoredSet);
      setResult(aggregated);
      toast.success(t('contacts.importDone'));
    } catch (failure) {
      setErrorKey(resolveContactImportErrorKey(failure));
      if (completedRows > 0 && totalRows > 0) {
        setPartialFailure({ completed: completedRows, total: totalRows });
      }
    } finally {
      setIsImporting(false);
      setProgress(null);
    }
  };

  const progressPercent =
    progress && progress.total > 0
      ? Math.min(100, Math.round((progress.completed / progress.total) * 100))
      : 0;

  return (
    <Dialog open onOpenChange={open => !open && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('contacts.importTitle')}</DialogTitle>
          <DialogDescription>{t('contacts.importDescription')}</DialogDescription>
        </DialogHeader>
        {result ? (
          <>
            <DialogBody className="space-y-3">
              <ImportReport result={result} />
            </DialogBody>
            <DialogFooter className="mt-4">
              <Button type="button" onClick={onClose}>
                {t('contacts.close')}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit}>
            <DialogBody className="space-y-4">
              <div className="space-y-2">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <Label htmlFor={ids.file}>{t('contacts.importFile')}</Label>
                  <Button
                    type="button"
                    size="xs"
                    variant="outline"
                    disabled={busy}
                    icon={<Download className="size-3.5" aria-hidden="true" />}
                    onClick={downloadSampleCsv}
                  >
                    {t('contacts.importSample')}
                  </Button>
                </div>
                <div
                  onDragEnter={handleDragOver}
                  onDragOver={handleDragOver}
                  onDragLeave={handleDragLeave}
                  onDrop={handleDrop}
                  className={cn(
                    'relative rounded-lg border border-dashed transition-colors',
                    isDragging
                      ? 'border-primary bg-primary/5'
                      : 'border-input bg-muted/20 hover:bg-muted/40',
                    busy && 'pointer-events-none opacity-60'
                  )}
                >
                  <input
                    ref={fileInputRef}
                    id={ids.file}
                    type="file"
                    accept=".csv,text/csv"
                    disabled={busy}
                    className="sr-only"
                    onChange={event => selectFile(event.target.files?.[0] ?? null)}
                  />
                  {file ? (
                    <div className="flex items-center justify-between gap-3 p-3.5">
                      <div className="flex min-w-0 items-center gap-3">
                        <div className="bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-md">
                          <FileSpreadsheet className="size-4" aria-hidden="true" />
                        </div>
                        <div className="min-w-0">
                          <p className="truncate text-sm font-medium">{file.name}</p>
                          <p className="text-muted-foreground text-xs">
                            {formatFileSize(file.size)}
                            {previewRowCount !== null
                              ? ` · ${t('contacts.importFileRows', { count: previewRowCount.toLocaleString() })}`
                              : ''}
                          </p>
                        </div>
                      </div>
                      <Button
                        type="button"
                        size="xs"
                        isIcon
                        variant="ghost"
                        disabled={busy}
                        aria-label={t('contacts.importChangeFile')}
                        onClick={clearFile}
                      >
                        <X className="size-3.5" aria-hidden="true" />
                      </Button>
                    </div>
                  ) : (
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => fileInputRef.current?.click()}
                      className="flex w-full flex-col items-center justify-center gap-1.5 px-4 py-6 text-center focus-visible:outline-none"
                    >
                      <div className="bg-muted text-muted-foreground flex size-9 items-center justify-center rounded-full">
                        <Upload className="size-4" aria-hidden="true" />
                      </div>
                      <span className="text-sm font-medium">
                        {t('contacts.importDropzoneTitle')}
                      </span>
                      <span className="text-muted-foreground text-xs">
                        {t('contacts.importDropzoneHint')}
                      </span>
                    </button>
                  )}
                </div>
              </div>
              <div className="space-y-2">
                <Label htmlFor={ids.list}>{t('contacts.importList')}</Label>
                <Select
                  value={listId === undefined ? NO_LIST_VALUE : String(listId)}
                  disabled={busy}
                  onValueChange={value =>
                    setListId(value === NO_LIST_VALUE ? undefined : Number(value))
                  }
                >
                  <SelectTrigger id={ids.list} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={NO_LIST_VALUE}>{t('contacts.importNoList')}</SelectItem>
                    {lists.map(list => (
                      <SelectItem key={list.id} value={String(list.id)}>
                        {list.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              {progress ? (
                <div className="space-y-1.5" role="status" aria-live="polite">
                  <div className="flex items-center justify-between text-xs">
                    <span className="font-medium">
                      {t('contacts.importProgress', {
                        completed: progress.completed.toLocaleString(),
                        total: progress.total.toLocaleString(),
                      })}
                    </span>
                    <span className="text-muted-foreground tabular-nums">{progressPercent}%</span>
                  </div>
                  <div
                    role="progressbar"
                    aria-valuenow={progress.completed}
                    aria-valuemin={0}
                    aria-valuemax={progress.total}
                    className="bg-muted h-2 w-full overflow-hidden rounded-full"
                  >
                    <div
                      className="bg-primary h-full transition-all duration-200"
                      style={{ width: `${progressPercent}%` }}
                    />
                  </div>
                </div>
              ) : null}
              {errorKey ? (
                <Alert variant="destructive">
                  <AlertCircle className="size-4" aria-hidden="true" />
                  <AlertDescription className="space-y-1">
                    <p>{t(errorKey)}</p>
                    {partialFailure ? (
                      <p className="text-xs opacity-90">
                        {t('contacts.importPartialFailure', {
                          completed: partialFailure.completed.toLocaleString(),
                          total: partialFailure.total.toLocaleString(),
                        })}
                      </p>
                    ) : null}
                  </AlertDescription>
                </Alert>
              ) : null}
            </DialogBody>
            <DialogFooter className="mt-4">
              <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
                {t('actions.cancel')}
              </Button>
              <Button
                type="submit"
                loading={busy}
                disabled={!file}
                icon={<Upload className="size-4" />}
              >
                {t('contacts.importRun')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

function ImportReport({ result }: { result: MailContactImport }) {
  const t = useT('mailbox');
  const unlisted = result.skipped_count - result.skipped.length;
  return (
    <div className="space-y-3 text-sm">
      <p role="status">
        {t('contacts.importSummary', {
          rows: result.rows,
          created: result.created,
          updated: result.updated,
          skipped: result.skipped_count,
        })}
      </p>
      {result.ignored_columns.length > 0 ? (
        <p className="text-muted-foreground">
          {t('contacts.importIgnored', { columns: result.ignored_columns.join(', ') })}
        </p>
      ) : null}
      {result.skipped.length > 0 ? (
        <section className="space-y-2">
          <h3 className="font-medium">{t('contacts.importSkipped')}</h3>
          <ul className="max-h-60 divide-y overflow-auto rounded-md border">
            {result.skipped.map(skip => (
              <li key={skip.line} className="flex justify-between gap-3 px-3 py-1.5">
                <span className="whitespace-nowrap">
                  {t('contacts.importLine', { line: skip.line })}
                </span>
                <span className="text-muted-foreground">{t(reasonKey[skip.reason])}</span>
              </li>
            ))}
          </ul>
          {unlisted > 0 ? (
            <p className="text-muted-foreground">{t('contacts.importMore', { count: unlisted })}</p>
          ) : null}
        </section>
      ) : null}
    </div>
  );
}
