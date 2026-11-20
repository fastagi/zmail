'use client';

import { useState } from 'react';
import { AlertCircle } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { MembersSection, type Run } from '@/features/mailbox/components/mailbox-access-panel';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useOrganizationPeople, useUserNames } from '@/features/mailbox/components/mailbox-people';
import { useMailboxMembers } from '@/features/mailbox/hooks/use-mailbox';
import type { MailboxDirectoryEntry } from '@/features/mailbox/types';
import {
  isAccessConflict,
  isPermissionDenied,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/** A side drawer with the members of a shared mailbox and their permissions. */
export function MailboxMembersDrawer({
  mailbox,
  onClose,
}: {
  mailbox: Pick<MailboxDirectoryEntry, 'id' | 'address'> | null;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Sheet open={mailbox !== null} onOpenChange={open => !open && onClose()}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-lg">
        {mailbox ? (
          <>
            <SheetHeader>
              <SheetTitle>{t('admin.members.title', { address: mailbox.address })}</SheetTitle>
              <SheetDescription>{t('admin.members.description')}</SheetDescription>
            </SheetHeader>
            <DrawerBody key={mailbox.id} mailboxId={mailbox.id} />
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

function DrawerBody({ mailboxId }: { mailboxId: number }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const people = useOrganizationPeople(organizationId, true);
  const nameOf = useUserNames(people.items);
  const members = useMailboxMembers(organizationId, mailboxId, true);
  const [error, setError] = useState<MailboxErrorKey | null>(null);

  const run: Run = async (change, success) => {
    setError(null);
    try {
      await change();
      if (success) toast.success(success);
      return { ok: true };
    } catch (failure) {
      const key = resolveMailboxErrorKey(failure);
      setError(key);
      return { ok: false, key, stale: isAccessConflict(failure) || isPermissionDenied(failure) };
    }
  };

  return (
    <div className="space-y-4 px-4 pb-6">
      {error ? (
        <Alert variant="destructive" role="alert">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>
            {t(error === 'errors.accessConflict' ? 'access.conflict' : error)}
          </AlertDescription>
        </Alert>
      ) : null}
      <MembersSection
        mailbox={{ id: mailboxId }}
        members={members}
        people={people.items}
        nameOf={nameOf}
        run={run}
      />
    </div>
  );
}
