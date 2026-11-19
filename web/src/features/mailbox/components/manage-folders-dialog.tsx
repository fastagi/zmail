'use client';

import { useId, useState, type FormEvent } from 'react';
import { Check, Pencil, Plus, Trash2, X } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import { LabelDot } from '@/features/mailbox/components/mailbox-sidebar';
import {
  useCreateMailFolder,
  useDeleteMailFolder,
  useRenameMailFolder,
} from '@/features/mailbox/hooks/use-mailbox';
import { MAIL_FOLDER_NAME_MAX_LENGTH, MAIL_LABEL_COLORS } from '@/features/mailbox/schemas';
import type { MailFolder, MailLabelColor } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

export interface NamedItem {
  id: number;
  name: string;
  color?: MailLabelColor;
}

export interface NamedItemInput {
  name: string;
  color?: MailLabelColor;
}

/** The copy of one kind of named item; every key is in the `mailbox` namespace. */
interface NamedItemCopy {
  title: 'folderManager.title' | 'labelManager.title';
  description: 'folderManager.description' | 'labelManager.description';
  name: 'folderManager.name' | 'labelManager.name';
  newName: 'folderManager.newName' | 'labelManager.newName';
  add: 'folderManager.add' | 'labelManager.add';
  empty: 'folderManager.empty' | 'labelManager.empty';
  rename: 'folderManager.rename' | 'labelManager.rename';
  delete: 'folderManager.delete' | 'labelManager.delete';
  confirmDelete: 'folderManager.confirmDelete' | 'labelManager.confirmDelete';
  saved: 'folderManager.saved' | 'labelManager.saved';
  deleted: 'folderManager.deleted' | 'labelManager.deleted';
}

/** Trimmed, and null outside the 1–64 characters folders and labels allow. */
function normalizeName(raw: string): string | null {
  const name = raw.trim();
  const length = Array.from(name).length;
  return length >= 1 && length <= MAIL_FOLDER_NAME_MAX_LENGTH ? name : null;
}

/**
 * Adds, renames, and deletes the folders or labels of a mailbox; only offered with `organize`.
 * Failures are shown next to the field they concern, so a taken name is corrected in place.
 */
