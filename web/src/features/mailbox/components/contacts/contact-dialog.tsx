'use client';

import { useId, useState, type FormEvent } from 'react';
import { AlertCircle, Plus, Trash2, X } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
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
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import {
  useDeleteMailContact,
  useSaveMailContact,
} from '@/features/mailbox/hooks/use-mail-contacts';
import { MAIL_CONTACT_NAME_MAX_LENGTH } from '@/features/mailbox/schemas';
import type { MailContact, MailContactList, MailContactStatus } from '@/features/mailbox/types';
import { resolveMailboxErrorKey, type MailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

import { ConfirmDialog } from './contact-shared';

interface PropertyRow {
  key: number;
  name: string;
  value: string;
}

export function ContactDialog({
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
