'use client';

import { useDeferredValue, useId, useState, type FormEvent } from 'react';
import { AlertCircle, Pencil, Plus, Search, Trash2, Upload, Users, X } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  nativeSelectClassName,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import {
  useDeleteMailContact,
  useDeleteMailContactList,
  useImportMailContacts,
  useMailContactLists,
  useMailContacts,
  useSaveMailContact,
  useSaveMailContactList,
} from '@/features/mailbox/hooks/use-mail-contacts';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import {
  MAIL_CONTACT_IMPORT_MAX_BYTES,
  MAIL_CONTACT_LIST_NAME_MAX_LENGTH,
  MAIL_CONTACT_NAME_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import type {
  MailContact,
  MailContactImport,
  MailContactImportSkipReason,
  MailContactList,
  MailContactStatus,
} from '@/features/mailbox/types';
import {
  isOrganizationLost,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const statusKey = {
  subscribed: 'contacts.subscribed',
  unsubscribed: 'contacts.unsubscribed',
  suppressed: 'contacts.suppressed',
} as const satisfies Record<MailContactStatus, string>;

const reasonKey = {
  invalid_email: 'contacts.reasonInvalidEmail',
  duplicate: 'contacts.reasonDuplicate',
  invalid_name: 'contacts.reasonInvalidName',
  invalid_property: 'contacts.reasonInvalidProperty',
  too_many_properties: 'contacts.reasonTooManyProperties',
} as const satisfies Record<MailContactImportSkipReason, string>;

const statuses: readonly MailContactStatus[] = ['subscribed', 'unsubscribed', 'suppressed'];

/**
 * Contacts console: the organization's contacts with search and filters, lists on the side, a
 * contact editor, and CSV import with a per-row report.
 */
export function MailContacts() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader
        title={t('contacts.title')}
        description={t('contacts.description')}
        scope={scope}
      />
      <MailOrganizationBoundary scope={scope}>
        {() => <ContactsWorkspace />}
      </MailOrganizationBoundary>
    </div>
  );
}

function ContactsWorkspace() {
  const organizationId = useMailOrganizationId();
  const lists = useMailContactLists(organizationId);
  const [listId, setListId] = useState<number | undefined>(undefined);
  const [query, setQuery] = useState('');
  const [status, setStatus] = useState<MailContactStatus | undefined>(undefined);
  const [page, setPage] = useState(1);
  const [editing, setEditing] = useState<MailContact | 'new' | null>(null);
  const [importing, setImporting] = useState(false);
  const deferredQuery = useDeferredValue(query.trim());
  const filters = {
    ...(deferredQuery === '' ? {} : { q: deferredQuery }),
    ...(status ? { status } : {}),
    ...(listId === undefined ? {} : { listId }),
  };
  const contacts = useMailContacts(organizationId, filters, page);
  const allLists = lists.data ?? [];
  const selectedList = allLists.find(list => list.id === listId) ?? null;

  if (contacts.error && isOrganizationLost(contacts.error)) return <OrganizationLostState />;

  return (
    <div className="grid gap-6 lg:grid-cols-[15rem_minmax(0,1fr)]">
      <ListsPanel
        lists={allLists}
        loading={lists.isPending}
        selectedId={listId}
        onSelect={id => {
          setListId(id);
          setPage(1);
        }}
      />
      <section className="min-w-0 space-y-4">
        <ContactsToolbar
          query={query}
          status={status}
          onQuery={value => {
            setQuery(value);
            setPage(1);
          }}
          onStatus={value => {
            setStatus(value);
            setPage(1);
          }}
          onAdd={() => setEditing('new')}
          onImport={() => setImporting(true)}
        />
        {selectedList ? (
          <SelectedListActions
            list={selectedList}
            onDeleted={() => {
              setListId(undefined);
              setPage(1);
            }}
          />
        ) : null}
        <ContactsTable
          query={contacts}
          lists={allLists}
          filtered={Object.keys(filters).length > 0}
          onEdit={contact => setEditing(contact)}
          onAdd={() => setEditing('new')}
          onPage={setPage}
        />
      </section>
      {editing ? (
        <ContactDialog
          contact={editing === 'new' ? null : editing}
          lists={allLists}
          defaultListId={listId}
          onClose={() => setEditing(null)}
        />
      ) : null}
      {importing ? (
        <ImportDialog lists={allLists} defaultListId={listId} onClose={() => setImporting(false)} />
      ) : null}
    </div>
  );
}