export function NamedItemsDialog({
  open,
  items,
  copy,
  withColor,
  pending,
  onCreate,
  onUpdate,
  onDelete,
  onClose,
}: {
  open: boolean;
  items: readonly NamedItem[];
  copy: NamedItemCopy;
  withColor: boolean;
  pending: boolean;
  onCreate: (input: NamedItemInput) => Promise<unknown>;
  onUpdate: (id: number, input: NamedItemInput) => Promise<unknown>;
  onDelete: (id: number) => Promise<unknown>;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const newId = useId();
  const [name, setName] = useState('');
  const [color, setColor] = useState<MailLabelColor>('blue');
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<NamedItem | null>(null);
  const [editError, setEditError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<number | null>(null);

  const failure = (cause: unknown) => t(resolveMailboxErrorKey(cause));

  const create = async (event: FormEvent) => {
    event.preventDefault();
    const normalized = normalizeName(name);
    if (normalized === null) {
      setError(t('organizeNames.invalid'));
      return;
    }
    try {
      await onCreate(withColor ? { name: normalized, color } : { name: normalized });
      toast.success(t(copy.saved));
      setName('');
      setError(null);
    } catch (cause) {
      setError(failure(cause));
    }
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (!editing) return;
    const normalized = normalizeName(editing.name);
    if (normalized === null) {
      setEditError(t('organizeNames.invalid'));
      return;
    }
    try {
      await onUpdate(
        editing.id,
        withColor ? { name: normalized, color: editing.color } : { name: normalized }
      );
      toast.success(t(copy.saved));
      setEditing(null);
      setEditError(null);
    } catch (cause) {
      setEditError(failure(cause));
    }
  };

  const remove = async (id: number) => {
    try {
      await onDelete(id);
      toast.success(t(copy.deleted));
      setDeleting(null);
    } catch (cause) {
      toast.error(failure(cause));
    }
  };

  const colorSelect = (
    value: MailLabelColor,
    label: string,
    onChange: (next: MailLabelColor) => void
  ) => (
    <select
      aria-label={label}
      className={cn(nativeSelectClassName, 'w-28 shrink-0')}
      value={value}
      onChange={event => onChange(event.target.value as MailLabelColor)}
    >
      {MAIL_LABEL_COLORS.map(option => (
        <option key={option} value={option}>
          {t(`labelColors.${option}`)}
        </option>
      ))}
    </select>
  );

  return (
    <Dialog open={open} onOpenChange={next => !next && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{t(copy.title)}</DialogTitle>
          <DialogDescription>{t(copy.description)}</DialogDescription>
        </DialogHeader>

        {items.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t(copy.empty)}</p>
        ) : (
          <ul className="max-h-72 divide-y overflow-y-auto rounded-md border">
            {items.map(item =>
              editing?.id === item.id ? (
                <li key={item.id} className="px-3 py-2">
                  <form className="space-y-1.5" onSubmit={event => void save(event)}>
                    <div className="flex items-center gap-2">
                      <Input
                        aria-label={t(copy.name)}
                        value={editing.name}
                        maxLength={MAIL_FOLDER_NAME_MAX_LENGTH * 2}
                        aria-invalid={editError !== null}
                        onChange={event => setEditing({ ...editing, name: event.target.value })}
                        autoFocus
                      />
                      {withColor && editing.color
                        ? colorSelect(
                            editing.color,
                            t('labelManager.colorOf', { name: item.name }),
                            next => setEditing({ ...editing, color: next })
                          )
                        : null}
                      <Button
                        type="submit"
                        isIcon
                        size="sm"
                        variant="ghost"
                        loading={pending}
                        aria-label={t('actions.save')}
                        title={t('actions.save')}
                      >
                        <Check className="size-4" />
                      </Button>
                      <Button
                        type="button"
                        isIcon
                        size="sm"
                        variant="ghost"
                        aria-label={t('actions.cancel')}
                        title={t('actions.cancel')}
                        onClick={() => {
                          setEditing(null);
                          setEditError(null);
                        }}
                      >
                        <X className="size-4" />
                      </Button>
                    </div>
                    {editError ? (
                      <p role="alert" className="text-xs text-error">
                        {editError}
                      </p>
                    ) : null}
                  </form>
                </li>
              ) : deleting === item.id ? (
                <li key={item.id} className="space-y-2 px-3 py-2">
                  <p className="text-sm">{t(copy.confirmDelete, { name: item.name })}</p>
                  <div className="flex justify-end gap-2">
                    <Button
                      type="button"
                      size="xs"
                      variant="outline"
                      onClick={() => setDeleting(null)}
                    >
                      {t('actions.cancel')}
                    </Button>
                    <Button
                      type="button"
                      size="xs"
                      variant="destructive"
                      loading={pending}
                      icon={<Trash2 className="size-3.5" />}
                      onClick={() => void remove(item.id)}
                    >
                      {t('actions.delete')}
                    </Button>
                  </div>
                </li>
              ) : (
                <li key={item.id} className="flex items-center gap-2 px-3 py-1.5">
                  {item.color ? <LabelDot color={item.color} /> : null}
                  <span className="min-w-0 flex-1 truncate text-sm">{item.name}</span>
                  <Button
                    type="button"
                    isIcon
                    size="sm"
                    variant="ghost"
                    aria-label={t(copy.rename, { name: item.name })}
                    title={t(copy.rename, { name: item.name })}
                    onClick={() => {
                      setDeleting(null);
                      setEditError(null);
                      setEditing({ ...item });
                    }}
                  >
                    <Pencil className="size-4" />
                  </Button>
                  <Button
                    type="button"
                    isIcon
                    size="sm"
                    variant="ghost"
                    aria-label={t(copy.delete, { name: item.name })}
                    title={t(copy.delete, { name: item.name })}
                    onClick={() => {
                      setEditing(null);
                      setDeleting(item.id);
                    }}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </li>
              )
            )}
          </ul>
        )}

        <form className="space-y-1.5" onSubmit={event => void create(event)}>
          <Label htmlFor={newId}>{t(copy.newName)}</Label>
          <div className="flex items-center gap-2">
            <Input
              id={newId}
              value={name}
              maxLength={MAIL_FOLDER_NAME_MAX_LENGTH * 2}
              aria-invalid={error !== null}
              onChange={event => {
                setName(event.target.value);
                setError(null);
              }}
            />
            {withColor ? colorSelect(color, t('labelManager.color'), setColor) : null}
            <Button type="submit" size="sm" loading={pending} icon={<Plus className="size-4" />}>
              {t(copy.add)}
            </Button>
          </div>
          {error ? (
            <p role="alert" className="text-xs text-error">
              {error}
            </p>
          ) : null}
        </form>
      </DialogContent>
    </Dialog>
  );
}

const folderCopy: NamedItemCopy = {
  title: 'folderManager.title',
  description: 'folderManager.description',
  name: 'folderManager.name',
  newName: 'folderManager.newName',
  add: 'folderManager.add',
  empty: 'folderManager.empty',
  rename: 'folderManager.rename',
  delete: 'folderManager.delete',
  confirmDelete: 'folderManager.confirmDelete',
  saved: 'folderManager.saved',
  deleted: 'folderManager.deleted',
};

/** The folders of a mailbox. Deleting one moves its mail back to the Inbox. */
export function ManageFoldersDialog({
  mailboxId,
  folders,
  open,
  onClose,
}: {
  mailboxId: number;
  folders: readonly MailFolder[];
  open: boolean;
  onClose: () => void;
}) {
  const organizationId = useMailOrganizationId();
  const create = useCreateMailFolder(organizationId, mailboxId);
  const rename = useRenameMailFolder(organizationId, mailboxId);
  const remove = useDeleteMailFolder(organizationId, mailboxId);
  return (
    <NamedItemsDialog
      open={open}
      items={folders}
      copy={folderCopy}
      withColor={false}
      pending={create.isPending || rename.isPending || remove.isPending}
      onCreate={input => create.mutateAsync({ name: input.name })}
      onUpdate={(folderId, input) => rename.mutateAsync({ folderId, name: input.name })}
      onDelete={folderId => remove.mutateAsync(folderId)}
      onClose={onClose}
    />
  );
}
