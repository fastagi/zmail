'use client';

import type { RefObject } from 'react';
import { Plus } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  MAIL_DOMAIN_MAX_PER_ORGANIZATION,
  MAIL_DOMAIN_NAME_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import type { MailDomainCapability } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

import { useConnectDomainForm } from './use-connect-domain-form';

export { connectDomainErrorKey } from './use-connect-domain-form';

export function ConnectDomainForm({
  another,
  formRef,
  inputRef,
}: {
  /** The organization already has a domain, so this one is another. */
  another: boolean;
  formRef: RefObject<HTMLFormElement | null>;
  inputRef: RefObject<HTMLInputElement | null>;
}) {
  const t = useT('mailbox');
  const {
    name,
    value,
    capability,
    current,
    confirming,
    error,
    busy,
    lookUp,
    choose,
    changeName,
    submit,
  } = useConnectDomainForm();

  const capabilities: { value: MailDomainCapability; label: string; hint: string }[] = [
    {
      value: 'send_receive',
      label: t('domain.capabilitySendReceive'),
      hint: t('domain.capabilitySendReceiveHint'),
    },
    {
      value: 'send_only',
      label: t('domain.capabilitySendOnly'),
      hint: t('domain.capabilitySendOnlyHint'),
    },
  ];

  return (
    <form
      ref={formRef}
      className="scroll-mt-4 space-y-3 rounded-md border bg-muted/20 p-4"
      aria-labelledby="mail-domain-connect-title"
      onSubmit={submit}
    >
      <div className="space-y-1">
        <h3 id="mail-domain-connect-title" className="font-semibold">
          {t(another ? 'domain.connectAnotherTitle' : 'domain.connectTitle')}
        </h3>
        {another ? (
          <p className="text-xs text-muted-foreground">
            {t('domain.connectAnotherHint', { max: MAIL_DOMAIN_MAX_PER_ORGANIZATION })}
          </p>
        ) : null}
        <p className="text-xs text-muted-foreground">{t('domain.connectWhen')}</p>
      </div>
      <div className="space-y-2">
        <Label htmlFor="mail-domain-name-input">{t('domain.connectLabel')}</Label>
        <div className="flex flex-wrap items-center gap-2">
          <Input
            ref={inputRef}
            id="mail-domain-name-input"
            className="w-full max-w-sm flex-1"
            value={name}
            maxLength={MAIL_DOMAIN_NAME_MAX_LENGTH}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            disabled={busy}
            placeholder={t('domain.connectPlaceholder')}
            aria-describedby="mail-domain-name-hint"
            onChange={event => changeName(event.target.value)}
            onBlur={() => {
              if (value !== '') void lookUp(value);
            }}
          />
          <Button type="submit" size="sm" loading={busy} icon={<Plus className="size-4" />}>
            {t('domain.connectAction')}
          </Button>
        </div>
        <p id="mail-domain-name-hint" className="text-xs text-muted-foreground">
          {t('domain.connectHint')}
        </p>
      </div>
      <fieldset className="space-y-2" disabled={busy}>
        <legend className="text-sm font-medium">{t('domain.capabilityLabel')}</legend>
        <div className="flex flex-wrap gap-2">
          {capabilities.map(option => (
            <label
              key={option.value}
              className={cn(
                'flex max-w-xs min-w-56 flex-1 cursor-pointer gap-2 rounded-md border p-2.5 text-sm has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50',
                capability === option.value && 'border-primary bg-primary/5'
              )}
            >
              <input
                type="radio"
                name="mail-domain-capability"
                value={option.value}
                checked={capability === option.value}
                className="mt-0.5 accent-primary"
                // Clicking the checked option is a choice too, though nothing changes.
                onClick={() => choose(option.value)}
                onChange={() => choose(option.value)}
              />
              <span className="space-y-0.5">
                <span className="block font-medium">{option.label}</span>
                <span className="block text-xs text-muted-foreground">{option.hint}</span>
              </span>
            </label>
          ))}
        </div>
      </fieldset>
      {current && current.suggested_capability === 'send_only' ? (
        <Alert
          data-testid="mail-domain-subdomain-hint"
          className="border-warning/40 bg-warning-subtle text-warning"
        >
          <AlertDescription className="break-words">
            {t('domain.subdomainHint', {
              domain: current.name,
              hosts: current.existing_mx.join(', '),
              apex: current.apex,
            })}
          </AlertDescription>
        </Alert>
      ) : null}
      {confirming ? (
        <p role="status" className="text-sm text-muted-foreground">
          {t('domain.capabilityConfirm')}
        </p>
      ) : null}
      {error ? (
        <p role="alert" className="text-sm text-error">
          {error}
        </p>
      ) : null}
    </form>
  );
}
