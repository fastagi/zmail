'use client';

import { useId, useState, type FormEvent } from 'react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import type { PlatformAccountLookups } from '@/features/mailbox/components/admin/platform-account-lookups';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { memberName } from '@/features/mailbox/components/mailbox-people';
import {
  ErrorAlert,
  nativeSelectClassName,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import {
  useCreatePlatformAccount,
  usePlatformAccountEvents,
  useUpdatePlatformAccount,
} from '@/features/mailbox/hooks/use-platform-accounts';
import {
  PLATFORM_ACCOUNT_NOTES_MAX_LENGTH,
  PLATFORM_ACCOUNT_STATUSES,
  PLATFORM_HANDLE_MAX_LENGTH,
  PLATFORM_NAME_MAX_LENGTH,
  PLATFORM_PROFILE_URL_MAX_LENGTH,
  type PlatformAccount,
  type PlatformAccountEvent,
  type PlatformAccountInput,
  type PlatformAccountStatus,
} from '@/features/mailbox/platform-account-schemas';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** The form's values; ids are text, empty for none. */
interface AccountDraft {
  platform: string;
  handle: string;
  profileUrl: string;
  roleId: string;
  mailboxId: string;
  ownerUserId: string;
  status: PlatformAccountStatus;
  notes: string;
}

const emptyDraft: AccountDraft = {
  platform: '',
  handle: '',
  profileUrl: '',
  roleId: '',
  mailboxId: '',
  ownerUserId: '',
  status: 'active',
  notes: '',
};

function idText(id: number | null): string {
  return id === null ? '' : String(id);
}

function draftOf(account: PlatformAccount | null): AccountDraft {
  if (!account) return emptyDraft;
  return {
    platform: account.platform,
    handle: account.handle,
    profileUrl: account.profile_url ?? '',
    roleId: idText(account.role_id),
    mailboxId: idText(account.mailbox_id),
    ownerUserId: idText(account.owner_user_id),
    status: account.status,
    notes: account.notes,
  };
}

/** Trimmed, and null outside 1 to `max` characters or with a control character. */
function requiredText(raw: string, max: number): string | null {
  const value = raw.trim();
  const length = Array.from(value).length;
  return length >= 1 && length <= max && !/\p{Cc}/u.test(value) ? value : null;
}

/** Trimmed; empty, or an absolute http(s) URL with a host and no credentials. Null otherwise. */
function profileUrl(raw: string): string | null {
  const value = raw.trim();
  if (value === '') return '';
  if (new TextEncoder().encode(value).length > PLATFORM_PROFILE_URL_MAX_LENGTH) return null;
  try {
    const url = new URL(value);
    const valid =
      (url.protocol === 'http:' || url.protocol === 'https:') &&
      url.hostname !== '' &&
      url.username === '' &&
      url.password === '';
    return valid ? value : null;
  } catch {
    return null;
  }
}

function optionalId(value: string): number | null {
  return value === '' ? null : Number(value);
}

/**
 * Creates an account, or edits one and shows its history. `PUT` replaces every field, so the form
 * always sends them all. A taken platform and handle is shown under the handle.
 */
export function PlatformAccountDrawer({
  open,
  account,
  lookups,
  onClose,
}: {
  open: boolean;
  account: PlatformAccount | null;
  lookups: PlatformAccountLookups;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const title = account
    ? t('platformAccounts.form.editTitle', { platform: account.platform, handle: account.handle })
    : t('platformAccounts.form.createTitle');
  return (
    <Sheet open={open} onOpenChange={next => !next && onClose()}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-[32rem]">
        {open ? (
          <>
            <SheetHeader>
              <SheetTitle>{title}</SheetTitle>
              <SheetDescription>{t('platformAccounts.form.description')}</SheetDescription>
            </SheetHeader>
            {/* Keyed by the account, so nothing of another one stays in the form. */}
            <div key={account?.id ?? 'new'} className="space-y-6 px-4 pb-6">
              <AccountForm account={account} lookups={lookups} onSaved={onClose} />
              {account ? <AccountHistory accountId={account.id} lookups={lookups} /> : null}
            </div>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

function AccountForm({
  account,
  lookups,
  onSaved,
}: {
  account: PlatformAccount | null;
  lookups: PlatformAccountLookups;
  onSaved: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const create = useCreatePlatformAccount(organizationId);
  const update = useUpdatePlatformAccount(organizationId);
  const ids = {
    platform: useId(),
    handle: useId(),
    profileUrl: useId(),
    role: useId(),
    mailbox: useId(),
    owner: useId(),
    status: useId(),
    notes: useId(),
  };
  const [draft, setDraft] = useState<AccountDraft>(() => draftOf(account));
  const [errors, setErrors] = useState<Partial<Record<keyof AccountDraft | 'form', string>>>({});

  const change = (next: Partial<AccountDraft>) => {
    setDraft(current => ({ ...current, ...next }));
    setErrors(current => {
      const kept = { ...current };
      for (const key of Object.keys(next)) delete kept[key as keyof AccountDraft];
      delete kept.form;
      return kept;
    });
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const platform = requiredText(draft.platform, PLATFORM_NAME_MAX_LENGTH);
    const handle = requiredText(draft.handle, PLATFORM_HANDLE_MAX_LENGTH);
    const url = profileUrl(draft.profileUrl);
    const notes = draft.notes.trim();
    const notesValid = Array.from(notes).length <= PLATFORM_ACCOUNT_NOTES_MAX_LENGTH;
    const found: typeof errors = {};
    if (platform === null) found.platform = t('platformAccounts.form.platformInvalid');
    if (handle === null) found.handle = t('platformAccounts.form.handleInvalid');
    if (url === null) found.profileUrl = t('platformAccounts.form.profileUrlInvalid');
    if (!notesValid) found.notes = t('platformAccounts.form.notesInvalid');
    setErrors(found);
    if (platform === null || handle === null || url === null || !notesValid) return;

    const input: Required<PlatformAccountInput> = {
      platform,
      handle,
      profile_url: url,
      role_id: optionalId(draft.roleId),
      mailbox_id: optionalId(draft.mailboxId),
      owner_user_id: optionalId(draft.ownerUserId),
      status: draft.status,
      notes,
    };
    try {
      if (account) {
        await update.mutateAsync({ accountId: account.id, ...input });
      } else {
        await create.mutateAsync(input);
      }
      toast.success(t('platformAccounts.form.saved'));
      onSaved();
    } catch (cause) {
      const message = t(resolveMailboxErrorKey(cause));
      if (
        cause instanceof ApiError &&
        cause.errorCode === ApiErrorCode.MAILBOX_PLATFORM_ACCOUNT_TAKEN
      ) {
        setErrors({ handle: message });
      } else {
        setErrors({ form: message });
      }
    }
  };

  // A reference the lists do not hold, such as a former member or a hidden mailbox, keeps a
  // choice of its own so saving does not drop it.
  const roleOptions = lookups.roles.map(role => ({ value: String(role.id), label: role.name }));
  if (draft.roleId !== '' && !roleOptions.some(option => option.value === draft.roleId)) {
    roleOptions.unshift({ value: draft.roleId, label: `#${draft.roleId}` });
  }
  const mailboxOptions = lookups.mailboxes.map(mailbox => ({
    value: String(mailbox.id),
    label: mailbox.address,
  }));
  if (draft.mailboxId !== '' && !mailboxOptions.some(option => option.value === draft.mailboxId)) {
    mailboxOptions.unshift({
      value: draft.mailboxId,
      label:
        account?.mailbox_id === Number(draft.mailboxId) && account.mailbox_address
          ? account.mailbox_address
          : t('platformAccounts.history.unknownMailbox', { id: Number(draft.mailboxId) }),
    });
  }
  const ownerOptions = lookups.people.map(person => ({
    value: String(person.user_id),
    label: memberName(person),
  }));
  if (
    draft.ownerUserId !== '' &&
    !ownerOptions.some(option => option.value === draft.ownerUserId)
  ) {
    ownerOptions.unshift({
      value: draft.ownerUserId,
      label: lookups.nameOf(Number(draft.ownerUserId)),
    });
  }

  const pending = create.isPending || update.isPending;
  const none = t('platformAccounts.none');

  return (
    <form
      aria-label={t(account ? 'platformAccounts.edit' : 'platformAccounts.create')}
      className="space-y-4"
      noValidate
      onSubmit={event => void submit(event)}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          id={ids.platform}
          label={t('platformAccounts.form.platform')}
          error={errors.platform}
        >
          <Input
            id={ids.platform}
            value={draft.platform}
            placeholder={t('platformAccounts.form.platformPlaceholder')}
            maxLength={PLATFORM_NAME_MAX_LENGTH * 2}
            aria-invalid={errors.platform !== undefined}
            aria-describedby={errors.platform ? `${ids.platform}-error` : undefined}
            onChange={event => change({ platform: event.target.value })}
          />
        </Field>
        <Field id={ids.handle} label={t('platformAccounts.form.handle')} error={errors.handle}>
          <Input
            id={ids.handle}
            value={draft.handle}
            maxLength={PLATFORM_HANDLE_MAX_LENGTH * 2}
            aria-invalid={errors.handle !== undefined}
            aria-describedby={errors.handle ? `${ids.handle}-error` : undefined}
            onChange={event => change({ handle: event.target.value })}
          />
        </Field>
      </div>
      <Field
        id={ids.profileUrl}
        label={t('platformAccounts.form.profileUrl')}
        error={errors.profileUrl}
      >
        <Input
          id={ids.profileUrl}
          type="url"
          inputMode="url"
          value={draft.profileUrl}
          placeholder="https://"
          aria-invalid={errors.profileUrl !== undefined}
          aria-describedby={errors.profileUrl ? `${ids.profileUrl}-error` : undefined}
          onChange={event => change({ profileUrl: event.target.value })}
        />
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={ids.role} label={t('platformAccounts.form.role')}>
          <Select
            id={ids.role}
            value={draft.roleId}
            noneLabel={none}
            options={roleOptions}
            onChange={roleId => change({ roleId })}
          />
        </Field>
        <Field id={ids.status} label={t('platformAccounts.form.status')}>
          <select
            id={ids.status}
            className={cn(nativeSelectClassName, 'w-full')}
            value={draft.status}
            onChange={event => change({ status: event.target.value as PlatformAccountStatus })}
          >
            {PLATFORM_ACCOUNT_STATUSES.map(status => (
              <option key={status} value={status}>
                {t(`platformAccounts.statuses.${status}`)}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <Field id={ids.mailbox} label={t('platformAccounts.form.mailbox')}>
        <Select
          id={ids.mailbox}
          value={draft.mailboxId}
          noneLabel={none}
          options={mailboxOptions}
          onChange={mailboxId => change({ mailboxId })}
        />
      </Field>
      <Field id={ids.owner} label={t('platformAccounts.form.owner')}>
        <Select
          id={ids.owner}
          value={draft.ownerUserId}
          noneLabel={none}
          options={ownerOptions}
          onChange={ownerUserId => change({ ownerUserId })}
        />
      </Field>
      <Field id={ids.notes} label={t('platformAccounts.form.notes')} error={errors.notes}>
        <Textarea
          id={ids.notes}
          rows={3}
          value={draft.notes}
          aria-invalid={errors.notes !== undefined}
          aria-describedby={errors.notes ? `${ids.notes}-error` : undefined}
          onChange={event => change({ notes: event.target.value })}
        />
      </Field>
      {errors.form ? (
        <p role="alert" className="text-xs text-error">
          {errors.form}
        </p>
      ) : null}
      <div className="flex justify-end">
        <Button type="submit" size="sm" loading={pending}>
          {t('actions.save')}
        </Button>
      </div>
    </form>
  );
}

function Field({
  id,
  label,
  error,
  children,
}: {
  id: string;
  label: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0 space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error ? (
        <p id={`${id}-error`} className="text-xs text-error">
          {error}
        </p>
      ) : null}
    </div>
  );
}

function Select({
  id,
  value,
  noneLabel,
  options,
  onChange,
}: {
  id: string;
  value: string;
  noneLabel: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
}) {
  return (
    <select
      id={id}
      className={cn(nativeSelectClassName, 'w-full')}
      value={value}
      onChange={event => onChange(event.target.value)}
    >
      <option value="">{noneLabel}</option>
      {options.map(option => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  );
}

/** The account's history, newest first; older pages load on request. */
function AccountHistory({
  accountId,
  lookups,
}: {
  accountId: number;
  lookups: PlatformAccountLookups;
}) {
  const t = useT('mailbox');
  const [pages, setPages] = useState(1);
  return (
    <section aria-label={t('platformAccounts.history.title')} className="space-y-3 border-t pt-4">
      <h3 className="text-sm font-semibold">{t('platformAccounts.history.title')}</h3>
      <ol className="space-y-3">
        {Array.from({ length: pages }, (_, index) => (
          <HistoryPage
            key={index + 1}
            accountId={accountId}
            page={index + 1}
            last={index + 1 === pages}
            lookups={lookups}
            onMore={() => setPages(current => current + 1)}
          />
        ))}
      </ol>
    </section>
  );
}

function HistoryPage({
  accountId,
  page,
  last,
  lookups,
  onMore,
}: {
  accountId: number;
  page: number;
  last: boolean;
  lookups: PlatformAccountLookups;
  onMore: () => void;
}) {
  const t = useT('mailbox');
  const events = usePlatformAccountEvents(useMailOrganizationId(), accountId, page);
  const format = useDateTimeFormat();
  if (events.isPending) {
    return (
      <li aria-hidden="true">
        <Skeleton className="h-10 w-full" />
      </li>
    );
  }
  if (events.error || !events.data) {
    return (
      <li>
        <ErrorAlert
          message={t(resolveMailboxErrorKey(events.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void events.refetch()}
        />
      </li>
    );
  }
  const { data, meta } = events.data;
  return (
    <>
      {data.length === 0 && page === 1 ? (
        <li className="text-sm text-muted-foreground">{t('platformAccounts.history.empty')}</li>
      ) : null}
      {data.map(event => (
        <li key={event.id} className="border-l-2 pl-3">
          <p className="text-sm">
            <EventText event={event} lookups={lookups} />
          </p>
          <p className="text-xs text-muted-foreground">
            {t('platformAccounts.history.by', {
              name: lookups.nameOf(event.actor_user_id),
              time: format.format(new Date(event.created_at)),
            })}
          </p>
        </li>
      ))}
      {last && meta.current_page < meta.last_page ? (
        <li>
          <Button type="button" size="xs" variant="outline" onClick={onMore}>
            {t('platformAccounts.history.more')}
          </Button>
        </li>
      ) : null}
    </>
  );
}

function EventText({
  event,
  lookups,
}: {
  event: PlatformAccountEvent;
  lookups: PlatformAccountLookups;
}) {
  const t = useT('mailbox');
  const none = t('platformAccounts.none');
  const status = (value: string | null) =>
    value !== null && (PLATFORM_ACCOUNT_STATUSES as readonly string[]).includes(value)
      ? t(`platformAccounts.statuses.${value as PlatformAccountStatus}`)
      : none;
  const owner = (value: string | null) => (value === null ? none : lookups.nameOf(Number(value)));
  const mailbox = (value: string | null) => {
    if (value === null) return none;
    const id = Number(value);
    return lookups.mailboxAddress(id) ?? t('platformAccounts.history.unknownMailbox', { id });
  };
  switch (event.kind) {
    case 'created':
      return t('platformAccounts.history.created', { status: status(event.to) });
    case 'status_changed':
      return t('platformAccounts.history.statusChanged', {
        from: status(event.from),
        to: status(event.to),
      });
    case 'owner_changed':
      return t('platformAccounts.history.ownerChanged', {
        from: owner(event.from),
        to: owner(event.to),
      });
    case 'mailbox_changed':
      return t('platformAccounts.history.mailboxChanged', {
        from: mailbox(event.from),
        to: mailbox(event.to),
      });
    case 'deleted':
      return t('platformAccounts.history.deleted');
  }
}
