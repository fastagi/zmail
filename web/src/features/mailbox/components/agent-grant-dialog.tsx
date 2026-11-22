'use client';

import { useId, useMemo, useState } from 'react';
import Link from 'next/link';
import { AlertTriangle, KeyRound, ShieldAlert } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import { ROUTES } from '@/constants/routes';
import { useApiKeys } from '@/features/api-key/hooks/use-api-keys';
import {
  MAIL_AGENT_FILTER_MAX_ENTRIES,
  MAIL_AGENT_GRANT_DEFAULT_DAYS,
  MAIL_AGENT_GRANT_EXPIRY_DAYS,
  MAIL_AGENT_GRANT_MAX_DAYS,
  MAIL_AGENT_GRANT_MAX_PER_MAILBOX,
  type MailAgentGrant,
} from '@/features/mailbox/agent-grant-schemas';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import { useIssueAgentGrant } from '@/features/mailbox/hooks/use-agent-grants';
import { useDialogReturnFocus } from '@/features/mailbox/hooks/use-dialog-return-focus';
import { useMailLabels } from '@/features/mailbox/hooks/use-mailbox';
import type { Mailbox } from '@/features/mailbox/types';
import { resolveAgentGrantErrorKey } from '@/features/mailbox/utils/agent-grant-error';
import { isUsableAgentKey } from '@/features/mailbox/utils/agent-grant-access';
import {
  agentGrantInput,
  validateAgentGrantForm,
  type AgentGrantFormError,
  type AgentGrantFormValues,
} from '@/features/mailbox/utils/agent-grant-form';
import { isConnectedMailbox } from '@/features/mailbox/utils/connected-mailbox';
import { useT } from '@/i18n';

const initialValues: AgentGrantFormValues = {
  apiKeyId: null,
  scope: 'all',
  senders: '',
  labelIds: [],
  draft: false,
  revealSecrets: false,
  days: MAIL_AGENT_GRANT_DEFAULT_DAYS,
};

/**
 * "Authorize an agent": the key, what it receives (the whole mailbox or sender and label
 * filters), what it may do (read always; draft; reveal codes), and the expiry. The limits are
 * checked before submitting and the API judges the request. Focus returns to the control that
 * opened the dialog however it closes.
 */
