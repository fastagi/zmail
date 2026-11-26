'use client';

import type { ReactNode } from 'react';
import { Archive, Mail, MailOpen, Trash2, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  canArchiveInboxItem,
  canDeleteInboxItem,
  type InboxBulkAction,
} from '@/features/mailbox/components/inbox/use-inbox-actions';
import type { InboxItem, InboxView } from '@/features/mailbox/types';
import { useT } from '@/i18n';

/**
 * Gmail-style sticky bulk action bar shown above the unified inbox list whenever at least one
 * row is checked.
 */
export function InboxBulkBar({
  selectedItems,
  totalCount,
  sendable,
  view,
  busy = false,
  onToggleAll,
  onClear,
  onAction,
}: {
  selectedItems: readonly InboxItem[];
  totalCount: number;
  sendable: ReadonlySet<number>;
  view: InboxView;
  busy?: boolean;
  onToggleAll: () => void;
  onClear: () => void;
  onAction: (action: InboxBulkAction) => void;
}) {
  const t = useT('mailbox');
  if (selectedItems.length === 0) return null;

  const allSelected = totalCount > 0 && selectedItems.length >= totalCount;
  const canArchive = selectedItems.some(item => canArchiveInboxItem(item, sendable, view));
  const canDelete = selectedItems.some(item => canDeleteInboxItem(item, sendable));

  return (
    <div
      role="toolbar"
      aria-label={t('inbox.bulk.toolbarLabel', { count: selectedItems.length })}
      className="mx-3 mb-2 flex shrink-0 items-center gap-1.5 rounded-[10px] border border-blue-200/80 bg-blue-50/90 px-2.5 py-1.5 text-xs shadow-xs dark:border-blue-800/80 dark:bg-blue-950/70"
    >
      <Checkbox
        aria-label={allSelected ? t('inbox.bulk.clear') : t('inbox.bulk.selectAll')}
        title={allSelected ? t('inbox.bulk.clear') : t('inbox.bulk.selectAll')}
        checked={allSelected ? true : 'indeterminate'}
        disabled={busy}
        onCheckedChange={() => onToggleAll()}
      />
      <span className="ml-1 font-medium text-text-main tabular-nums">
        {t('inbox.bulk.selected', { count: selectedItems.length })}
      </span>
      <span className="flex-1" />
      <div className="flex items-center gap-0.5">
        <BulkIconButton
          label={t('actions.markRead')}
          disabled={busy}
          onClick={() => onAction('markRead')}
        >
          <MailOpen className="size-3.5" />
        </BulkIconButton>
        <BulkIconButton
          label={t('actions.markUnread')}
          disabled={busy}
          onClick={() => onAction('markUnread')}
        >
          <Mail className="size-3.5" />
        </BulkIconButton>
        {canArchive ? (
          <BulkIconButton
            label={t('actions.archive')}
            disabled={busy}
            onClick={() => onAction('archive')}
          >
            <Archive className="size-3.5" />
          </BulkIconButton>
        ) : null}
        {canDelete ? (
          <BulkIconButton
            label={t('actions.delete')}
            disabled={busy}
            onClick={() => onAction('trash')}
          >
            <Trash2 className="size-3.5" />
          </BulkIconButton>
        ) : null}
        <BulkIconButton label={t('inbox.bulk.clear')} disabled={busy} onClick={onClear}>
          <X className="size-3.5" />
        </BulkIconButton>
      </div>
    </div>
  );
}

function BulkIconButton({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string;
  disabled: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <Button
      type="button"
      isIcon
      size="xs"
      variant="ghost"
      aria-label={label}
      title={label}
      disabled={disabled}
      className="text-text-subtle hover:bg-blue-100/80 hover:text-text-main dark:hover:bg-blue-900/70"
      onClick={onClick}
    >
      {children}
    </Button>
  );
}
