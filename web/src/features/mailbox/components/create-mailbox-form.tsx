'use client';

import { useId, useState, type FormEvent } from 'react';
import { Plus, X } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import {
  MailboxPermissionSelect,
  memberName,
  useOrganizationPeople,
} from '@/features/mailbox/components/mailbox-people';
import { useCreateMailbox } from '@/features/mailbox/hooks/use-mailbox';
import {
  MAILBOX_DISPLAY_NAME_MAX_LENGTH,
  MAILBOX_LOCAL_PART_MAX_LENGTH,
  MAILBOX_MAX_MEMBERS,
  normalizeDisplayName,
  normalizeLocalPart,
} from '@/features/mailbox/schemas';
import type {
  CreateMailboxInput,
  Mailbox,
  MailboxDomain,
  MailboxPermission,
  MailboxType,
} from '@/features/mailbox/types';
import { isPermissionDenied, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

interface MemberDraft {
  key: number;
  /** '' until a member is chosen. */
  userId: string;
  permission: MailboxPermission;
}

/**
 * Creates a mailbox: the address, then its type. A personal mailbox names its owner; a shared one
 * names 1 to 100 members, each with a permission. People come from the organization's member
 * directory. The API remains the authority for every rule checked here.
 */
export function CreateMailboxForm({
  domain,
  domains,
  mailboxCount,
  fixedType,
  onCreated,
}: {
  domain: MailboxDomain;
  /** The shared domain and the organization's verified domains. */
  domains: MailboxDomain[];
  /**
   * The organization's mailbox count, when the caller knows it; the limit is then checked here
   * too. Without it the API's `409 MAILBOX.LIMIT_REACHED` is the only check.
   */
  mailboxCount?: number;
  /** Creates only this type and hides the type choice. */
  fixedType?: MailboxType;
  onCreated: (mailbox: Mailbox) => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const create = useCreateMailbox(organizationId);
  const people = useOrganizationPeople(organizationId, true);
  const ids = { title: useId(), type: useId(), owner: useId() };
  const [localPart, setLocalPart] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [addressDomain, setAddressDomain] = useState(domain.domain);
  const [chosenType, setType] = useState<MailboxType>('shared');
  const type = fixedType ?? chosenType;
  const [owner, setOwner] = useState('');
  const [nextKey, setNextKey] = useState(2);
  const [members, setMembers] = useState<MemberDraft[]>([
    { key: 1, userId: '', permission: 'send' },
  ]);
  const [error, setError] = useState<string | null>(null);
  const limitReached = mailboxCount !== undefined && mailboxCount >= domain.max_mailboxes;
  // A domain removed or unverified since the form opened falls back to the shared one.
  const chosen = domains.find(item => item.domain === addressDomain) ?? domain;

  const updateMember = (key: number, change: Partial<MemberDraft>) =>
    setMembers(current => current.map(item => (item.key === key ? { ...item, ...change } : item)));

  /** The access part of the request, or null with the reason shown. */
  const accessInput = (): Pick<CreateMailboxInput, 'type' | 'owner_user_id' | 'members'> | null => {
    if (type === 'personal') {
      if (owner === '') {
        setError(t('create.ownerRequired'));
        return null;
      }
      return { type, owner_user_id: Number(owner) };
    }
    const chosenMembers = members.filter(item => item.userId !== '');
    const unique = new Set(chosenMembers.map(item => item.userId));
    if (
      chosenMembers.length === 0 ||
      unique.size !== chosenMembers.length ||
      chosenMembers.length > MAILBOX_MAX_MEMBERS
    ) {
      setError(t('create.membersRequired'));
      return null;
    }
    return {
      type,
      members: chosenMembers.map(item => ({
        user_id: Number(item.userId),
        permission: item.permission,
      })),
    };
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    const normalized = normalizeLocalPart(localPart);
    if (!normalized.ok) {
      setError(
        t(normalized.reason === 'reserved' ? 'errors.addressReserved' : 'errors.addressInvalid')
      );
      return;
    }
    const name = normalizeDisplayName(displayName);
    if (name === null) {
      setError(t('create.displayNameInvalid'));
      return;
    }
    const access = accessInput();
    if (!access) return;
    try {
      const mailbox = await create.mutateAsync({
        local_part: normalized.value,
        ...(name === '' ? {} : { display_name: name }),
        ...(chosen.kind === 'organization' ? { domain: chosen.domain } : {}),
        ...access,
      });
      setLocalPart('');
      setDisplayName('');
      setOwner('');
      setMembers([{ key: nextKey, userId: '', permission: 'send' }]);
      setNextKey(key => key + 1);
      toast.success(t('messages.mailboxCreated'));
      onCreated(mailbox);
    } catch (failure) {
      // Creating is an organization role, not a mailbox permission, so a refusal says so.
      setError(
        t(isPermissionDenied(failure) ? 'errors.createDenied' : resolveMailboxErrorKey(failure))
      );
    }
  };

  const peopleOptions = (
    <>
      <option value="">{t('create.chooseMember')}</option>
      {people.items.map(person => (
        <option key={person.user_id} value={String(person.user_id)}>
          {memberName(person)}
        </option>
      ))}
    </>
  );

  return (
    <form className="space-y-3 rounded-md border p-4" aria-labelledby={ids.title} onSubmit={submit}>
      <h3 id={ids.title} className="text-sm font-semibold">
        {t('create.title')}
      </h3>
      {limitReached ? (
        <p className="text-sm text-muted-foreground">
          {t('create.limitReached', { max: domain.max_mailboxes })}
        </p>
      ) : (
        <>
          <div className="space-y-2">
            <Label htmlFor="mailbox-local-part">{t('create.localPart')}</Label>
            <div className="flex items-center gap-2">
              <Input
                id="mailbox-local-part"
                value={localPart}
                maxLength={MAILBOX_LOCAL_PART_MAX_LENGTH}
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                disabled={create.isPending}
                placeholder={t('create.localPartPlaceholder')}
                aria-describedby="mailbox-local-part-hint"
                onChange={event => setLocalPart(event.target.value)}
              />
              {domains.length > 1 ? (
                <span className="flex shrink-0 items-center gap-1 text-sm text-muted-foreground">
                  @
                  <select
                    aria-label={t('create.domain')}
                    className={cn(nativeSelectClassName, 'max-w-44')}
                    value={chosen.domain}
                    disabled={create.isPending}
                    onChange={event => setAddressDomain(event.target.value)}
                  >
                    {domains.map(item => (
                      <option key={item.domain} value={item.domain}>
                        {item.domain}
                      </option>
                    ))}
                  </select>
                </span>
              ) : (
                <span className="shrink-0 text-sm text-muted-foreground">@{domain.domain}</span>
              )}
            </div>
            <p id="mailbox-local-part-hint" className="text-xs text-muted-foreground">
              {t('create.localPartHint')}
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="mailbox-display-name">{t('create.displayName')}</Label>
            <Input
              id="mailbox-display-name"
              value={displayName}
              maxLength={MAILBOX_DISPLAY_NAME_MAX_LENGTH}
              autoComplete="off"
              disabled={create.isPending}
              placeholder={t('create.displayNamePlaceholder')}
              onChange={event => setDisplayName(event.target.value)}
            />
          </div>

          <fieldset className={cn('space-y-2', fixedType ? 'hidden' : undefined)}>
            <legend id={ids.type} className="text-sm font-medium">
              {t('create.type')}
            </legend>
            <div role="radiogroup" aria-labelledby={ids.type} className="space-y-2">
              {(['shared', 'personal'] as const).map(option => (
                <div key={option} className="flex items-start gap-2 text-sm">
                  <input
                    id={`${ids.type}-${option}`}
                    type="radio"
                    name={`${ids.type}-type`}
                    value={option}
                    className="mt-1"
                    checked={type === option}
                    disabled={create.isPending}
                    aria-describedby={`${ids.type}-${option}-hint`}
                    onChange={() => setType(option)}
                  />
                  <div>
                    <label htmlFor={`${ids.type}-${option}`} className="block font-medium">
                      {t(`create.${option}`)}
                    </label>
                    <p id={`${ids.type}-${option}-hint`} className="text-xs text-muted-foreground">
                      {t(option === 'shared' ? 'create.sharedHint' : 'create.personalHint')}
                    </p>
                  </div>
                </div>
              ))}
            </div>
          </fieldset>

          {people.status === 'loading' ? (
            <p className="text-sm text-muted-foreground">{t('create.membersLoading')}</p>
          ) : people.status === 'error' ? (
            <p className="text-sm text-error">{t('create.membersFailed')}</p>
          ) : null}

          {type === 'personal' ? (
            <div className="space-y-2">
              <Label htmlFor={ids.owner}>{t('create.owner')}</Label>
              <select
                id={ids.owner}
                className={nativeSelectClassName}
                value={owner}
                disabled={create.isPending}
                onChange={event => setOwner(event.target.value)}
              >
                {peopleOptions}
              </select>
            </div>
          ) : (
            <div className="space-y-2">
              <ul className="space-y-2">
                {members.map((item, index) => {
                  const number = index + 1;
                  return (
                    <li key={item.key} className="flex flex-wrap items-center gap-2">
                      <select
                        aria-label={t('create.member', { index: number })}
                        className={cn(nativeSelectClassName, 'min-w-0 flex-1')}
                        value={item.userId}
                        disabled={create.isPending}
                        onChange={event => updateMember(item.key, { userId: event.target.value })}
                      >
                        {peopleOptions}
                      </select>
                      <MailboxPermissionSelect
                        label={t('create.memberPermission', { index: number })}
                        value={item.permission}
                        disabled={create.isPending}
                        onChange={permission => updateMember(item.key, { permission })}
                      />
                      {members.length > 1 ? (
                        <Button
                          type="button"
                          isIcon
                          size="sm"
                          variant="ghost"
                          aria-label={t('create.removeMember', { index: number })}
                          title={t('create.removeMember', { index: number })}
                          disabled={create.isPending}
                          onClick={() =>
                            setMembers(current => current.filter(other => other.key !== item.key))
                          }
                        >
                          <X className="size-4" />
                        </Button>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
              {members.length < MAILBOX_MAX_MEMBERS ? (
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  icon={<Plus className="size-3.5" />}
                  disabled={create.isPending}
                  onClick={() => {
                    setMembers(current => [
                      ...current,
                      { key: nextKey, userId: '', permission: 'send' },
                    ]);
                    setNextKey(key => key + 1);
                  }}
                >
                  {t('create.addMember')}
                </Button>
              ) : null}
              <p className="text-xs text-muted-foreground">{t('access.permissionHint')}</p>
            </div>
          )}

          {error ? (
            <p role="alert" className="text-sm text-error">
              {error}
            </p>
          ) : null}
          <Button
            type="submit"
            size="sm"
            loading={create.isPending}
            icon={<Plus className="size-4" />}
          >
            {t('actions.create')}
          </Button>
        </>
      )}
    </form>
  );
}