function ListsPanel({
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
        {count === null ? null : <span className="text-xs text-muted-foreground">{count}</span>}
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
          icon={<Plus className="size-4" />}
          aria-label={t('contacts.addList')}
        />
      </form>
    </nav>
  );
}

function SelectedListActions({
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
            value={name}
            maxLength={MAIL_CONTACT_LIST_NAME_MAX_LENGTH}
            disabled={save.isPending}
            onChange={event => setName(event.target.value)}
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
          <span className="font-medium">{list.name}</span>
          <div className="flex gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Pencil className="size-3.5" />}
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
              icon={<Trash2 className="size-3.5" />}
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

function ContactsToolbar({
  query,
  status,
  onQuery,
  onStatus,
  onAdd,
  onImport,
}: {
  query: string;
  status: MailContactStatus | undefined;
  onQuery: (value: string) => void;
  onStatus: (value: MailContactStatus | undefined) => void;
  onAdd: () => void;
  onImport: () => void;
}) {
  const t = useT('mailbox');
  const searchId = useId();
  const statusId = useId();
  return (
    <div className="flex flex-wrap items-end gap-3">
      <div className="relative min-w-48 flex-1">
        <Label htmlFor={searchId} className="sr-only">
          {t('contacts.search')}
        </Label>
        <Search
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          id={searchId}
          type="search"
          value={query}
          className="pl-9"
          placeholder={t('contacts.searchPlaceholder')}
          onChange={event => onQuery(event.target.value)}
        />
      </div>
      <div>
        <Label htmlFor={statusId} className="sr-only">
          {t('contacts.statusFilter')}
        </Label>
        <select
          id={statusId}
          className={nativeSelectClassName}
          value={status ?? ''}
          onChange={event =>
            onStatus(
              event.target.value === '' ? undefined : (event.target.value as MailContactStatus)
            )
          }
        >
          <option value="">{t('contacts.allStatuses')}</option>
          {statuses.map(value => (
            <option key={value} value={value}>
              {t(statusKey[value])}
            </option>
          ))}
        </select>
      </div>
      <Button
        type="button"
        variant="outline"
        icon={<Upload className="size-4" />}
        onClick={onImport}
      >
        {t('contacts.import')}
      </Button>
      <Button type="button" icon={<Plus className="size-4" />} onClick={onAdd}>
        {t('contacts.add')}
      </Button>
    </div>
  );
}

function StatusBadge({ status }: { status: MailContactStatus }) {
  const t = useT('mailbox');
  return (
    <Badge variant={status === 'subscribed' ? 'secondary' : 'outline'}>
      {t(statusKey[status])}
    </Badge>
  );
}

function ContactsTable({
  query,
  lists,
  filtered,
  onEdit,
  onAdd,
  onPage,
}: {
  query: ReturnType<typeof useMailContacts>;
  lists: MailContactList[];
  filtered: boolean;
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
        icon={<Search className="size-5" />}
        title={t('contacts.noMatchTitle')}
        description={t('contacts.noMatchDescription')}
      />
    ) : (
      <EmptyState
        icon={<Users className="size-5" />}
        title={t('contacts.emptyTitle')}
        description={t('contacts.emptyDescription')}
        action={
          <Button type="button" icon={<Plus className="size-4" />} onClick={onAdd}>
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
            {items.map(contact => (
              <TableRow key={contact.id}>
                <TableCell className="max-w-64 truncate">
                  <button
                    type="button"
                    className="max-w-full truncate text-left hover:underline focus-ring"
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
                  <span className="flex flex-wrap gap-1">
                    {contact.list_ids.map(id =>
                      listNames.has(id) ? (
                        <Badge key={id} variant="outline" className="text-xs">
                          {listNames.get(id)}
                        </Badge>
                      ) : null
                    )}
                  </span>
                </TableCell>
                <TableCell className="text-right whitespace-nowrap text-muted-foreground">
                  <time dateTime={contact.created_at}>
                    {formatter.format(new Date(contact.created_at))}
                  </time>
                </TableCell>
              </TableRow>
            ))}
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

interface PropertyRow {
  key: number;
  name: string;
  value: string;
}

function ContactDialog({
  contact,
  lists,
  defaultListId,
  onClose,
}: {
  contact: MailContact | null;
  lists: MailContactList[];
  defaultListId: number | undefined;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const save = useSaveMailContact(organizationId);
  const remove = useDeleteMailContact(organizationId);
  const [email, setEmail] = useState(contact?.email ?? '');
  const [firstName, setFirstName] = useState(contact?.first_name ?? '');
  const [lastName, setLastName] = useState(contact?.last_name ?? '');
  const [status, setStatus] = useState<MailContactStatus>(contact?.status ?? 'subscribed');
  const [listIds, setListIds] = useState<number[]>(
    contact?.list_ids ?? (defaultListId === undefined ? [] : [defaultListId])
  );
  const [rows, setRows] = useState<PropertyRow[]>(() =>
    Object.entries(contact?.properties ?? {}).map(([name, value], key) => ({ key, name, value }))
  );
  const [nextKey, setNextKey] = useState(rows.length);
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(null);
  const [deleting, setDeleting] = useState(false);
  const ids = { email: useId(), first: useId(), last: useId(), status: useId(), base: useId() };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorKey(null);
    const properties = Object.fromEntries(
      rows.map(row => [row.name.trim(), row.value] as const).filter(([name]) => name !== '')
    );
    try {
      await save.mutateAsync({
        input: {
          ...(contact ? {} : { email }),
          first_name: firstName,
          last_name: lastName,
          properties,
          // A suppressed contact keeps its status unless a member picks another one.
          ...(status === 'suppressed' ? {} : { status }),
          list_ids: listIds,
        },
        ...(contact ? { contactId: contact.id } : {}),
      });
      toast.success(t(contact ? 'contacts.saved' : 'contacts.created'));
      onClose();
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
    }
  };

  const confirmDelete = async () => {
    if (!contact) return;
    try {
      await remove.mutateAsync(contact.id);
      toast.success(t('contacts.deleted'));
      setDeleting(false);
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const pending = save.isPending || remove.isPending;

  return (
    <Dialog open onOpenChange={open => !open && !pending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(contact ? 'contacts.editTitle' : 'contacts.addTitle')}</DialogTitle>
          <DialogDescription>
            {contact ? t('contacts.emailFixed') : t('contacts.description')}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit}>
          <DialogBody className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor={ids.email}>{t('contacts.email')}</Label>
              <Input
                id={ids.email}
                type="email"
                value={email}
                readOnly={contact !== null}
                spellCheck={false}
                autoComplete="off"
                disabled={pending}
                onChange={event => setEmail(event.target.value)}
              />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor={ids.first}>{t('contacts.firstName')}</Label>
                <Input
                  id={ids.first}
                  value={firstName}
                  maxLength={MAIL_CONTACT_NAME_MAX_LENGTH}
                  disabled={pending}
                  onChange={event => setFirstName(event.target.value)}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor={ids.last}>{t('contacts.lastName')}</Label>
                <Input
                  id={ids.last}
                  value={lastName}
                  maxLength={MAIL_CONTACT_NAME_MAX_LENGTH}
                  disabled={pending}
                  onChange={event => setLastName(event.target.value)}
                />
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor={ids.status}>{t('contacts.status')}</Label>
              <select
                id={ids.status}
                className={cn(nativeSelectClassName, 'w-full')}
                value={status}
                disabled={pending}
                onChange={event => setStatus(event.target.value as MailContactStatus)}
              >
                <option value="subscribed">{t('contacts.subscribed')}</option>
                <option value="unsubscribed">{t('contacts.unsubscribed')}</option>
                {contact?.status === 'suppressed' ? (
                  <option value="suppressed">{t('contacts.suppressed')}</option>
                ) : null}
              </select>
              {contact?.status === 'suppressed' ? (
                <p className="text-xs text-muted-foreground">{t('contacts.suppressedNote')}</p>
              ) : null}
            </div>
            <fieldset className="space-y-2">
              <legend className="text-sm font-medium">{t('contacts.listsField')}</legend>
              {lists.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t('contacts.noLists')}</p>
              ) : (
                <div className="flex flex-wrap gap-x-4 gap-y-2">
                  {lists.map(list => (
                    <div key={list.id} className="flex items-center gap-2">
                      <Checkbox
                        id={`${ids.base}-list-${list.id}`}
                        checked={listIds.includes(list.id)}
                        disabled={pending}
                        onCheckedChange={checked =>
                          setListIds(current =>
                            checked === true
                              ? [...current, list.id]
                              : current.filter(id => id !== list.id)
                          )
                        }
                      />
                      <Label htmlFor={`${ids.base}-list-${list.id}`} className="font-normal">
                        {list.name}
                      </Label>
                    </div>
                  ))}
                </div>
              )}
            </fieldset>
            <fieldset className="space-y-2">
              <legend className="text-sm font-medium">{t('contacts.properties')}</legend>
              <p className="text-xs text-muted-foreground">{t('contacts.propertiesHint')}</p>
              {rows.map(row => (
                <div key={row.key} className="flex items-center gap-2">
                  <Input
                    aria-label={t('contacts.propertyName')}
                    value={row.name}
                    className="font-mono text-xs"
                    disabled={pending}
                    onChange={event =>
                      setRows(current =>
                        current.map(item =>
                          item.key === row.key ? { ...item, name: event.target.value } : item
                        )
                      )
                    }
                  />
                  <Input
                    aria-label={t('contacts.propertyValue')}
                    value={row.value}
                    disabled={pending}
                    onChange={event =>
                      setRows(current =>
                        current.map(item =>
                          item.key === row.key ? { ...item, value: event.target.value } : item
                        )
                      )
                    }
                  />
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    isIcon
                    icon={<X className="size-3.5" />}
                    aria-label={t('contacts.removeProperty', { name: row.name || '-' })}
                    disabled={pending}
                    onClick={() => setRows(current => current.filter(item => item.key !== row.key))}
                  />
                </div>
              ))}
              <Button
                type="button"
                size="xs"
                variant="outline"
                icon={<Plus className="size-3.5" />}
                disabled={pending}
                onClick={() => {
                  setRows(current => [...current, { key: nextKey, name: '', value: '' }]);
                  setNextKey(key => key + 1);
                }}
              >
                {t('contacts.addProperty')}
              </Button>
            </fieldset>
            {errorKey ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{t(errorKey)}</AlertDescription>
              </Alert>
            ) : null}
          </DialogBody>
          <DialogFooter className="mt-4 sm:justify-between">
            {contact ? (
              <Button
                type="button"
                variant="outline"
                icon={<Trash2 className="size-4" />}
                disabled={pending}
                onClick={() => setDeleting(true)}
              >
                {t('contacts.delete')}
              </Button>
            ) : (
              <span />
            )}
            <div className="flex gap-2">
              <Button type="button" variant="outline" disabled={pending} onClick={onClose}>
                {t('actions.cancel')}
              </Button>
              <Button type="submit" loading={save.isPending}>
                {t('contacts.save')}
              </Button>
            </div>
          </DialogFooter>
        </form>
        {deleting && contact ? (
          <ConfirmDialog
            title={t('contacts.deleteTitle')}
            description={t('contacts.deleteDescription', { email: contact.email })}
            pending={remove.isPending}
            onCancel={() => setDeleting(false)}
            onConfirm={() => void confirmDelete()}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function ImportDialog({
  lists,
  defaultListId,
  onClose,
}: {
  lists: MailContactList[];
  defaultListId: number | undefined;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const importContacts = useImportMailContacts(useMailOrganizationId());
  const [file, setFile] = useState<File | null>(null);
  const [listId, setListId] = useState<number | undefined>(defaultListId);
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | 'contacts.importTooLarge' | null>(
    null
  );
  const [result, setResult] = useState<MailContactImport | null>(null);
  const ids = { file: useId(), list: useId() };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorKey(null);
    if (!file) return;
    if (file.size > MAIL_CONTACT_IMPORT_MAX_BYTES) {
      setErrorKey('contacts.importTooLarge');
      return;
    }
    try {
      const csv = await file.text();
      const imported = await importContacts.mutateAsync({
        csv,
        ...(listId === undefined ? {} : { list_id: listId }),
      });
      setResult(imported);
      toast.success(t('contacts.importDone'));
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !importContacts.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('contacts.importTitle')}</DialogTitle>
          <DialogDescription>{t('contacts.importDescription')}</DialogDescription>
        </DialogHeader>
        {result ? (
          <>
            <DialogBody className="space-y-3">
              <ImportReport result={result} />
            </DialogBody>
            <DialogFooter className="mt-4">
              <Button type="button" onClick={onClose}>
                {t('contacts.close')}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit}>
            <DialogBody className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor={ids.file}>{t('contacts.importFile')}</Label>
                <Input
                  id={ids.file}
                  type="file"
                  accept=".csv,text/csv"
                  disabled={importContacts.isPending}
                  onChange={event => setFile(event.target.files?.[0] ?? null)}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor={ids.list}>{t('contacts.importList')}</Label>
                <select
                  id={ids.list}
                  className={cn(nativeSelectClassName, 'w-full')}
                  value={listId === undefined ? '' : String(listId)}
                  disabled={importContacts.isPending}
                  onChange={event =>
                    setListId(event.target.value === '' ? undefined : Number(event.target.value))
                  }
                >
                  <option value="">{t('contacts.importNoList')}</option>
                  {lists.map(list => (
                    <option key={list.id} value={String(list.id)}>
                      {list.name}
                    </option>
                  ))}
                </select>
              </div>
              {errorKey ? (
                <Alert variant="destructive">
                  <AlertCircle className="size-4" aria-hidden="true" />
                  <AlertDescription>{t(errorKey)}</AlertDescription>
                </Alert>
              ) : null}
            </DialogBody>
            <DialogFooter className="mt-4">
              <Button
                type="button"
                variant="outline"
                disabled={importContacts.isPending}
                onClick={onClose}
              >
                {t('actions.cancel')}
              </Button>
              <Button
                type="submit"
                loading={importContacts.isPending}
                disabled={!file}
                icon={<Upload className="size-4" />}
              >
                {t('contacts.importRun')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

function ImportReport({ result }: { result: MailContactImport }) {
  const t = useT('mailbox');
  const unlisted = result.skipped_count - result.skipped.length;
  return (
    <div className="space-y-3 text-sm">
      <p role="status">
        {t('contacts.importSummary', {
          rows: result.rows,
          created: result.created,
          updated: result.updated,
          skipped: result.skipped_count,
        })}
      </p>
      {result.ignored_columns.length > 0 ? (
        <p className="text-muted-foreground">
          {t('contacts.importIgnored', { columns: result.ignored_columns.join(', ') })}
        </p>
      ) : null}
      {result.skipped.length > 0 ? (
        <section className="space-y-2">
          <h3 className="font-medium">{t('contacts.importSkipped')}</h3>
          <ul className="max-h-60 divide-y overflow-auto rounded-md border">
            {result.skipped.map(skip => (
              <li key={skip.line} className="flex justify-between gap-3 px-3 py-1.5">
                <span className="whitespace-nowrap">
                  {t('contacts.importLine', { line: skip.line })}
                </span>
                <span className="text-muted-foreground">{t(reasonKey[skip.reason])}</span>
              </li>
            ))}
          </ul>
          {unlisted > 0 ? (
            <p className="text-muted-foreground">{t('contacts.importMore', { count: unlisted })}</p>
          ) : null}
        </section>
      ) : null}
    </div>
  );
}

function ConfirmDialog({
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
