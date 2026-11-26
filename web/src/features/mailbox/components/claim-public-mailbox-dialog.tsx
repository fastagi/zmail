'use client';

import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { ClaimPublicMailboxCard } from '@/features/mailbox/components/claim-public-mailbox-card';
import type { PublicMailboxOptions } from '@/features/mailbox/services/public-mailbox-service';
import type { Mailbox } from '@/features/mailbox/types';
import { useT } from '@/i18n';

export function ClaimPublicMailboxDialog({
  open,
  onOpenChange,
  organizationId,
  options,
  onClaimed,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  organizationId: number;
  options: PublicMailboxOptions;
  onClaimed: (mailbox: Mailbox) => void;
}) {
  const t = useT('mailbox');
  const domain = options.domains[0] ?? 'zmail.im';

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{t('inbox.publicMailbox.dialogTitle', { domain })}</DialogTitle>
          <DialogDescription>{t('inbox.publicMailbox.dialogDescription')}</DialogDescription>
        </DialogHeader>
        <DialogBody>
          <ClaimPublicMailboxCard
            options={options}
            organizationId={organizationId}
            variant="inline"
            onClaimed={mailbox => onClaimed(mailbox)}
          />
        </DialogBody>
      </DialogContent>
    </Dialog>
  );
}
