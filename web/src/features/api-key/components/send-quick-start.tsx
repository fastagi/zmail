'use client';

import { useMemo, useState, useSyncExternalStore } from 'react';
import Link from 'next/link';
import { ArrowRight, Building2, Copy, Inbox } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { env } from '@/config/env';
import type { SendReadiness } from '@/features/api-key/hooks/use-send-readiness';
import { buildSendSnippets, type SendSnippets } from '@/features/api-key/utils/send-snippets';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import { mailOrganizationHref } from '@/features/mailbox/hooks/use-mail-organization';
import { useT } from '@/i18n';

/** Stands in for the key once its plaintext is no longer shown. */
export const API_KEY_PLACEHOLDER = 'YOUR_API_KEY';

const LANGUAGES = ['curl', 'node', 'python'] as const;
type Language = (typeof LANGUAGES)[number];
const LANGUAGE_LABELS: Record<Language, string> = { curl: 'curl', node: 'Node.js', python: 'Python' };

// Without a configured API origin the samples use this site's own origin, read after hydration.
const subscribeNever = () => () => {};
const readSiteOrigin = () => window.location.origin;
const readNoOrigin = () => '';

/**
 * How to send a first message with a key: what is still missing (an organization, a mailbox), or
 * ready-to-run code with the real endpoint, organization, and one of the user's own mailboxes.
 */
/** The limits of a just-created key, which the example must respect. */
export interface QuickStartRestriction {
  /** The key acts only in this organization, so the example sends no `Organization-Id`. */
  organizationId?: number;
  /** The key sends only from these domains, so the example sends from one of them. */
  sendingDomains?: readonly string[];
}

export function SendQuickStart({
  readiness,
  apiKey = API_KEY_PLACEHOLDER,
  idPrefix,
  restriction,
}: {
  readiness: SendReadiness;
  restriction?: QuickStartRestriction;
  /** The plaintext key right after creation; a placeholder otherwise. */
  apiKey?: string;
  /** Keeps form ids unique when the page shows the quick start twice. */
  idPrefix: string;
}) {
  const t = useT('settings.api.quickStart');
  if (readiness.status === 'loading') {
    return <Skeleton className="h-48 w-full" aria-hidden="true" />;
  }
  if (readiness.status === 'unavailable' || readiness.status === 'error') return null;
  if (readiness.status === 'no-organization') {
    return (
      <SetupStep
        icon={<Building2 className="size-4" aria-hidden="true" />}
        title={t('noOrganizationTitle')}
        description={t('noOrganizationDescription')}
        action={{ href: '/console/organizations', label: t('createOrganization') }}
      />
    );
  }
  if (readiness.status === 'no-mailbox') {
    const { organization } = readiness;
    return (
      <SetupStep
        icon={<Inbox className="size-4" aria-hidden="true" />}
        title={t('noMailboxTitle')}
        description={
          organization.canManage
            ? t('noMailboxManager', { organization: organization.name })
            : t('noMailboxMember', { organization: organization.name })
        }
        action={
          organization.canManage
            ? {
                // `new=mailbox` opens the create form, as after creating an organization.
                href: `${mailOrganizationHref('/console/mailboxes', organization.id)}&new=mailbox`,
                label: t('createMailbox'),
              }
            : undefined
        }
      />
    );
  }
  return (
    <ReadyQuickStart
      apiKey={apiKey}
      idPrefix={idPrefix}
      organizationId={readiness.organization.id}
      organizationName={readiness.organization.name}
      senders={readiness.senders}
      recipient={readiness.recipient}
      restriction={restriction}
    />
  );
}

function SetupStep({
  icon,
  title,
  description,
  action,
}: {
  icon: React.ReactNode;
  title: string;
  description: string;
  action?: { href: string; label: string };
}) {
  return (
    <Alert>
      {icon}
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{description}</p>
        {action ? (
          <Button asChild size="sm" variant="outline">
            <Link href={action.href}>
              {action.label}
              <ArrowRight className="size-4" aria-hidden="true" />
            </Link>
          </Button>
        ) : null}
      </AlertDescription>
    </Alert>
  );
}

