'use client';

import { useId, useRef, useState, type FormEvent } from 'react';
import { AlertCircle, Eye, RefreshCw, RotateCcw, Send } from 'lucide-react';
import { useFormatter } from 'next-intl';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
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
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import {
  useMemberWelcomeMails,
  useMemberWelcomeSetting,
  usePreviewMemberWelcome,
  useResendMemberWelcome,
  useUpdateMemberWelcomeSetting,
} from '@/features/mailbox/hooks/use-member-welcome';
import {
  MEMBER_WELCOME_BODY_MAX_LENGTH,
  MEMBER_WELCOME_SUBJECT_MAX_LENGTH,
  memberWelcomeLanguageSchema,
  type MemberWelcomeDeliveryStatus,
  type MemberWelcomeLanguage,
  type MemberWelcomeMail,
  type MemberWelcomeSetting,
  type MemberWelcomeVariable,
} from '@/features/mailbox/member-welcome-schemas';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';

const LANGUAGES = memberWelcomeLanguageSchema.options;

/**
 * The organization's member welcome email: on or off, its language, and its subject and message,
 * with a preview. Owners and admins only; see `contracts/MAILBOXES.md`, Member Welcome Mail.
 */
export function MemberWelcomePanel({ organizationId }: { organizationId: number }) {
  const t = useT('mailbox');
  const query = useMemberWelcomeSetting(organizationId);

  return (
    <section className="border-t py-6" aria-labelledby="member-welcome-heading">
      <div className="mb-5">
        <h2 id="member-welcome-heading" className="text-base font-semibold">
          {t('admin.memberWelcome.title')}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">{t('admin.memberWelcome.description')}</p>
      </div>
      {query.isPending ? (
        <div className="max-w-2xl space-y-4" aria-hidden="true">
          <Skeleton className="h-6 w-64 max-w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : query.error || !query.data ? (
        <Alert variant="destructive">
          <AlertCircle aria-hidden="true" className="size-4" />
          <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
            <span>{t('admin.memberWelcome.loadFailed')}</span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<RefreshCw className="size-3.5" />}
              onClick={() => void query.refetch()}
            >
              {t('admin.memberWelcome.retry')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <MemberWelcomeForm
          key={query.data.updated_at ?? 'standard'}
          organizationId={organizationId}
          setting={query.data}
        />
      )}
      <MemberWelcomeMails organizationId={organizationId} />
    </section>
  );
}

