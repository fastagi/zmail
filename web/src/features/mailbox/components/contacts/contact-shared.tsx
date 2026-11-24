'use client';

import { Trash2 } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import type { MailContactImportSkipReason, MailContactStatus } from '@/features/mailbox/types';
import { useT } from '@/i18n';

export const statusKey = {
  subscribed: 'contacts.subscribed',
  unsubscribed: 'contacts.unsubscribed',
  suppressed: 'contacts.suppressed',
} as const satisfies Record<MailContactStatus, string>;

export const reasonKey = {
  invalid_email: 'contacts.reasonInvalidEmail',
  duplicate: 'contacts.reasonDuplicate',
  invalid_name: 'contacts.reasonInvalidName',
  invalid_property: 'contacts.reasonInvalidProperty',
  too_many_properties: 'contacts.reasonTooManyProperties',
} as const satisfies Record<MailContactImportSkipReason, string>;

export const statuses: readonly MailContactStatus[] = ['subscribed', 'unsubscribed', 'suppressed'];

export function StatusBadge({ status }: { status: MailContactStatus }) {
  const t = useT('mailbox');
  return (
    <Badge variant={status === 'subscribed' ? 'secondary' : 'outline'}>
      {t(statusKey[status])}
    </Badge>
  );
}

export function ConfirmDialog({
  title,
  description,
  pending,
  onCancel,
  onConfirm,
}: {
  title: string;
  description: string;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open onOpenChange={open => !open && !pending && onCancel()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={pending} onClick={onCancel}>
            {t('actions.cancel')}
          </Button>
          <Button
            type="button"
            variant="destructive"
            loading={pending}
            icon={<Trash2 className="size-4" />}
            onClick={onConfirm}
          >
            {t('actions.delete')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
