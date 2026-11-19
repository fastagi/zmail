'use client';

import { useId, useState, type FormEvent } from 'react';
import { Inbox, Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import { LabelDot } from '@/features/mailbox/components/mailbox-sidebar';
import { memberName, useOrganizationPeople } from '@/features/mailbox/components/mailbox-people';
import {
  useAssignMailboxRole,
  useCreateMailRole,
  useDeleteMailRole,
  useMailRoles,
  useUpdateMailRole,
} from '@/features/mailbox/hooks/use-inbox';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import {
  MAIL_LABEL_COLORS,
  MAIL_ROLE_DESCRIPTION_MAX_LENGTH,
  MAIL_ROLE_NAME_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import type { MailLabelColor, Mailbox, MailRole, MailRoleInput } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** The form's values; the owner is the user id as a string, empty for none. */
interface RoleDraft {
  name: string;
  color: MailLabelColor;
  description: string;
  owner: string;
}

const emptyDraft: RoleDraft = { name: '', color: 'gray', description: '', owner: '' };

function draftOf(role: MailRole): RoleDraft {
  return {
    name: role.name,
    color: role.color,
    description: role.description,
    owner: role.owner_user_id === null ? '' : String(role.owner_user_id),
  };
}

/**
 * Creates, edits, and deletes the organization's mail roles: a name, a color of the label
 * palette, a description, and an owner shown for display. Rendered only for owners and admins; the
 * API decides. A taken name is shown under the name field, other refusals (such as the 50-role
 * limit) under the form. Deleting asks first: the role's mailboxes become ungrouped. Each role opens
 * a checklist of the mailboxes the caller administers: a mailbox has at most one role, so checking
 * one that is in another role moves it, and unchecking leaves it without a role.
 */
export function ManageRolesDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const roles = useMailRoles(organizationId);
  const people = useOrganizationPeople(organizationId, open);
  const create = useCreateMailRole(organizationId);
  const update = useUpdateMailRole(organizationId);
  const remove = useDeleteMailRole(organizationId);
  const [editing, setEditing] = useState<number | null>(null);
  const [deleting, setDeleting] = useState<number | null>(null);
  const [assigning, setAssigning] = useState<number | null>(null);
  const list = roles.data ?? [];

  const confirmDelete = async (role: MailRole) => {
    try {
      await remove.mutateAsync(role.id);
      toast.success(t('roles.deleted'));
      setDeleting(null);
    } catch (cause) {
      toast.error(t(resolveMailboxErrorKey(cause)));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent size="lg" className="overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t('roles.title')}</DialogTitle>
          <DialogDescription>{t('roles.description')}</DialogDescription>
        </DialogHeader>

        <section className="space-y-2" aria-label={t('roles.list')}>
          {list.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t('roles.empty')}</p>
          ) : (
            <ul className="divide-y rounded-md border">
              {list.map(role =>
                editing === role.id ? (
                  <li key={role.id} className="px-3 py-3">
                    <RoleForm
                      label={t('roles.editing')}
                      initial={draftOf(role)}
                      people={people.items}
                      submitLabel={t('actions.save')}
                      pending={update.isPending}
                      onSubmit={async input => {
                        await update.mutateAsync({ roleId: role.id, ...input });
                        toast.success(t('roles.saved'));
                        setEditing(null);
                      }}
                      onCancel={() => setEditing(null)}
                    />
                  </li>
                ) : deleting === role.id ? (
                  <li key={role.id} className="space-y-2 px-3 py-2">
                    <p className="text-sm">{t('roles.confirmDelete', { name: role.name })}</p>
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
                        loading={remove.isPending}
                        icon={<Trash2 className="size-3.5" />}
                        onClick={() => void confirmDelete(role)}
                      >
                        {t('actions.delete')}
                      </Button>
                    </div>
                  </li>
                ) : (
                  <li key={role.id} className="px-3 py-2">
                    <div className="flex items-center gap-2">
                      <LabelDot color={role.color} />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium">{role.name}</span>
                        <span className="block truncate text-xs text-muted-foreground">
                          {role.description ||
                            t('roles.mailboxCount', { count: role.mailbox_ids.length })}
                        </span>
                      </span>
                      <Button
                        type="button"
                        isIcon
                        size="sm"
                        variant="ghost"
                        aria-label={t('roles.mailboxes', { name: role.name })}
                        title={t('roles.mailboxes', { name: role.name })}
                        aria-expanded={assigning === role.id}
                        onClick={() =>
                          setAssigning(current => (current === role.id ? null : role.id))
                        }
                      >
                        <Inbox className="size-4" />
                      </Button>
                      <Button
                        type="button"
                        isIcon
                        size="sm"
                        variant="ghost"
                        aria-label={t('roles.edit', { name: role.name })}
                        title={t('roles.edit', { name: role.name })}
                        onClick={() => {
                          setDeleting(null);
                          setEditing(role.id);
                        }}
                      >
                        <Pencil className="size-4" />
                      </Button>
                      <Button
                        type="button"
                        isIcon
                        size="sm"
                        variant="ghost"
                        aria-label={t('roles.delete', { name: role.name })}
                        title={t('roles.delete', { name: role.name })}
                        onClick={() => {
                          setEditing(null);
                          setDeleting(role.id);
                        }}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                    {assigning === role.id ? <RoleMailboxes role={role} roles={list} /> : null}
                  </li>
                )
              )}
            </ul>
          )}
        </section>

        <section className="space-y-2 border-t pt-4">
          <h3 className="text-sm font-semibold">{t('roles.newRole')}</h3>
          <RoleForm
            label={t('roles.newRole')}
            initial={emptyDraft}
            people={people.items}
            submitLabel={t('roles.add')}
            submitIcon={<Plus className="size-4" />}
            pending={create.isPending}
            resetOnSuccess
            onSubmit={async input => {
              await create.mutateAsync(input);
              toast.success(t('roles.saved'));
            }}
          />
        </section>
      </DialogContent>
    </Dialog>
  );
}

