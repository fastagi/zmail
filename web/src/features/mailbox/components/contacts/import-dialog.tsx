'use client';

import { useId, useState, type FormEvent } from 'react';
import { AlertCircle, Download, Upload } from 'lucide-react';
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
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import { useImportMailContacts } from '@/features/mailbox/hooks/use-mail-contacts';
import { MAIL_CONTACT_IMPORT_MAX_BYTES } from '@/features/mailbox/schemas';
import type { MailContactImport, MailContactList } from '@/features/mailbox/types';
import { resolveMailboxErrorKey, type MailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

import { reasonKey } from './contact-shared';

const sampleContactsCsv =
  'email,first_name,last_name,company,code\nann@example.com,Ann,Lee,Acme,VIP20\nbob@example.com,Bob,Chen,Globex,\n';
const sampleContactsFilename = 'contacts-sample.csv';

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
  const [file, setFile] = useState<File | null>(null);
  const [listId, setListId] = useState<number | undefined>(defaultListId);
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | 'contacts.importTooLarge' | null>(
    null
  );
  const [result, setResult] = useState<MailContactImport | null>(null);
  const ids = { file: useId(), list: useId() };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorKey(null);
    if (!file) return;
    if (file.size > MAIL_CONTACT_IMPORT_MAX_BYTES) {
      setErrorKey('contacts.importTooLarge');
      return;
    }
    try {
      const csv = await file.text();
      const imported = await importContacts.mutateAsync({
        csv,
        ...(listId === undefined ? {} : { list_id: listId }),
      });
      setResult(imported);
      toast.success(t('contacts.importDone'));
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !importContacts.isPending && onClose()}>
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
                    disabled={importContacts.isPending}
                    icon={<Download className="size-3.5" aria-hidden="true" />}
                    onClick={downloadSampleCsv}
                  >
                    {t('contacts.importSample')}
                  </Button>
                </div>
                <Input
                  id={ids.file}
                  type="file"
                  accept=".csv,text/csv"
                  disabled={importContacts.isPending}
                  onChange={event => setFile(event.target.files?.[0] ?? null)}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor={ids.list}>{t('contacts.importList')}</Label>
                <select
                  id={ids.list}
                  className={cn(nativeSelectClassName, 'w-full')}
                  value={listId === undefined ? '' : String(listId)}
                  disabled={importContacts.isPending}
                  onChange={event =>
                    setListId(event.target.value === '' ? undefined : Number(event.target.value))
                  }
                >
                  <option value="">{t('contacts.importNoList')}</option>
                  {lists.map(list => (
                    <option key={list.id} value={String(list.id)}>
                      {list.name}
                    </option>
                  ))}
                </select>
              </div>
              {errorKey ? (
                <Alert variant="destructive">
                  <AlertCircle className="size-4" aria-hidden="true" />
                  <AlertDescription>{t(errorKey)}</AlertDescription>
                </Alert>
              ) : null}
            </DialogBody>
            <DialogFooter className="mt-4">
              <Button
                type="button"
                variant="outline"
                disabled={importContacts.isPending}
                onClick={onClose}
              >
                {t('actions.cancel')}
              </Button>
              <Button
                type="submit"
                loading={importContacts.isPending}
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
