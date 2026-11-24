'use client';

import { Plus, Search, Users, X } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  EmptyState,
  ErrorAlert,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import type { useMailContacts } from '@/features/mailbox/hooks/use-mail-contacts';
import type { MailContact, MailContactList } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

import { StatusBadge } from './contact-shared';

export function ContactsTable({
  query,
  lists,
  selectedListId,
  filtered,
  onClearFilters,
  onEdit,
  onAdd,
  onPage,
}: {
  query: ReturnType<typeof useMailContacts>;
  lists: MailContactList[];
  selectedListId: number | undefined;
  filtered: boolean;
  onClearFilters: () => void;
  onEdit: (contact: MailContact) => void;
  onAdd: () => void;
  onPage: (page: number) => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const listNames = new Map(lists.map(list => [list.id, list.name]));

  if (query.isPending) return <Skeleton className="h-64 w-full" aria-hidden="true" />;
  if (query.error || !query.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(query.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void query.refetch()}
      />
    );
  }
  const { items, meta } = query.data;
  if (items.length === 0) {
    return filtered ? (
      <EmptyState
        icon={<Search className="size-5" aria-hidden="true" />}
        title={t('contacts.noMatchTitle')}
        description={t('contacts.noMatchDescription')}
        action={
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<X className="size-3.5" aria-hidden="true" />}
            onClick={onClearFilters}
          >
            {t('emails.clearFilters')}
          </Button>
        }
      />
    ) : (
      <EmptyState
        icon={<Users className="size-5" aria-hidden="true" />}
        title={t('contacts.emptyTitle')}
        description={t('contacts.emptyDescription')}
        action={
          <Button
            type="button"
            icon={<Plus className="size-4" aria-hidden="true" />}
            onClick={onAdd}
          >
            {t('contacts.add')}
          </Button>
        }
      />
    );
  }

  return (
    <div className="space-y-3">
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('contacts.email')}</TableHead>
              <TableHead>{t('contacts.name')}</TableHead>
              <TableHead>{t('contacts.status')}</TableHead>
              <TableHead>{t('contacts.listsColumn')}</TableHead>
              <TableHead className="text-right">{t('contacts.added')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map(contact => {
              const assignedLists = contact.list_ids.filter(id => listNames.has(id));
              return (
                <TableRow key={contact.id}>
                  <TableCell className="max-w-64 truncate">
                    <button
                      type="button"
                      className="max-w-full truncate rounded-xs text-left hover:underline focus-ring"
                      aria-label={t('contacts.edit', { email: contact.email })}
                      onClick={() => onEdit(contact)}
                    >
                      {contact.email}
                    </button>
                  </TableCell>
                  <TableCell className="max-w-48 truncate text-muted-foreground">
                    {[contact.first_name, contact.last_name].filter(Boolean).join(' ')}
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={contact.status} />
                  </TableCell>
                  <TableCell>
                    {assignedLists.length > 0 ? (
                      <span className="flex flex-wrap gap-1">
                        {assignedLists.map(id => (
                          <Badge
                            key={id}
                            variant={selectedListId === id ? 'secondary' : 'outline'}
                            className="max-w-40 truncate text-xs"
                          >
                            {listNames.get(id)}
                          </Badge>
                        ))}
                      </span>
                    ) : (
                      <span className="text-muted-foreground" aria-hidden="true">
                        —
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-right whitespace-nowrap text-muted-foreground">
                    <time dateTime={contact.created_at}>
                      {formatter.format(new Date(contact.created_at))}
                    </time>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-sm text-muted-foreground">
          {t('contacts.range', { from: meta.from, to: meta.to, total: meta.total })}
        </span>
        {meta.last_page > 1 ? (
          <div className="flex items-center gap-3">
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={meta.current_page <= 1}
              onClick={() => onPage(meta.current_page - 1)}
            >
              {t('list.previous')}
            </Button>
            <span className="text-sm text-muted-foreground">
              {t('list.page', { page: meta.current_page, total: meta.last_page })}
            </span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={meta.current_page >= meta.last_page}
              onClick={() => onPage(meta.current_page + 1)}
            >
              {t('list.next')}
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  );
}
