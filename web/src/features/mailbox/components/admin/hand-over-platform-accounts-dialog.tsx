'use client';

import { useId, useState, type FormEvent } from 'react';
import { ArrowRightLeft } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import type { PlatformAccountLookups } from '@/features/mailbox/components/admin/platform-account-lookups';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { memberName } from '@/features/mailbox/components/mailbox-people';
import { useHandOverPlatformAccounts } from '@/features/mailbox/hooks/use-platform-accounts';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

const NO_CHOICE_VALUE = '__none__';

/**
 * Moves every account one person is responsible for to another member. The person handing over
 * may have left the organization: owners shown on the current page who are no longer members are
 * offered too. Independent of a mailbox handover.
 */
export function HandOverPlatformAccountsDialog({
  open,
  initialFrom,
  formerOwnerIds,
  lookups,
  onClose,
}: {
  open: boolean;
  initialFrom: string;
  formerOwnerIds: number[];
  lookups: PlatformAccountLookups;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open={open} onOpenChange={next => !next && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{t('platformAccounts.handoverDialog.title')}</DialogTitle>
          <DialogDescription>{t('platformAccounts.handoverDialog.description')}</DialogDescription>
        </DialogHeader>
        {open ? (
          <HandOverForm
            initialFrom={initialFrom}
            formerOwnerIds={formerOwnerIds}
            lookups={lookups}
            onDone={onClose}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function HandOverForm({
  initialFrom,
  formerOwnerIds,
  lookups,
  onDone,
}: {
  initialFrom: string;
  formerOwnerIds: number[];
  lookups: PlatformAccountLookups;
  onDone: () => void;
}) {
  const t = useT('mailbox');
  const handOver = useHandOverPlatformAccounts(useMailOrganizationId());
  const ids = { from: useId(), to: useId() };
  const [from, setFrom] = useState(initialFrom);
  const [to, setTo] = useState('');
  const [error, setError] = useState<string | null>(null);

  const members = lookups.people.map(person => ({
    value: String(person.user_id),
    label: memberName(person),
  }));
  const memberIds = new Set(members.map(member => member.value));
  const former = [...new Set(formerOwnerIds.map(String))]
    .filter(id => !memberIds.has(id))
    .map(id => ({
      value: id,
      label: t('platformAccounts.handoverDialog.formerMember', { id: Number(id) }),
    }));
  const fromOptions = [...members, ...former];
  const toOptions = members.filter(member => member.value !== from);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (from === '' || to === '' || from === to) return;
    setError(null);
    try {
      const result = await handOver.mutateAsync({
        from_user_id: Number(from),
        to_user_id: Number(to),
      });
      toast.success(
        t('platformAccounts.handoverDialog.result', { count: result.account_ids.length })
      );
      onDone();
    } catch (cause) {
      setError(t(resolveMailboxErrorKey(cause)));
    }
  };

  return (
    <form className="space-y-4" onSubmit={event => void submit(event)}>
      <div className="space-y-1.5">
        <Label htmlFor={ids.from}>{t('platformAccounts.handoverDialog.from')}</Label>
        <Select
          value={from === '' ? NO_CHOICE_VALUE : from}
          onValueChange={next => {
            const value = next === NO_CHOICE_VALUE ? '' : next;
            setFrom(value);
            if (value === to) setTo('');
            setError(null);
          }}
        >
          <SelectTrigger id={ids.from} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={NO_CHOICE_VALUE}>
              {t('platformAccounts.handoverDialog.choose')}
            </SelectItem>
            {fromOptions.map(option => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={ids.to}>{t('platformAccounts.handoverDialog.to')}</Label>
        <Select
          value={to === '' ? NO_CHOICE_VALUE : to}
          onValueChange={next => {
            setTo(next === NO_CHOICE_VALUE ? '' : next);
            setError(null);
          }}
        >
          <SelectTrigger id={ids.to} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={NO_CHOICE_VALUE}>
              {t('platformAccounts.handoverDialog.choose')}
            </SelectItem>
            {toOptions.map(option => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {error ? (
        <p role="alert" className="text-xs text-error">
          {error}
        </p>
      ) : null}
      <DialogFooter>
        <Button
          type="submit"
          loading={handOver.isPending}
          disabled={from === '' || to === ''}
          icon={<ArrowRightLeft className="size-4" />}
        >
          {t('platformAccounts.handoverDialog.submit')}
        </Button>
      </DialogFooter>
    </form>
  );
}
