'use client';

import { useEffect, useState } from 'react';
import { Download, Inbox, MoreHorizontal, Pencil, Plus, Trash2, Upload, Users } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { CreateSharedMailboxDialog } from '@/features/mailbox/components/admin/create-shared-mailbox-dialog';
import { DeleteMailboxDialog } from '@/features/mailbox/components/admin/delete-mailbox-dialog';
import { ImportMailboxesDialog } from '@/features/mailbox/components/admin/import-mailboxes-dialog';
import { MailboxMembersDrawer } from '@/features/mailbox/components/admin/mailbox-members-drawer';
import { RenameMailboxDialog } from '@/features/mailbox/components/admin/rename-mailbox-dialog';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import { EmptyState, ErrorAlert } from '@/features/mailbox/components/mail-shared';
import {
  useExportMailboxes,
  useMailboxDirectory,
} from '@/features/mailbox/hooks/use-mailbox-admin';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import type { MailboxDirectoryEntry, MailboxDirectoryFilters } from '@/features/mailbox/types';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/** How long typing pauses before the search runs. */
const SEARCH_DEBOUNCE_MS = 300;

type Dialog =
  | { kind: 'create' }
  | { kind: 'import' }
  | { kind: 'rename' | 'members' | 'delete'; mailbox: MailboxDirectoryEntry }
  | null;

/**
 * Admin page of the organization's shared mailboxes: the 24-hour send total, a searchable,
 * cursor-paged table, and create, rename, members, delete, import, and export. Owners and admins
 * only; the API remains the authority for every change.
 */
export function SharedMailboxesAdmin() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader title={t('admin.title')} description={t('admin.description')} scope={scope} />
      <MailOrganizationBoundary scope={scope}>{() => <SharedMailboxes />}</MailOrganizationBoundary>
    </div>
  );
}

function useDebounced(value: string, delay: number): string {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return debounced;
}

