'use client';

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import { CreateMailboxForm } from '@/features/mailbox/components/create-mailbox-form';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/**
 * Creates a shared mailbox: the mailbox form without the type choice. The API answers
 * `409 MAILBOX.LIMIT_REACHED` when the organization is full.
 */
export function CreateSharedMailboxDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open={open} onOpenChange={next => !next && onClose()}>
      <DialogContent size="lg" className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t('admin.createDialog.title')}</DialogTitle>
          <DialogDescription>{t('admin.createDialog.description')}</DialogDescription>
        </DialogHeader>
        {open ? <CreateBody onClose={onClose} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function CreateBody({ onClose }: { onClose: () => void }) {
  const t = useT('mailbox');
  const domains = useMailboxDomains(useMailOrganizationId());
  if (domains.isPending) return <Skeleton className="h-48 w-full" aria-hidden="true" />;
  const list = domains.data ?? [];
  const domain = list.find(item => item.kind === 'shared') ?? list[0];
  if (domains.error || !domain) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(domains.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void domains.refetch()}
      />
    );
  }
  return (
    <CreateMailboxForm domain={domain} domains={list} fixedType="shared" onCreated={onClose} />
  );
}
