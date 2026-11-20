'use client';

import { useId, useState, type FormEvent } from 'react';
import { toast } from 'sonner';

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
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useRenameMailbox } from '@/features/mailbox/hooks/use-mailbox-admin';
import { MAILBOX_DISPLAY_NAME_MAX_LENGTH, normalizeDisplayName } from '@/features/mailbox/schemas';
import type { MailboxDirectoryEntry } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/** Changes the display name of a mailbox; an empty name clears it. */
export function RenameMailboxDialog({
  mailbox,
  onClose,
}: {
  mailbox: Pick<MailboxDirectoryEntry, 'id' | 'address' | 'display_name'> | null;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open={mailbox !== null} onOpenChange={open => !open && onClose()}>
      <DialogContent size="sm">
        {mailbox ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('admin.rename.title')}</DialogTitle>
              <DialogDescription>
                {t('admin.rename.description', { address: mailbox.address })}
              </DialogDescription>
            </DialogHeader>
            <RenameForm key={mailbox.id} mailbox={mailbox} onClose={onClose} />
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function RenameForm({
  mailbox,
  onClose,
}: {
  mailbox: Pick<MailboxDirectoryEntry, 'id' | 'display_name'>;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const rename = useRenameMailbox(useMailOrganizationId());
  const inputId = useId();
  const [name, setName] = useState(mailbox.display_name);
  const [error, setError] = useState<string | null>(null);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const displayName = normalizeDisplayName(name);
    if (displayName === null) {
      setError(t('create.displayNameInvalid'));
      return;
    }
    setError(null);
    try {
      await rename.mutateAsync({ mailboxId: mailbox.id, displayName });
      toast.success(t('admin.rename.saved'));
      onClose();
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <form className="space-y-4" onSubmit={event => void submit(event)} noValidate>
      <div className="space-y-2">
        <Label htmlFor={inputId}>{t('admin.rename.label')}</Label>
        <Input
          id={inputId}
          value={name}
          maxLength={MAILBOX_DISPLAY_NAME_MAX_LENGTH * 2}
          autoComplete="off"
          aria-invalid={error !== null}
          onChange={event => setName(event.target.value)}
        />
        {error ? (
          <p role="alert" className="text-sm text-error">
            {error}
          </p>
        ) : null}
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" disabled={rename.isPending} onClick={onClose}>
          {t('actions.cancel')}
        </Button>
        <Button type="submit" loading={rename.isPending}>
          {t('actions.save')}
        </Button>
      </DialogFooter>
    </form>
  );
}