function ReadyQuickStart({
  apiKey,
  idPrefix,
  organizationId,
  organizationName,
  senders: allSenders,
  recipient,
  restriction,
}: {
  apiKey: string;
  idPrefix: string;
  organizationId: number;
  organizationName: string;
  senders: readonly string[];
  recipient: string;
  restriction?: QuickStartRestriction;
}) {
  const t = useT('settings.api.quickStart');
  const bound = restriction?.organizationId === organizationId;
  const allowedDomains = restriction?.sendingDomains;
  const allowedSenders = allowedDomains
    ? allSenders.filter(sender => allowedDomains.includes(senderDomain(sender)))
    : allSenders;
  // With no mailbox on an allowed domain the example keeps the user's own mailboxes, with a hint.
  const noAllowedSender = allowedSenders.length === 0;
  const senders = noAllowedSender ? allSenders : allowedSenders;
  const [chosenSender, setChosenSender] = useState(senders[0]);
  const from = senders.includes(chosenSender) ? chosenSender : senders[0];
  const siteOrigin = useSyncExternalStore(subscribeNever, readSiteOrigin, readNoOrigin);
  const snippets = useMemo(
    () =>
      buildSendSnippets({
        apiOrigin: env.NEXT_PUBLIC_DEVELOPER_API_ORIGIN ?? siteOrigin,
        organizationId,
        omitOrganizationHeader: bound,
        apiKey,
        from,
        to: recipient,
      }),
    [apiKey, bound, from, organizationId, recipient, siteOrigin]
  );
  const senderId = `${idPrefix}-sender`;

  return (
    <div className="min-w-0 space-y-4">
      {senders.length > 1 ? (
        <div className="flex min-w-0 flex-col gap-1.5 sm:flex-row sm:items-center sm:gap-3">
          <Label htmlFor={senderId} className="shrink-0">
            {t('from')}
          </Label>
          <select
            id={senderId}
            value={from}
            onChange={event => setChosenSender(event.target.value)}
            className={`${nativeSelectClassName} w-full sm:w-auto sm:max-w-sm`}
          >
            {senders.map(sender => (
              <option key={sender} value={sender}>
                {sender}
              </option>
            ))}
          </select>
        </div>
      ) : null}
      <CodeSamples snippets={snippets} />
      <ul className="space-y-1.5 text-sm text-muted-foreground">
        {bound ? (
          <li>{t('boundOrganization', { organization: organizationName })}</li>
        ) : (
          <li>
            <code className="font-mono text-foreground">Organization-Id: {organizationId}</code>{' '}
            {t('organizationHeader', { organization: organizationName })}
          </li>
        )}
        <li>{t('fromHint', { from })}</li>
        {allowedDomains && noAllowedSender ? (
          <li className="break-words">
            {t('noAllowedSender', { domains: allowedDomains.join(', ') })}
          </li>
        ) : null}
        <li>
          {t('recipientHint')}{' '}
          <Link
            href={mailOrganizationHref('/console/emails', organizationId)}
            className="font-medium text-foreground underline underline-offset-4"
          >
            {t('emailLog')}
          </Link>
        </li>
      </ul>
    </div>
  );
}

function senderDomain(address: string): string {
  return address.slice(address.lastIndexOf('@') + 1).toLowerCase();
}

function CodeSamples({ snippets }: { snippets: SendSnippets }) {
  const t = useT('settings.api.quickStart');
  const [language, setLanguage] = useState<Language>('curl');
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(snippets[language]);
      toast.success(t('copied'));
    } catch {
      toast.error(t('copyError'));
    }
  };
  return (
    <section className="min-w-0 space-y-2">
      <div className="flex items-center justify-between gap-2">
        <div
          role="group"
          aria-label={t('language')}
          className="inline-flex rounded-md bg-muted p-0.5"
        >
          {LANGUAGES.map(item => (
            <Button
              key={item}
              type="button"
              size="sm"
              variant={language === item ? 'secondary' : 'ghost'}
              aria-pressed={language === item}
              onClick={() => setLanguage(item)}
            >
              {LANGUAGE_LABELS[item]}
            </Button>
          ))}
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          icon={<Copy className="size-4" />}
          onClick={copy}
        >
          {t('copy')}
        </Button>
      </div>
      <pre
        aria-label={t('samples')}
        tabIndex={0}
        className="max-h-80 max-w-full overflow-auto rounded-md border bg-muted p-4 font-mono text-xs leading-relaxed whitespace-pre text-foreground focus-ring"
      >
        <code>{snippets[language]}</code>
      </pre>
    </section>
  );
}
