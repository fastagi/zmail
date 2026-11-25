'use client';

import { useId, useState, type FormEvent } from 'react';
import { AlertCircle, Send, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import {
  useDeleteMailTemplate,
  useSendMailTemplateTest,
} from '@/features/mailbox/hooks/use-mail-templates';
import {
  MAIL_TEMPLATE_TEST_MAX_RECIPIENTS,
  normalizeRecipient,
  splitRecipients,
} from '@/features/mailbox/schemas';
import { resolveMailboxErrorKey, type MailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export function TemplateTestDialog({
  templateId,
  unsaved,
  onClose,
}: {
  templateId: number | null;
  unsaved: boolean;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const mailboxes = useMailboxes(organizationId);
  const send = useSendMailTemplateTest(organizationId);
  const [mailboxId, setMailboxId] = useState<number | null>(null);
  const [to, setTo] = useState('');
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(null);
  const ids = { from: useId(), to: useId(), hint: useId() };
  // A test send needs `send` on the mailbox it goes from.
  const options = (mailboxes.data ?? []).filter(mailbox => mailbox.access.send);
  const from = mailboxId ?? options[0]?.id ?? null;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorKey(null);
    const recipients = splitRecipients(to).map(normalizeRecipient);
    if (
      templateId === null ||
      from === null ||
      recipients.length === 0 ||
      recipients.length > MAIL_TEMPLATE_TEST_MAX_RECIPIENTS ||
      recipients.some(recipient => recipient === null)
    ) {
      setErrorKey('errors.messageInvalid');
      return;
    }
    try {
      const stored = await send.mutateAsync({
        templateId,
        input: { mailbox_id: from, to: recipients as string[] },
      });
      toast.success(
        t(stored.queued_reason === 'send_limit' ? 'messages.queued' : 'templates.testSent')
      );
      onClose();
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !send.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('templates.testTitle')}</DialogTitle>
          <DialogDescription>{t('templates.testDescription')}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit}>
          <DialogBody className="space-y-4">
            {unsaved ? (
              <Alert>
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{t('templates.testNeedsSave')}</AlertDescription>
              </Alert>
            ) : null}
            {mailboxes.isSuccess && options.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t('templates.testNeedsMailbox')}</p>
            ) : (
              <div className="space-y-2">
                <Label htmlFor={ids.from}>{t('templates.testFrom')}</Label>
                <Select
                  value={from === null ? '' : String(from)}
                  disabled={send.isPending || options.length === 0}
                  onValueChange={value => setMailboxId(Number(value))}
                >
                  <SelectTrigger id={ids.from} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {options.map(option => (
                      <SelectItem key={option.id} value={String(option.id)}>
                        {option.address}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor={ids.to}>{t('templates.testTo')}</Label>
              <Input
                id={ids.to}
                value={to}
                spellCheck={false}
                aria-describedby={ids.hint}
                disabled={send.isPending}
                onChange={event => setTo(event.target.value)}
              />
              <p id={ids.hint} className="text-xs text-muted-foreground">
                {t('templates.testToHint')}
              </p>
            </div>
            {errorKey ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{t(errorKey)}</AlertDescription>
              </Alert>
            ) : null}
          </DialogBody>
          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" disabled={send.isPending} onClick={onClose}>
              {t('actions.cancel')}
            </Button>
            <Button
              type="submit"
              loading={send.isPending}
              disabled={from === null || templateId === null}
              icon={<Send className="size-4" />}
            >
              {t('templates.test')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function DeleteTemplateDialog({
  templateId,
  name,
  onClose,
  onDeleted,
}: {
  templateId: number;
  name: string;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const t = useT('mailbox');
  const remove = useDeleteMailTemplate(useMailOrganizationId());

  const confirm = async () => {
    try {
      await remove.mutateAsync(templateId);
      toast.success(t('templates.deleted'));
      onClose();
      onDeleted();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !remove.isPending && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{t('templates.deleteTitle')}</DialogTitle>
          <DialogDescription>{t('templates.deleteDescription', { name })}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={remove.isPending} onClick={onClose}>
            {t('actions.cancel')}
          </Button>
          <Button
            type="button"
            variant="destructive"
            loading={remove.isPending}
            icon={<Trash2 className="size-4" />}
            onClick={() => void confirm()}
          >
            {t('actions.delete')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function SwitchModeDialog({
  mode,
  onConfirm,
  onClose,
}: {
  mode: 'visual' | 'html';
  onConfirm: () => void;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const toHtml = mode === 'html';
  return (
    <Dialog open onOpenChange={open => !open && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>
            {t(toHtml ? 'templates.visual.toHtmlTitle' : 'templates.visual.toVisualTitle')}
          </DialogTitle>
          <DialogDescription>
            {t(
              toHtml ? 'templates.visual.toHtmlDescription' : 'templates.visual.toVisualDescription'
            )}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('actions.cancel')}
          </Button>
          <Button type="button" onClick={onConfirm}>
            {t(toHtml ? 'templates.visual.toHtmlConfirm' : 'templates.visual.toVisualConfirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function DiscardChangesDialog({
  onConfirm,
  onClose,
}: {
  onConfirm: () => void;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open onOpenChange={open => !open && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{t('templates.unsavedTitle')}</DialogTitle>
          <DialogDescription>{t('templates.unsavedDescription')}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('actions.cancel')}
          </Button>
          <Button type="button" variant="destructive" onClick={onConfirm}>
            {t('templates.discard')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
