'use client';

import type { ReactNode } from 'react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';

// Kept apart from mail-shared so pages that never confirm do not load the dialog primitive.
/** Confirms an action that cannot be undone; stays open while it runs. */
export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel,
  pending,
  icon,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  description: string;
  confirmLabel: string;
  cancelLabel: string;
  pending: boolean;
  icon?: ReactNode;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={next => !next && !pending && onCancel()}>
      <DialogContent size="sm" role="alertdialog">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={pending} onClick={onCancel}>
            {cancelLabel}
          </Button>
          <Button
            type="button"
            variant="destructive"
            loading={pending}
            icon={icon}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