function SharedMailboxes() {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const [search, setSearch] = useState('');
  const query = useDebounced(search.trim(), SEARCH_DEBOUNCE_MS);
  // Cursors of the pages before the current one; the first page has none.
  const [cursors, setCursors] = useState<string[]>([]);
  const [searched, setSearched] = useState(query);
  const [dialog, setDialog] = useState<Dialog>(null);
  const exportMailboxes = useExportMailboxes(organizationId);

  // A new search starts again at the first page.
  if (searched !== query) {
    setSearched(query);
    setCursors([]);
  }

  const filters: MailboxDirectoryFilters =
    query === '' ? { type: 'shared' } : { type: 'shared', q: query };
  const cursor = cursors.at(-1);
  const directory = useMailboxDirectory(organizationId, filters, cursor);
  const meta = directory.data?.meta;
  const items = directory.data?.items ?? [];

  const runExport = async () => {
    try {
      await exportMailboxes.mutateAsync('shared');
      toast.success(t('admin.exported'));
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  if (isOrganizationLost(directory.error)) return <OrganizationLostState />;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground" title={t('admin.quotaHint')}>
          {meta
            ? t('admin.quota', { sent: meta.sent_last_24h, limit: meta.daily_send_limit })
            : null}
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<Download className="size-4" />}
            loading={exportMailboxes.isPending}
            onClick={() => void runExport()}
          >
            {t('admin.export')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<Upload className="size-4" />}
            onClick={() => setDialog({ kind: 'import' })}
          >
            {t('admin.import')}
          </Button>
          <Button
            type="button"
            size="sm"
            icon={<Plus className="size-4" />}
            onClick={() => setDialog({ kind: 'create' })}
          >
            {t('admin.create')}
          </Button>
        </div>
      </div>

      <Input
        type="search"
        aria-label={t('admin.search')}
        placeholder={t('admin.search')}
        className="max-w-sm"
        value={search}
        onChange={event => setSearch(event.target.value)}
      />

      {directory.isPending ? (
        <Skeleton className="h-48 w-full" aria-hidden="true" />
      ) : directory.error || !directory.data ? (
        <ErrorAlert
          message={t(resolveMailboxErrorKey(directory.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void directory.refetch()}
        />
      ) : items.length === 0 && cursors.length === 0 ? (
        <EmptyState
          icon={<Inbox className="size-5" aria-hidden="true" />}
          title={query === '' ? t('admin.emptyTitle') : t('admin.noMatches')}
          description={query === '' ? t('admin.empty') : undefined}
        />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('admin.columns.name')}</TableHead>
                <TableHead>{t('admin.columns.address')}</TableHead>
                <TableHead className="text-right">{t('admin.columns.members')}</TableHead>
                <TableHead className="text-right">{t('admin.columns.sent')}</TableHead>
                <TableHead className="text-right">{t('admin.columns.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map(mailbox => (
                <MailboxRow
                  key={mailbox.id}
                  mailbox={mailbox}
                  onAction={kind => setDialog({ kind, mailbox })}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {/* One page needs no pager. */}
      {cursors.length > 0 || meta?.has_more ? (
        <div className="flex items-center justify-end gap-2">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={cursors.length === 0}
            onClick={() => setCursors(previous => previous.slice(0, -1))}
          >
            {t('admin.previous')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={!meta?.has_more || !meta.next_cursor || directory.isFetching}
            onClick={() => {
              const next = meta?.next_cursor;
              if (next) setCursors(previous => [...previous, next]);
            }}
          >
            {t('admin.next')}
          </Button>
        </div>
      ) : null}

      <CreateSharedMailboxDialog open={dialog?.kind === 'create'} onClose={() => setDialog(null)} />
      <ImportMailboxesDialog open={dialog?.kind === 'import'} onClose={() => setDialog(null)} />
      <RenameMailboxDialog
        mailbox={dialog?.kind === 'rename' ? dialog.mailbox : null}
        onClose={() => setDialog(null)}
      />
      <MailboxMembersDrawer
        mailbox={dialog?.kind === 'members' ? dialog.mailbox : null}
        onClose={() => setDialog(null)}
      />
      <DeleteMailboxDialog
        mailbox={dialog?.kind === 'delete' ? dialog.mailbox : null}
        onClose={() => setDialog(null)}
        onDeleted={() => {
          // The page may now be empty; going back keeps the table useful.
          if (items.length === 1) setCursors(previous => previous.slice(0, -1));
        }}
      />
    </div>
  );
}

function MailboxRow({
  mailbox,
  onAction,
}: {
  mailbox: MailboxDirectoryEntry;
  onAction: (kind: 'rename' | 'members' | 'delete') => void;
}) {
  const t = useT('mailbox');
  return (
    <TableRow>
      <TableCell className="font-medium">
        {mailbox.display_name === '' ? (
          <span className="text-muted-foreground">{t('admin.noName')}</span>
        ) : (
          mailbox.display_name
        )}
      </TableCell>
      <TableCell className="font-mono text-xs">{mailbox.address}</TableCell>
      <TableCell className="text-right tabular-nums">{mailbox.member_count}</TableCell>
      <TableCell className="text-right tabular-nums">{mailbox.sent_last_24h}</TableCell>
      <TableCell className="text-right">
        <div className="flex items-center justify-end gap-1">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            icon={<Pencil className="size-3.5" />}
            aria-label={t('admin.editNameOf', { address: mailbox.address })}
            onClick={() => onAction('rename')}
          >
            {t('admin.editName')}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                isIcon
                noScale
                aria-label={t('admin.moreActions', { address: mailbox.address })}
                title={t('admin.moreActions', { address: mailbox.address })}
              >
                <MoreHorizontal className="size-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => onAction('members')}>
                <Users />
                {t('admin.manageMembers')}
              </DropdownMenuItem>
              <DropdownMenuItem variant="destructive" onSelect={() => onAction('delete')}>
                <Trash2 />
                {t('admin.delete')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </TableCell>
    </TableRow>
  );
}
