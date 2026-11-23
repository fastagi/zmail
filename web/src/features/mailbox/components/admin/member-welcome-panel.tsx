'use client';

import { useId, useRef, useState, type FormEvent } from 'react';
import { AlertCircle, Eye, RefreshCw, RotateCcw } from 'lucide-react';
import { toast } from 'sonner';

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
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import {
  useMemberWelcomeSetting,
  usePreviewMemberWelcome,
  useUpdateMemberWelcomeSetting,
} from '@/features/mailbox/hooks/use-member-welcome';
import {
  MEMBER_WELCOME_BODY_MAX_LENGTH,
  MEMBER_WELCOME_SUBJECT_MAX_LENGTH,
  memberWelcomeLanguageSchema,
  type MemberWelcomeLanguage,
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

function hasFieldError(field: string) {
  return (error: unknown) =>
    error instanceof ApiError && (error.fieldErrors?.[field]?.length ?? 0) > 0;
}

function isValidationFailure(error: unknown) {
  return error instanceof ApiError && error.errorCode === ApiErrorCode.COMMON_VALIDATION_FAILED;
}