function MemberWelcomeForm({
  organizationId,
  setting,
}: {
  organizationId: number;
  setting: MemberWelcomeSetting;
}) {
  const t = useT('mailbox');
  const id = useId();
  const bodyRef = useRef<HTMLTextAreaElement>(null);
  const update = useUpdateMemberWelcomeSetting(organizationId);
  const preview = usePreviewMemberWelcome(organizationId);
  const [enabled, setEnabled] = useState(setting.enabled);
  const [language, setLanguage] = useState<MemberWelcomeLanguage>(setting.language);
  const [subject, setSubject] = useState(setting.subject);
  const [body, setBody] = useState(setting.body);

  const standard = setting.defaults[language];
  const isStandard = subject === standard.subject && body === standard.body;
  const fieldError = (field: 'subject' | 'body') =>
    [update.error, preview.error].some(hasFieldError(field))
      ? t('admin.memberWelcome.fieldInvalid')
      : undefined;
  const failure = update.error ?? preview.error;

  const changeLanguage = (next: string) => {
    if (!LANGUAGES.includes(next as MemberWelcomeLanguage)) return;
    const nextLanguage = next as MemberWelcomeLanguage;
    // Copy still in the standard text follows the language; edited copy stays as typed.
    const current = setting.defaults[language];
    if (subject === current.subject) setSubject(setting.defaults[nextLanguage].subject);
    if (body === current.body) setBody(setting.defaults[nextLanguage].body);
    setLanguage(nextLanguage);
    preview.reset();
  };

  const insert = (variable: MemberWelcomeVariable) => {
    const token = `{{${variable}}}`;
    const element = bodyRef.current;
    const start = element?.selectionStart ?? body.length;
    const end = element?.selectionEnd ?? body.length;
    const next = body.slice(0, start) + token + body.slice(end);
    if (next.length > MEMBER_WELCOME_BODY_MAX_LENGTH) return;
    setBody(next);
    requestAnimationFrame(() => {
      element?.focus();
      element?.setSelectionRange(start + token.length, start + token.length);
    });
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    update.mutate(
      { enabled, language, subject, body },
      { onSuccess: () => toast.success(t('admin.memberWelcome.saved')) }
    );
  };

  return (
    <form onSubmit={submit} className="max-w-2xl space-y-5" aria-busy={update.isPending}>
      <div className="flex items-start gap-3">
        <Switch
          id={`${id}-enabled`}
          checked={enabled}
          disabled={update.isPending}
          onCheckedChange={setEnabled}
        />
        <Label htmlFor={`${id}-enabled`} className="leading-5">
          {t('admin.memberWelcome.enabled')}
        </Label>
      </div>

      <div className="space-y-2">
        <Label htmlFor={`${id}-language`}>{t('admin.memberWelcome.language')}</Label>
        <Select value={language} onValueChange={changeLanguage} disabled={update.isPending}>
          <SelectTrigger id={`${id}-language`} className="w-full sm:w-56">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {LANGUAGES.map(value => (
              <SelectItem key={value} value={value}>
                {t(`admin.memberWelcome.languages.${value}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-2">
        <Label htmlFor={`${id}-subject`}>{t('admin.memberWelcome.subject')}</Label>
        <Input
          id={`${id}-subject`}
          value={subject}
          maxLength={MEMBER_WELCOME_SUBJECT_MAX_LENGTH}
          disabled={update.isPending}
          errorText={fieldError('subject')}
          onChange={event => {
            setSubject(event.target.value);
            update.reset();
          }}
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor={`${id}-body`}>{t('admin.memberWelcome.body')}</Label>
        <div className="space-y-1.5">
          <p className="text-xs text-muted-foreground">
            {t('admin.memberWelcome.placeholders')} · {t('admin.memberWelcome.placeholdersHint')}
          </p>
          <div className="flex flex-wrap gap-2">
            {setting.variables.map(variable => (
              <Button
                key={variable}
                type="button"
                size="sm"
                variant="outline"
                disabled={update.isPending}
                onClick={() => insert(variable)}
              >
                {t(`admin.memberWelcome.variables.${variable}`)}
              </Button>
            ))}
          </div>
        </div>
        <Textarea
          ref={bodyRef}
          id={`${id}-body`}
          rows={9}
          value={body}
          maxLength={MEMBER_WELCOME_BODY_MAX_LENGTH}
          disabled={update.isPending}
          errorText={fieldError('body')}
          className="min-h-40 break-words"
          onChange={event => {
            setBody(event.target.value);
            update.reset();
          }}
        />
        <p className="text-xs text-muted-foreground">{t('admin.memberWelcome.bodyHint')}</p>
      </div>

      {failure ? (
        <Alert variant="destructive" role="alert">
          <AlertCircle aria-hidden="true" className="size-4" />
          <AlertDescription>
            {isValidationFailure(failure)
              ? t('admin.memberWelcome.invalid')
              : t(resolveMailboxErrorKey(failure))}
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="flex flex-wrap gap-2">
        <Button type="submit" loading={update.isPending}>
          {t('admin.memberWelcome.save')}
        </Button>
        <Button
          type="button"
          variant="outline"
          icon={<Eye className="size-4" />}
          loading={preview.isPending}
          disabled={update.isPending}
          onClick={() => preview.mutate({ language, subject, body })}
        >
          {t('admin.memberWelcome.preview')}
        </Button>
        <Button
          type="button"
          variant="ghost"
          icon={<RotateCcw className="size-4" />}
          disabled={update.isPending || isStandard}
          onClick={() => {
            setSubject(standard.subject);
            setBody(standard.body);
            update.reset();
            preview.reset();
          }}
        >
          {t('admin.memberWelcome.reset')}
        </Button>
      </div>

      {preview.data ? (
        <div
          className="space-y-3 rounded-md border bg-muted/30 p-4"
          aria-label={t('admin.memberWelcome.previewTitle')}
          role="region"
        >
          <p className="text-xs font-medium text-muted-foreground">
            {t('admin.memberWelcome.previewTitle')}
          </p>
          <div className="space-y-0.5">
            <p className="text-xs text-muted-foreground">
              {t('admin.memberWelcome.previewSubject')}
            </p>
            <p className="break-words text-sm font-medium">{preview.data.subject}</p>
          </div>
          <pre className="whitespace-pre-wrap break-words font-sans text-sm leading-6">
            {preview.data.text}
          </pre>
        </div>
      ) : null}
    </form>
  );
}

/** Send log failure codes with a plain explanation; any other shows a generic one. */
const KNOWN_FAILURES = {
  'send.sender_unverified': 'senderUnverified',
  'transport.permanent_bounce': 'permanentBounce',
  'transport.not_configured': 'notConfigured',
} as const satisfies Record<string, string>;

function failureKey(
  code: string | null
): (typeof KNOWN_FAILURES)[keyof typeof KNOWN_FAILURES] | 'other' {
  return code !== null && Object.hasOwn(KNOWN_FAILURES, code)
    ? KNOWN_FAILURES[code as keyof typeof KNOWN_FAILURES]
    : 'other';
}

const STATUS_VARIANT: Record<
  MemberWelcomeDeliveryStatus,
  'default' | 'secondary' | 'destructive' | 'outline'
> = {
  not_sent: 'outline',
  queued: 'secondary',
  sent: 'default',
  expired: 'default',
  failed: 'destructive',
  suppressed: 'destructive',
};

/**
 * Each member mailbox's latest welcome email, with a button to send it again. The list carries no
 * recipient address; see `contracts/MAILBOXES.md`, Welcome Mail Status.
 */
function MemberWelcomeMails({ organizationId }: { organizationId: number }) {
  const t = useT('mailbox');
  const query = useMemberWelcomeMails(organizationId);
  const resend = useResendMemberWelcome(organizationId);

  const send = (mail: MemberWelcomeMail) => {
    resend.mutate(mail.mailbox_id, {
      onSuccess: next => {
        if (next.status === 'suppressed') {
          toast.warning(t('admin.memberWelcome.mails.suppressedToast', { address: next.address }));
          return;
        }
        toast.success(
          t(
            mail.status === 'not_sent'
              ? 'admin.memberWelcome.mails.sentFirst'
              : 'admin.memberWelcome.mails.resent',
            { address: next.address }
          )
        );
      },
      onError: error => toast.error(t(resolveMailboxErrorKey(error))),
    });
  };

  return (
    <div className="mt-8 max-w-2xl" aria-labelledby="member-welcome-mails-heading" role="region">
      <h3 id="member-welcome-mails-heading" className="text-sm font-semibold">
        {t('admin.memberWelcome.mails.title')}
      </h3>
      <p className="mt-1 text-sm text-muted-foreground">
        {t('admin.memberWelcome.mails.description')}
      </p>
      <div className="mt-4">
        {query.isPending ? (
          <div className="space-y-2" aria-hidden="true">
            <Skeleton className="h-14 w-full" />
            <Skeleton className="h-14 w-full" />
          </div>
        ) : query.error || !query.data ? (
          <Alert variant="destructive">
            <AlertCircle aria-hidden="true" className="size-4" />
            <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
              <span>{t('admin.memberWelcome.mails.loadFailed')}</span>
              <Button
                type="button"
                size="sm"
                variant="outline"
                icon={<RefreshCw className="size-3.5" />}
                onClick={() => void query.refetch()}
              >
                {t('admin.memberWelcome.retry')}
              </Button>
            </AlertDescription>
          </Alert>
        ) : query.data.length === 0 ? (
          <p className="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
            {t('admin.memberWelcome.mails.empty')}
          </p>
        ) : (
          <ul className="divide-y rounded-md border">
            {query.data.map(mail => (
              <MemberWelcomeMailRow
                key={mail.mailbox_id}
                mail={mail}
                sending={resend.isPending && resend.variables === mail.mailbox_id}
                disabled={resend.isPending}
                onSend={() => send(mail)}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function MemberWelcomeMailRow({
  mail,
  sending,
  disabled,
  onSend,
}: {
  mail: MemberWelcomeMail;
  sending: boolean;
  disabled: boolean;
  onSend: () => void;
}) {
  const t = useT('mailbox');
  const format = useFormatter();
  const time = (value: string | null) =>
    value ? format.dateTime(new Date(value), { dateStyle: 'medium', timeStyle: 'short' }) : '';
  const detail = (() => {
    switch (mail.status) {
      case 'failed':
        return t(`admin.memberWelcome.mails.failure.${failureKey(mail.failure_code)}`);
      case 'sent':
        return t('admin.memberWelcome.mails.detail.sent', {
          time: time(mail.sent_at ?? mail.queued_at),
        });
      case 'queued':
      case 'expired':
        return t(`admin.memberWelcome.mails.detail.${mail.status}`, { time: time(mail.queued_at) });
      default:
        return t(`admin.memberWelcome.mails.detail.${mail.status}`);
    }
  })();

  return (
    <li className="flex flex-col gap-3 px-3 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-4">
      <div className="min-w-0 space-y-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span className="min-w-0 break-all text-sm font-medium">{mail.address}</span>
          <Badge variant={STATUS_VARIANT[mail.status]}>
            {t(`admin.memberWelcome.mails.status.${mail.status}`)}
          </Badge>
        </div>
        <p className="break-words text-xs text-muted-foreground">
          {[
            mail.display_name || null,
            mail.invitation_pending ? t('admin.memberWelcome.mails.invitationPending') : null,
            mail.resend_count > 0
              ? t('admin.memberWelcome.mails.resendCount', { count: mail.resend_count })
              : null,
          ]
            .filter(Boolean)
            .join(' \u00b7 ')}
        </p>
        <p
          className={
            mail.status === 'failed'
              ? 'break-words text-xs text-error'
              : 'break-words text-xs text-muted-foreground'
          }
        >
          {detail}
          {mail.status === 'failed' && mail.failure_code ? (
            <span className="ml-1 font-mono">({mail.failure_code})</span>
          ) : null}
        </p>
      </div>
      <Button
        type="button"
        size="sm"
        variant="outline"
        className="w-full shrink-0 sm:w-auto"
        icon={<Send className="size-3.5" />}
        loading={sending}
        disabled={disabled}
        aria-label={t('admin.memberWelcome.mails.resendLabel', { address: mail.address })}
        onClick={onSend}
      >
        {mail.status === 'not_sent'
          ? t('admin.memberWelcome.mails.send')
          : t('admin.memberWelcome.mails.resend')}
      </Button>
    </li>
  );
}

function hasFieldError(field: string) {
  return (error: unknown) =>
    error instanceof ApiError && (error.fieldErrors?.[field]?.length ?? 0) > 0;
}

function isValidationFailure(error: unknown) {
  return error instanceof ApiError && error.errorCode === ApiErrorCode.COMMON_VALIDATION_FAILED;
}
