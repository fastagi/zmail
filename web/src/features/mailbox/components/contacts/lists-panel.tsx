'use client';

import { useId, useState, type FormEvent } from 'react';
import { Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  useDeleteMailContactList,
  useSaveMailContactList,
} from '@/features/mailbox/hooks/use-mail-contacts';
import { MAIL_CONTACT_LIST_NAME_MAX_LENGTH } from '@/features/mailbox/schemas';
import type { MailContactList } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

import { ConfirmDialog } from './contact-shared';

export function ListsPanel({
  lists,
  loading,
  selectedId,
  onSelect,
}: {
  lists: MailContactList[];
  loading: boolean;
  selectedId: number | undefined;
  onSelect: (listId: number | undefined) => void;
}) {
  const t = useT('mailbox');
  const save = useSaveMailContactList(useMailOrganizationId());
  const [name, setName] = useState('');
  const inputId = useId();

  const create = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (name.trim() === '') return;
    try {
      const list = await save.mutateAsync({ name });
      setName('');
      toast.success(t('contacts.listCreated'));
      onSelect(list.id);
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const item = (label: string, count: number | null, id: number | undefined) => (
    <li key={id ?? 'all'}>
      <button
        type="button"
        aria-current={selectedId === id ? 'true' : undefined}
        className={cn(
          'flex w-full items-center justify-between gap-2 rounded-md px-3 py-1.5 text-left text-sm focus-ring',
          selectedId === id ? 'bg-muted font-medium' : 'hover:bg-muted/60'
        )}
        onClick={() => onSelect(id)}
      >
        <span className="min-w-0 truncate">{label}</span>
        {count === null ? null : (
          <Badge
            variant={selectedId === id ? 'secondary' : 'outline'}
            className="shrink-0 px-1.5 py-0 text-xs tabular-nums"
          >
            {count}
          </Badge>
        )}
      </button>
    </li>
  );

  return (
    <nav aria-label={t('contacts.lists')} className="space-y-3">
      <ul className="space-y-0.5">{item(t('contacts.allContacts'), null, undefined)}</ul>
      <div className="space-y-1">
        <h2 className="px-3 text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {t('contacts.lists')}
        </h2>
        {loading ? (
          <Skeleton className="h-16 w-full" aria-hidden="true" />
        ) : lists.length === 0 ? (
          <p className="px-3 py-1.5 text-xs text-muted-foreground">{t('contacts.noLists')}</p>
        ) : (
          <ul className="space-y-0.5">
            {lists.map(list => item(list.name, list.contact_count, list.id))}
          </ul>
        )}
      </div>
      <form onSubmit={create} className="flex gap-2 px-1">
        <Label htmlFor={inputId} className="sr-only">
          {t('contacts.listName')}
        </Label>
        <Input
          id={inputId}
          value={name}
          maxLength={MAIL_CONTACT_LIST_NAME_MAX_LENGTH}
          placeholder={t('contacts.listName')}
          disabled={save.isPending}
          onChange={event => setName(event.target.value)}
        />
        <Button
          type="submit"
          size="sm"
          variant="outline"
          isIcon
          loading={save.isPending}
          icon={<Plus className="size-4" aria-hidden="true" />}
          aria-label={t('contacts.addList')}
        />
      </form>
    </nav>
  );
}

export function SelectedListActions({
  list,
  onDeleted,
}: {
  list: MailContactList;
  onDeleted: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const save = useSaveMailContactList(organizationId);
  const remove = useDeleteMailContactList(organizationId);
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState(list.name);
  const [deleting, setDeleting] = useState(false);
  const inputId = useId();

  const rename = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    try {
      await save.mutateAsync({ name, listId: list.id });
      toast.success(t('contacts.listSaved'));
      setRenaming(false);
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const confirmDelete = async () => {
    try {
      await remove.mutateAsync(list.id);
      toast.success(t('contacts.listDeleted'));
      setDeleting(false);
      onDeleted();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2">
      {renaming ? (
        <form onSubmit={rename} className="flex flex-1 items-center gap-2">
          <Label htmlFor={inputId} className="sr-only">
            {t('contacts.listName')}
          </Label>
          <Input
            id={inputId}
            autoFocus
            value={name}
            maxLength={MAIL_CONTACT_LIST_NAME_MAX_LENGTH}
            disabled={save.isPending}
            onChange={event => setName(event.target.value)}
            onKeyDown={event => {
              if (event.key === 'Escape') {
                event.preventDefault();
                setRenaming(false);
              }
            }}
          />
          <Button type="submit" size="sm" loading={save.isPending}>
            {t('contacts.save')}
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={() => setRenaming(false)}>
            {t('actions.cancel')}
          </Button>
        </form>
      ) : (
        <>
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate font-medium">{list.name}</span>
            <Badge variant="secondary" className="shrink-0 text-xs tabular-nums">
              {list.contact_count}
            </Badge>
          </div>
          <div className="flex gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Pencil className="size-3.5" aria-hidden="true" />}
              onClick={() => {
                setName(list.name);
                setRenaming(true);
              }}
            >
              {t('contacts.renameList')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Trash2 className="size-3.5" aria-hidden="true" />}
              onClick={() => setDeleting(true)}
            >
              {t('contacts.deleteList')}
            </Button>
          </div>
        </>
      )}
      {deleting ? (
        <ConfirmDialog
          title={t('contacts.deleteListTitle')}
          description={t('contacts.deleteListDescription', { name: list.name })}
          pending={remove.isPending}
          onCancel={() => setDeleting(false)}
          onConfirm={() => void confirmDelete()}
        />
      ) : null}
    </div>
  );
}