/**
 * The mailboxes the caller administers, checked when they are in `role`. A mailbox in another role
 * names it; checking moves the mailbox here, unchecking leaves it without a role.
 */
function RoleMailboxes({ role, roles }: { role: MailRole; roles: MailRole[] }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const mailboxes = useMailboxes(organizationId);
  const assign = useAssignMailboxRole(organizationId);
  const administered = (mailboxes.data ?? []).filter(mailbox => mailbox.access.administer);
  const names = new Map(roles.map(item => [item.id, item.name]));

  const toggle = async (mailbox: Mailbox, checked: boolean) => {
    try {
      await assign.mutateAsync({ mailboxId: mailbox.id, roleId: checked ? role.id : null });
      toast.success(t('roles.assigned'));
    } catch (cause) {
      toast.error(t(resolveMailboxErrorKey(cause)));
    }
  };

  return (
    <div
      role="group"
      aria-label={t('roles.mailboxes', { name: role.name })}
      className="mt-2 space-y-1.5 border-t pt-2"
    >
      {administered.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t('roles.noAdministeredMailboxes')}</p>
      ) : (
        administered.map(mailbox => {
          const current = mailbox.role_id ?? null;
          const other = current !== null && current !== role.id ? names.get(current) : undefined;
          const label =
            other === undefined
              ? mailbox.address
              : t('roles.mailboxInRole', { address: mailbox.address, role: other });
          return (
            <label key={mailbox.id} className="flex items-center gap-2 text-sm">
              <Checkbox
                aria-label={label}
                checked={current === role.id}
                disabled={assign.isPending}
                onCheckedChange={checked => void toggle(mailbox, checked === true)}
              />
              <span className="min-w-0 flex-1 truncate">{mailbox.address}</span>
              {other === undefined ? null : (
                <span className="truncate text-xs text-muted-foreground">{other}</span>
              )}
            </label>
          );
        })
      )}
    </div>
  );
}

/** Trimmed, and null outside the 1–64 characters a role name allows. */
function normalizeName(raw: string): string | null {
  const name = raw.trim();
  const length = Array.from(name).length;
  return length >= 1 && length <= MAIL_ROLE_NAME_MAX_LENGTH ? name : null;
}

/** Trimmed, and null past 280 characters or with a control character. */
function normalizeDescription(raw: string): string | null {
  const description = raw.trim();
  return Array.from(description).length <= MAIL_ROLE_DESCRIPTION_MAX_LENGTH &&
    !/\p{Cc}/u.test(description)
    ? description
    : null;
}

