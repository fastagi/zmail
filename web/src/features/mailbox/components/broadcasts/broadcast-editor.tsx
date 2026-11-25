'use client';

import { useId } from 'react';
import { AlertCircle, Clock, Save, Send, Trash2 } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  BroadcastPreview,
  SharedValues,
} from '@/features/mailbox/components/broadcasts/broadcast-preview';
import {
  BackButton,
  ConfirmDialog,
  domainOf,
  localDateTimeValue,
  maxScheduleAheadMs,
} from '@/features/mailbox/components/broadcasts/broadcast-shared';
import { useBroadcastEditor } from '@/features/mailbox/components/broadcasts/use-broadcast-editor';
import {
  MAIL_BROADCAST_LABEL_MAX_LENGTH,
  MAIL_BROADCAST_NAME_MAX_LENGTH,
  MAIL_BROADCAST_TEST_MAX_PERCENT,
  MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES,
  MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES,
} from '@/features/mailbox/schemas';
import type { MailBroadcast } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

export function BroadcastEditor({
  broadcast,
  enabled,
  canManage,
  onBack,
  onSaved,
}: {
  broadcast: MailBroadcast | null;
  enabled: boolean;
  canManage: boolean;
  onBack: () => void;
  onSaved: (broadcastId: number) => void;
}) {
  const t = useT('mailbox');
  const editor = useBroadcastEditor({ broadcast, onBack, onSaved });
  const ids = {
    name: useId(),
    from: useId(),
    template: useId(),
    list: useId(),
    label: useId(),
    labelHint: useId(),
    at: useId(),
    atHint: useId(),
    domain: useId(),
    staged: useId(),
    percent: useId(),
    wait: useId(),
    stagedHint: useId(),
  };

  const select = (
    id: string,
    labelText: string,
    value: number | null,
    onChange: (value: number) => void,
    options: { id: number; label: string }[]
  ) => (
    <div className="space-y-2">
      <Label htmlFor={id}>{labelText}</Label>
      <Select
        value={value === null ? '' : String(value)}
        disabled={editor.pending}
        onValueChange={next => onChange(Number(next))}
      >
        <SelectTrigger id={id} className="w-full">
          <SelectValue placeholder={t('broadcasts.choose')} />
        </SelectTrigger>
        <SelectContent>
          {options.map(option => (
            <SelectItem key={option.id} value={String(option.id)}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <BackButton onBack={editor.requestBack} />
        {editor.savedId !== null ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<Trash2 className="size-4" />}
            disabled={editor.pending}
            onClick={() => editor.setConfirm('delete')}
          >
            {t('broadcasts.delete')}
          </Button>
        ) : null}
      </div>
      {!editor.loading && !editor.ready ? (
        <Alert>
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>{t('broadcasts.needsSetup')}</AlertDescription>
        </Alert>
      ) : null}
      <div className={cn('grid gap-6', editor.activeTemplate !== null && 'lg:grid-cols-2')}>
        <form onSubmit={editor.submit} className="min-w-0 max-w-2xl space-y-4">
          <div className="space-y-2">
            <Label htmlFor={ids.name}>{t('broadcasts.name')}</Label>
            <Input
              id={ids.name}
              value={editor.name}
              maxLength={MAIL_BROADCAST_NAME_MAX_LENGTH}
              placeholder={t('broadcasts.namePlaceholder')}
              disabled={editor.pending}
              onChange={event => editor.setName(event.target.value)}
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor={ids.domain}>{t('broadcasts.fromDomain')}</Label>
              <Select
                value={editor.currentDomain ?? ''}
                disabled={editor.pending || editor.senderDomains.length === 0}
                onValueChange={value => editor.selectDomain(value)}
              >
                <SelectTrigger id={ids.domain} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {editor.senderDomains.map(domain => (
                    <SelectItem key={domain} value={domain}>
                      {domain}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {select(
              ids.from,
              t('broadcasts.from'),
              editor.mailboxId,
              editor.setMailboxId,
              editor.senderMailboxes
                .filter(
                  mailbox =>
                    editor.currentDomain === null ||
                    domainOf(mailbox.address) === editor.currentDomain
                )
                .map(mailbox => ({ id: mailbox.id, label: mailbox.address }))
            )}
          </div>
          {select(
            ids.template,
            t('broadcasts.template'),
            editor.templateId,
            editor.setTemplateId,
            editor.templates.map(template => ({ id: template.id, label: template.name }))
          )}
          {select(
            ids.list,
            t('broadcasts.list'),
            editor.listId,
            editor.setListId,
            editor.lists.map(list => ({
              id: list.id,
              label: t('broadcasts.listOption', { name: list.name, count: list.contact_count }),
            }))
          )}
          {editor.activeTemplate === null ? null : (
            <SharedValues
              template={editor.activeTemplate}
              values={editor.variables}
              disabled={editor.pending}
              onChange={editor.setVariable}
            />
          )}
          <div className="space-y-2">
            <Label htmlFor={ids.label}>{t('broadcasts.label')}</Label>
            <Input
              id={ids.label}
              value={editor.label}
              maxLength={MAIL_BROADCAST_LABEL_MAX_LENGTH}
              placeholder={t('broadcasts.labelPlaceholder')}
              aria-describedby={ids.labelHint}
              disabled={editor.pending}
              onChange={event => editor.setLabel(event.target.value)}
            />
            <p id={ids.labelHint} className="text-xs text-muted-foreground">
              {t('broadcasts.labelHint')}
            </p>
          </div>
          <fieldset className="space-y-3 rounded-md border p-3">
            <div className="flex items-center gap-2">
              <input
                id={ids.staged}
                type="checkbox"
                className="size-4"
                checked={editor.testPercent > 0}
                disabled={editor.pending}
                aria-describedby={ids.stagedHint}
                onChange={event => editor.setTestPercent(event.target.checked ? 10 : 0)}
              />
              <Label htmlFor={ids.staged}>{t('broadcasts.staged')}</Label>
            </div>
            <p id={ids.stagedHint} className="text-xs text-muted-foreground">
              {t('broadcasts.stagedHint')}
            </p>
            {editor.testPercent > 0 ? (
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor={ids.percent}>{t('broadcasts.testPercent')}</Label>
                  <Input
                    id={ids.percent}
                    type="number"
                    min={1}
                    max={MAIL_BROADCAST_TEST_MAX_PERCENT}
                    value={editor.testPercent}
                    disabled={editor.pending}
                    onChange={event =>
                      editor.setTestPercent(
                        Math.min(
                          MAIL_BROADCAST_TEST_MAX_PERCENT,
                          Math.max(1, Number(event.target.value) || 1)
                        )
                      )
                    }
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor={ids.wait}>{t('broadcasts.testWait')}</Label>
                  <Input
                    id={ids.wait}
                    type="number"
                    min={MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES}
                    max={MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES}
                    value={editor.testWait}
                    disabled={editor.pending}
                    onChange={event =>
                      editor.setTestWait(
                        Math.min(
                          MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES,
                          Math.max(
                            MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES,
                            Number(event.target.value) || 0
                          )
                        )
                      )
                    }
                  />
                </div>
              </div>
            ) : null}
          </fieldset>
          <p className="text-sm text-muted-foreground">{t('broadcasts.safetyHint')}</p>
          {editor.errorKey ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertDescription>{t(editor.errorKey)}</AlertDescription>
            </Alert>
          ) : null}
          <div className="flex flex-wrap items-end gap-3">
            <Button
              type="submit"
              variant={editor.savedId === null ? 'default' : 'outline'}
              loading={editor.saving && editor.confirm === null}
              disabled={(!editor.dirty && editor.savedId !== null) || !editor.ready || editor.pending}
              icon={<Save className="size-4" />}
            >
              {t('broadcasts.save')}
            </Button>
            {canManage ? (
              <>
                <Button
                  type="button"
                  loading={(editor.saving || editor.sending) && editor.confirm === 'send'}
                  disabled={!editor.ready || !enabled || editor.pending}
                  icon={<Send className="size-4" />}
                  onClick={editor.requestSend}
                >
                  {t('broadcasts.sendNow')}
                </Button>
                <div className="flex flex-wrap items-end gap-2">
                  <div className="space-y-2">
                    <div className="flex items-baseline justify-between gap-2">
                      <Label htmlFor={ids.at}>{t('broadcasts.scheduleAt')}</Label>
                      <span id={ids.atHint} className="text-xs text-muted-foreground">
                        {t('broadcasts.timezoneHint', { zone: editor.zone })}
                      </span>
                    </div>
                    <Input
                      id={ids.at}
                      type="datetime-local"
                      value={editor.scheduleAt}
                      max={localDateTimeValue(Date.now() + maxScheduleAheadMs)}
                      aria-describedby={ids.atHint}
                      disabled={editor.pending}
                      onChange={event => editor.setScheduleAt(event.target.value)}
                    />
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    loading={(editor.saving || editor.sending) && editor.confirm === 'schedule'}
                    disabled={!editor.ready || !enabled || editor.pending || editor.scheduleAt === ''}
                    icon={<Clock className="size-4" />}
                    onClick={editor.requestSchedule}
                  >
                    {t('broadcasts.schedule')}
                  </Button>
                </div>
              </>
            ) : null}
          </div>
          {!canManage ? (
            <p className="text-sm text-muted-foreground">{t('broadcasts.managersSend')}</p>
          ) : null}
        </form>
        {editor.activeTemplate !== null ? (
          <BroadcastPreview
            key={editor.activeTemplate.id}
            template={editor.activeTemplate}
            values={editor.variables}
          />
        ) : null}
      </div>
      {editor.confirm === 'send' || editor.confirm === 'schedule' ? (
        <ConfirmDialog
          title={t(
            editor.confirm === 'send' ? 'broadcasts.sendTitle' : 'broadcasts.scheduleTitle'
          )}
          description={t(
            editor.confirm === 'send'
              ? 'broadcasts.sendDescription'
              : 'broadcasts.scheduleDescription'
          )}
          confirmLabel={t(
            editor.confirm === 'send' ? 'broadcasts.sendNow' : 'broadcasts.schedule'
          )}
          pending={editor.saving || editor.sending}
          onCancel={() => editor.setConfirm(null)}
          onConfirm={() => void editor.start(editor.confirm === 'schedule')}
        />
      ) : null}
      {editor.confirm === 'delete' ? (
        <ConfirmDialog
          title={t('broadcasts.deleteTitle')}
          description={t('broadcasts.deleteDescription', { name: broadcast?.name ?? editor.name })}
          confirmLabel={t('actions.delete')}
          destructive
          pending={editor.removing}
          onCancel={() => editor.setConfirm(null)}
          onConfirm={() => void editor.confirmDelete()}
        />
      ) : null}
      {editor.confirm === 'discard' ? (
        <ConfirmDialog
          title={t('broadcasts.unsavedTitle')}
          description={t('broadcasts.unsavedDescription')}
          confirmLabel={t('broadcasts.discard')}
          cancelLabel={t('broadcasts.keep')}
          destructive
          pending={false}
          onCancel={() => editor.setConfirm(null)}
          onConfirm={() => {
            editor.setConfirm(null);
            onBack();
          }}
        />
      ) : null}
    </div>
  );
}