export function AgentGrantDialog({
  mailbox,
  grants,
  open,
  onOpenChange,
}: {
  mailbox: Pick<Mailbox, 'id' | 'origin' | 'access'>;
  grants: readonly MailAgentGrant[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT('mailbox');
  const onCloseAutoFocus = useDialogReturnFocus(open);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        size="lg"
        className="max-h-[90vh] overflow-y-auto"
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <DialogHeader>
          <DialogTitle>{t('agents.form.title')}</DialogTitle>
          <DialogDescription>{t('agents.form.description')}</DialogDescription>
        </DialogHeader>
        {open ? (
          <GrantForm mailbox={mailbox} grants={grants} onDone={() => onOpenChange(false)} />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function GrantForm({
  mailbox,
  grants,
  onDone,
}: {
  mailbox: Pick<Mailbox, 'id' | 'origin' | 'access'>;
  grants: readonly MailAgentGrant[];
  onDone: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const issue = useIssueAgentGrant(organizationId, mailbox.id);
  const keys = useApiKeys();
  const labels = useMailLabels(organizationId, mailbox.id);
  const ids = {
    key: useId(),
    keyHint: useId(),
    scope: useId(),
    senders: useId(),
    sendersHint: useId(),
    sendersWarning: useId(),
    labels: useId(),
    capabilities: useId(),
    draftHint: useId(),
    revealWarning: useId(),
    expiry: useId(),
  };
  const [values, setValues] = useState<AgentGrantFormValues>(initialValues);
  const [errors, setErrors] = useState<AgentGrantFormError[]>([]);
  const [failure, setFailure] = useState<string | null>(null);

  const connected = isConnectedMailbox(mailbox);
  const draftBlocked = connected
    ? 'agents.form.draftConnected'
    : mailbox.access.send
      ? null
      : 'agents.form.draftNoSend';
  const active = grants.filter(grant => grant.status === 'active');
  const activeKeyIds = useMemo(() => new Set(active.map(grant => grant.api_key_id)), [active]);
  const usableKeys = useMemo(() => {
    const now = new Date();
    return (keys.data?.items ?? []).filter(key => isUsableAgentKey(key, now));
  }, [keys.data]);

  const set = <K extends keyof AgentGrantFormValues>(name: K, value: AgentGrantFormValues[K]) => {
    setValues(current => ({ ...current, [name]: value }));
    setErrors([]);
    setFailure(null);
  };
  const errorFor = (field: AgentGrantFormError['field']) =>
    errors.find(error => error.field === field);
  const message = (error: AgentGrantFormError | undefined): string | null => {
    if (!error) return null;
    switch (error.key) {
      case 'agents.form.errors.senderInvalid':
        return t(error.key, { value: error.value });
      case 'agents.form.errors.sendersTooMany':
      case 'agents.form.errors.labelsTooMany':
      case 'agents.form.errors.expiry':
      case 'agents.form.limitReached':
        return t(error.key, { max: error.max });
      default:
        return t(error.key);
    }
  };

  const submit = async () => {
    const found = validateAgentGrantForm(values, {
      activeGrants: active.length,
      activeKeyIds,
      maxGrants: MAIL_AGENT_GRANT_MAX_PER_MAILBOX,
    });
    setErrors(found);
    setFailure(null);
    if (found.length > 0) return;
    try {
      await issue.mutateAsync(agentGrantInput(values, new Date()));
      toast.success(t('agents.form.issued'));
      onDone();
    } catch (error) {
      setFailure(t(resolveAgentGrantErrorKey(error)));
    }
  };

  const keyError = message(errorFor('key'));
  const sendersError = message(errorFor('senders'));
  const labelsError = message(errorFor('labels'));
  const filtersError = message(errorFor('filters'));
  const limitError = message(errorFor('limit'));
  const expiryError = message(errorFor('expiry'));
  const pending = issue.isPending;

  return (
    <form
      noValidate
      className="space-y-5"
      onSubmit={event => {
        event.preventDefault();
        void submit();
      }}
    >
      {limitError || failure ? (
        <Alert variant="destructive" role="alert">
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertDescription>{limitError ?? failure}</AlertDescription>
        </Alert>
      ) : null}

      <div className="space-y-2">
        <Label htmlFor={ids.key}>{t('agents.form.key')}</Label>
        {keys.isPending ? (
          <Skeleton className="h-9 w-full" />
        ) : keys.error ? (
          <p role="alert" className="text-sm text-error">
            {t('agents.form.keysFailed')}
          </p>
        ) : usableKeys.length === 0 ? (
          <div className="space-y-2 rounded-md border p-3 text-sm">
            <p>{t('agents.form.noKeys')}</p>
            <Button asChild size="sm" variant="outline" icon={<KeyRound className="size-3.5" />}>
              <Link href={ROUTES.CONSOLE.SETTINGS}>{t('agents.form.createKey')}</Link>
            </Button>
          </div>
        ) : (
          <>
            <select
              id={ids.key}
              className={`${nativeSelectClassName} w-full`}
              value={values.apiKeyId === null ? '' : String(values.apiKeyId)}
              disabled={pending}
              aria-invalid={keyError ? true : undefined}
              aria-describedby={keyError ? `${ids.keyHint} ${ids.key}-error` : ids.keyHint}
              onChange={event =>
                set('apiKeyId', event.target.value === '' ? null : Number(event.target.value))
              }
            >
              <option value="">{t('agents.form.chooseKey')}</option>
              {usableKeys.map(key => (
                <option key={key.id} value={String(key.id)}>
                  {t('agents.grants.key', { name: key.name, prefix: key.key_prefix })}
                </option>
              ))}
            </select>
            <p id={ids.keyHint} className="text-xs text-muted-foreground">
              {t('agents.form.keyHint')}
            </p>
            {keyError ? (
              <p id={`${ids.key}-error`} role="alert" className="text-sm text-error">
                {keyError}
              </p>
            ) : null}
          </>
        )}
      </div>

      <fieldset className="space-y-2">
        <legend id={ids.scope} className="text-sm font-medium">
          {t('agents.form.scope')}
        </legend>
        <div role="radiogroup" aria-labelledby={ids.scope} className="space-y-2">
          {(['all', 'filtered'] as const).map(option => (
            <div key={option} className="flex items-center gap-2 text-sm">
              <input
                id={`${ids.scope}-${option}`}
                type="radio"
                name={`${ids.scope}-scope`}
                value={option}
                checked={values.scope === option}
                disabled={pending}
                onChange={() => set('scope', option)}
              />
              <label htmlFor={`${ids.scope}-${option}`}>
                {t(option === 'all' ? 'agents.form.scopeAll' : 'agents.form.scopeFiltered')}
              </label>
            </div>
          ))}
        </div>
        {values.scope === 'filtered' ? (
          <div className="space-y-4 border-l-2 pl-3 sm:pl-4">
            <div className="space-y-2">
              <Label htmlFor={ids.senders}>{t('agents.form.senders')}</Label>
              <Textarea
                id={ids.senders}
                rows={3}
                value={values.senders}
                disabled={pending}
                autoCapitalize="none"
                spellCheck={false}
                placeholder="example.com, billing@vendor.test"
                aria-invalid={sendersError ? true : undefined}
                aria-describedby={`${ids.sendersHint} ${ids.sendersWarning}${
                  sendersError ? ` ${ids.senders}-error` : ''
                }${filtersError ? ` ${ids.scope}-filters-error` : ''}`}
                onChange={event => set('senders', event.target.value)}
              />
              <p id={ids.sendersHint} className="text-xs text-muted-foreground">
                {t('agents.form.sendersHint', { max: MAIL_AGENT_FILTER_MAX_ENTRIES })}
              </p>
              {sendersError ? (
                <p id={`${ids.senders}-error`} role="alert" className="text-sm text-error">
                  {sendersError}
                </p>
              ) : null}
              <p
                id={ids.sendersWarning}
                className="flex items-start gap-2 rounded-md border border-warning/40 bg-warning/10 p-2 text-xs"
              >
                <ShieldAlert className="mt-0.5 size-3.5 shrink-0 text-warning" aria-hidden="true" />
                <span>{t('agents.form.sendersWarning')}</span>
              </p>
            </div>
            <fieldset
              className="space-y-2"
              aria-invalid={labelsError ? true : undefined}
              aria-describedby={
                [
                  labelsError ? `${ids.labels}-error` : '',
                  filtersError ? `${ids.scope}-filters-error` : '',
                ]
                  .filter(Boolean)
                  .join(' ') || undefined
              }
            >
              <legend id={ids.labels} className="text-sm font-medium">
                {t('agents.form.labels')}
              </legend>
              <p className="text-xs text-muted-foreground">
                {t('agents.form.labelsHint', { max: MAIL_AGENT_FILTER_MAX_ENTRIES })}
              </p>
              {labels.isPending ? (
                <Skeleton className="h-6 w-1/2" />
              ) : (labels.data ?? []).length === 0 ? (
                <p className="text-xs text-muted-foreground">{t('agents.form.noLabels')}</p>
              ) : (
                <ul className="flex flex-wrap gap-x-4 gap-y-2">
                  {(labels.data ?? []).map(label => {
                    const checked = values.labelIds.includes(label.id);
                    const id = `${ids.labels}-${label.id}`;
                    return (
                      <li key={label.id} className="flex items-center gap-2 text-sm">
                        <Checkbox
                          id={id}
                          checked={checked}
                          disabled={pending}
                          onCheckedChange={next =>
                            set(
                              'labelIds',
                              next === true
                                ? [...values.labelIds, label.id]
                                : values.labelIds.filter(labelId => labelId !== label.id)
                            )
                          }
                        />
                        <label htmlFor={id}>{label.name}</label>
                      </li>
                    );
                  })}
                </ul>
              )}
              {labelsError ? (
                <p id={`${ids.labels}-error`} role="alert" className="text-sm text-error">
                  {labelsError}
                </p>
              ) : null}
            </fieldset>
            {filtersError ? (
              <p id={`${ids.scope}-filters-error`} role="alert" className="text-sm text-error">
                {filtersError}
              </p>
            ) : null}
          </div>
        ) : null}
      </fieldset>

      <fieldset className="space-y-3">
        <legend id={ids.capabilities} className="text-sm font-medium">
          {t('agents.form.capabilities')}
        </legend>
        <div className="flex items-start gap-2 text-sm">
          <Checkbox id={`${ids.capabilities}-read`} checked disabled className="mt-0.5" />
          <label htmlFor={`${ids.capabilities}-read`}>{t('agents.form.readAlways')}</label>
        </div>
        <div className="flex items-start gap-2 text-sm">
          <Checkbox
            id={`${ids.capabilities}-draft`}
            className="mt-0.5"
            checked={values.draft && draftBlocked === null}
            disabled={pending || draftBlocked !== null}
            aria-describedby={ids.draftHint}
            onCheckedChange={next => set('draft', next === true)}
          />
          <div className="space-y-1">
            <label htmlFor={`${ids.capabilities}-draft`}>{t('agents.form.draft')}</label>
            <p id={ids.draftHint} className="text-xs text-muted-foreground">
              {t(draftBlocked ?? 'agents.form.draftHint')}
            </p>
          </div>
        </div>
        <div className="flex items-start gap-2 text-sm">
          <Checkbox
            id={`${ids.capabilities}-reveal`}
            className="mt-0.5"
            checked={values.revealSecrets}
            disabled={pending}
            aria-describedby={ids.revealWarning}
            onCheckedChange={next => set('revealSecrets', next === true)}
          />
          <div className="space-y-1">
            <label htmlFor={`${ids.capabilities}-reveal`}>{t('agents.form.reveal')}</label>
            <p id={ids.revealWarning} className="flex items-start gap-1.5 text-xs text-warning">
              <AlertTriangle className="mt-0.5 size-3 shrink-0" aria-hidden="true" />
              <span>{t('agents.form.revealWarning')}</span>
            </p>
          </div>
        </div>
      </fieldset>

      <fieldset className="space-y-2">
        <legend id={ids.expiry} className="text-sm font-medium">
          {t('agents.form.expiry')}
        </legend>
        <div
          role="radiogroup"
          aria-labelledby={ids.expiry}
          aria-invalid={expiryError ? true : undefined}
          aria-describedby={`${ids.expiry}-hint${expiryError ? ` ${ids.expiry}-error` : ''}`}
          className="flex flex-wrap gap-4"
        >
          {MAIL_AGENT_GRANT_EXPIRY_DAYS.map(days => (
            <div key={days} className="flex items-center gap-2 text-sm">
              <input
                id={`${ids.expiry}-${days}`}
                type="radio"
                name={`${ids.expiry}-days`}
                value={days}
                checked={values.days === days}
                disabled={pending}
                onChange={() => set('days', days)}
              />
              <label htmlFor={`${ids.expiry}-${days}`}>
                {t('agents.form.days', { count: days })}
              </label>
            </div>
          ))}
        </div>
        <p id={`${ids.expiry}-hint`} className="text-xs text-muted-foreground">
          {t('agents.form.expiryHint', { max: MAIL_AGENT_GRANT_MAX_DAYS })}
        </p>
        {expiryError ? (
          <p id={`${ids.expiry}-error`} role="alert" className="text-sm text-error">
            {expiryError}
          </p>
        ) : null}
      </fieldset>

      <p className="rounded-md bg-muted p-3 text-xs text-muted-foreground">
        {t('agents.neverSends')}
      </p>

      <DialogFooter>
        <Button type="button" variant="outline" disabled={pending} onClick={onDone}>
          {t('agents.form.cancel')}
        </Button>
        <Button type="submit" loading={pending} disabled={usableKeys.length === 0}>
          {t('agents.form.submit')}
        </Button>
      </DialogFooter>
    </form>
  );
}