function RoleForm({
  label,
  initial,
  people,
  submitLabel,
  submitIcon,
  pending,
  resetOnSuccess = false,
  onSubmit,
  onCancel,
}: {
  label: string;
  initial: RoleDraft;
  people: ReturnType<typeof useOrganizationPeople>['items'];
  submitLabel: string;
  submitIcon?: React.ReactNode;
  pending: boolean;
  resetOnSuccess?: boolean;
  onSubmit: (input: Required<MailRoleInput>) => Promise<void>;
  onCancel?: () => void;
}) {
  const t = useT('mailbox');
  const ids = {
    name: useId(),
    nameError: useId(),
    color: useId(),
    description: useId(),
    descriptionError: useId(),
    owner: useId(),
  };
  const [draft, setDraft] = useState<RoleDraft>(initial);
  const [nameError, setNameError] = useState<string | null>(null);
  const [descriptionError, setDescriptionError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const change = (next: Partial<RoleDraft>) => {
    setDraft(current => ({ ...current, ...next }));
    if ('name' in next) setNameError(null);
    if ('description' in next) setDescriptionError(null);
    setFormError(null);
  };
  // A former member still named as the owner keeps a choice of their own.
  const ownerKnown =
    draft.owner === '' || people.some(person => String(person.user_id) === draft.owner);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const name = normalizeName(draft.name);
    const description = normalizeDescription(draft.description);
    setNameError(name === null ? t('roles.nameInvalid') : null);
    setDescriptionError(description === null ? t('roles.descriptionInvalid') : null);
    setFormError(null);
    if (name === null || description === null) return;
    try {
      await onSubmit({
        name,
        color: draft.color,
        description,
        owner_user_id: draft.owner === '' ? null : Number(draft.owner),
      });
      if (resetOnSuccess) setDraft(initial);
    } catch (cause) {
      const message = t(resolveMailboxErrorKey(cause));
      if (cause instanceof ApiError && cause.errorCode === ApiErrorCode.MAILBOX_ROLE_NAME_TAKEN) {
        setNameError(message);
      } else {
        setFormError(message);
      }
    }
  };

  return (
    <form aria-label={label} className="space-y-3" onSubmit={event => void submit(event)}>
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_8rem]">
        <div className="space-y-1.5">
          <Label htmlFor={ids.name}>{t('roles.name')}</Label>
          <Input
            id={ids.name}
            value={draft.name}
            maxLength={MAIL_ROLE_NAME_MAX_LENGTH * 2}
            aria-invalid={nameError !== null}
            aria-describedby={nameError ? ids.nameError : undefined}
            onChange={event => change({ name: event.target.value })}
          />
          {nameError ? (
            <p id={ids.nameError} className="text-xs text-error">
              {nameError}
            </p>
          ) : null}
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={ids.color}>{t('roles.color')}</Label>
          <select
            id={ids.color}
            className={nativeSelectClassName}
            value={draft.color}
            onChange={event => change({ color: event.target.value as MailLabelColor })}
          >
            {MAIL_LABEL_COLORS.map(option => (
              <option key={option} value={option}>
                {t(`labelColors.${option}`)}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={ids.description}>{t('roles.descriptionField')}</Label>
        <Textarea
          id={ids.description}
          rows={2}
          value={draft.description}
          maxLength={MAIL_ROLE_DESCRIPTION_MAX_LENGTH * 2}
          aria-invalid={descriptionError !== null}
          aria-describedby={descriptionError ? ids.descriptionError : undefined}
          onChange={event => change({ description: event.target.value })}
        />
        {descriptionError ? (
          <p id={ids.descriptionError} className="text-xs text-error">
            {descriptionError}
          </p>
        ) : null}
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={ids.owner}>{t('roles.owner')}</Label>
        <select
          id={ids.owner}
          className={cn(nativeSelectClassName)}
          value={draft.owner}
          onChange={event => change({ owner: event.target.value })}
        >
          <option value="">{t('roles.noOwner')}</option>
          {ownerKnown ? null : (
            <option value={draft.owner}>
              {t('access.unknownUser', { id: Number(draft.owner) })}
            </option>
          )}
          {people.map(person => (
            <option key={person.user_id} value={String(person.user_id)}>
              {memberName(person)}
            </option>
          ))}
        </select>
      </div>
      {formError ? (
        <p role="alert" className="text-xs text-error">
          {formError}
        </p>
      ) : null}
      <div className="flex justify-end gap-2">
        {onCancel ? (
          <Button type="button" size="sm" variant="outline" onClick={onCancel}>
            {t('actions.cancel')}
          </Button>
        ) : null}
        <Button type="submit" size="sm" loading={pending} icon={submitIcon}>
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}
