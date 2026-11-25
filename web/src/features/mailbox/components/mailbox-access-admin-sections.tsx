'use client';

import { useId, useState } from 'react';
import { Trash2 } from 'lucide-react';
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
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import type { Run } from '@/features/mailbox/components/mailbox-access-panel';
import { memberName, type MailboxPerson } from '@/features/mailbox/components/mailbox-people';
import {
  useDeleteMailboxSealedHistory,
  useHandOverMailbox,
} from '@/features/mailbox/hooks/use-mailbox';
import type { Mailbox } from '@/features/mailbox/types';
import type { MailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

const NO_SUCCESSOR = '__none__';

export function HandoverSection({
  mailbox,
  people,
  run,
}: {
  mailbox: Mailbox;
  people: MailboxPerson[];
  run: Run;
}) {
  const t = useT('mailbox');
  const handOver = useHandOverMailbox(useMailOrganizationId(), mailbox.id);
  const ids = { title: useId(), successor: useId() };
  const [successor, setSuccessor] = useState('');
  const [confirming, setConfirming] = useState(false);
  const candidates = people.filter(person => person.user_id !== mailbox.owner_user_id);

  return (
    <section className="space-y-3 rounded-md border p-3" aria-labelledby={ids.title}>
      <h3 id={ids.title} className="text-sm font-semibold">
        {t('access.handover.title')}
      </h3>
      <p className="text-sm text-muted-foreground">{t('access.handover.description')}</p>
      <p className="text-sm text-muted-foreground">{t('access.handover.seal')}</p>
      <div className="space-y-2">
        <Label htmlFor={ids.successor}>{t('access.handover.successor')}</Label>
        <Select
          value={successor || NO_SUCCESSOR}
          disabled={handOver.isPending}
          onValueChange={value => {
            setSuccessor(value === NO_SUCCESSOR ? '' : value);
            setConfirming(false);
          }}
        >
          <SelectTrigger id={ids.successor} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={NO_SUCCESSOR}>{t('create.chooseMember')}</SelectItem>
            {candidates.map(person => (
              <SelectItem key={person.user_id} value={String(person.user_id)}>
                {memberName(person)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {confirming ? (
        <div className="space-y-2">
          <p className="text-sm font-medium">{t('access.handover.confirmHint')}</p>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={handOver.isPending}
              onClick={() => setConfirming(false)}
            >
              {t('actions.cancel')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="destructive"
              loading={handOver.isPending}
              onClick={async () => {
                const done = await run(
                  () => handOver.mutateAsync(Number(successor)),
                  t('messages.handedOver')
                );
                if (done.ok) setConfirming(false);
              }}
            >
              {t('access.handover.confirm')}
            </Button>
          </div>
        </div>
      ) : (
        <Button
          type="button"
          size="sm"
          disabled={successor === ''}
          onClick={() => setConfirming(true)}
        >
          {t('access.handover.submit')}
        </Button>
      )}
    </section>
  );
}

export function SealedSection({ mailbox, run }: { mailbox: Mailbox; run: Run }) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const remove = useDeleteMailboxSealedHistory(useMailOrganizationId(), mailbox.id);
  const inputId = useId();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState('');
  const [failure, setFailure] = useState<MailboxErrorKey | null>(null);
  const matches = typed.trim().toLowerCase() === mailbox.address.toLowerCase();
  const sealedBefore = mailbox.sealed_before ?? '';

  const confirm = async () => {
    const address = typed.trim();
    let counts = { deleted_messages: 0, deleted_broadcasts: 0 };
    setFailure(null);
    const result = await run(async () => {
      counts = await remove.mutateAsync(address);
    });
    if (result.ok) {
      toast.success(
        t('messages.sealedDeleted', {
          messages: counts.deleted_messages,
          broadcasts: counts.deleted_broadcasts,
        })
      );
    } else if (!result.stale) {
      // A wrong address or a passing outage: the dialog stays open to correct or retry.
      setFailure(result.key);
      return;
    }
    close();
  };

  const close = () => {
    setOpen(false);
    setTyped('');
    setFailure(null);
  };

  return (
    <section className="space-y-2 rounded-md border p-3">
      <h3 className="text-sm font-semibold">{t('access.sealed.title')}</h3>
      <p className="text-sm text-muted-foreground">
        {t('access.sealed.description', { date: formatter.format(new Date(sealedBefore)) })}
      </p>
      <Button
        type="button"
        size="sm"
        variant="outline"
        icon={<Trash2 className="size-4" />}
        onClick={() => setOpen(true)}
      >
        {t('access.sealed.delete')}
      </Button>
      <Dialog
        open={open}
        onOpenChange={next => {
          if (next || remove.isPending) return;
          close();
        }}
      >
        <DialogContent size="sm" role="alertdialog">
          <DialogHeader>
            <DialogTitle>{t('access.sealed.confirmTitle')}</DialogTitle>
            <DialogDescription>
              {t('access.sealed.confirmDescription', { address: mailbox.address })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor={inputId}>{t('access.sealed.confirmLabel')}</Label>
            <Input
              id={inputId}
              value={typed}
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              placeholder={mailbox.address}
              disabled={remove.isPending}
              onChange={event => setTyped(event.target.value)}
            />
            {failure ? (
              <p role="alert" className="text-sm text-error">
                {t(failure)}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={remove.isPending} onClick={close}>
              {t('actions.cancel')}
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={!matches}
              loading={remove.isPending}
              icon={<Trash2 className="size-4" />}
              onClick={() => void confirm()}
            >
              {t('access.sealed.confirmAction')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
