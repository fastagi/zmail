'use client';

import { useEffect, useMemo, useState } from 'react';
import { Check, Copy, KeyRound, Send } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { env } from '@/config/env';
import { useApiKeys, useCreateApiKey } from '@/features/api-key/hooks/use-api-keys';
import type { ApiKey } from '@/features/api-key/types';
import {
  useTransactionalLog,
  useTransactionalLogEntry,
  useTransactionalTestSend,
} from '@/features/developer/hooks/use-developer';
import type {
  DeveloperMode,
  TransactionalLogEntry,
  TransactionalMessage,
} from '@/features/developer/types';
import { useDeveloperErrorMessage } from '@/features/developer/utils/developer-error';
import { logStatus } from '@/features/developer/utils/log-status';
import { buildSnippets, type Snippets } from '@/features/developer/utils/snippets';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import type { MailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailTemplate, useMailTemplates } from '@/features/mailbox/hooks/use-mail-templates';
import type { MailboxDomain } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** The only scope a quick-start key carries. */
const TRANSACTIONAL_SCOPE = 'mail:transactional';
/** How long a test send is followed for delivery events, and how often it is read. */
const FOLLOW_MS = 30_000;
const FOLLOW_INTERVAL_MS = 2_000;
/** Template key the samples use before the organization has a transactional template. */
const EXAMPLE_TEMPLATE_KEY = 'signup-code';
const LANGUAGES = ['curl', 'node', 'python'] as const;
type Language = (typeof LANGUAGES)[number];
const LANGUAGE_LABELS: Record<Language, string> = {
  curl: 'curl',
  node: 'Node.js',
  python: 'Python',
};
/** Events after which a message has nothing more to wait for. */
const SETTLED_EVENTS: ReadonlySet<string> = new Set([
  'delivered',
  'bounced',
  'complained',
  'rejected',
  'failed',
]);

/** True when a message has nothing more to wait for: refused, failed, or a final event. */
function isSettled(message: Pick<TransactionalLogEntry, 'status' | 'last_event'>): boolean {
  return (
    message.status === 'failed' ||
    message.status === 'suppressed' ||
    (message.last_event !== null && SETTLED_EVENTS.has(message.last_event))
  );
}

/** A key created on this page: its plaintext is held only until the user confirms saving it. */
interface CreatedKey {
  mode: DeveloperMode;
  prefix: string;
  last4: string;
  /** Dropped once the user confirms saving the key; it is never read again. */
  plaintext: string | null;
  /** The plaintext shows, in the field and the samples, only after "Show and copy". */
  revealed: boolean;
}

/** A key the quick start counts: the caller's own, in this mode, scoped to transactional mail only. */
function isQuickStartKey(apiKey: ApiKey, mode: DeveloperMode, now: number): boolean {
  return (
    apiKey.mode === mode &&
    apiKey.scopes.length === 1 &&
    apiKey.scopes[0] === TRANSACTIONAL_SCOPE &&
    !apiKey.revoked_at &&
    (!apiKey.expires_at || new Date(apiKey.expires_at).getTime() > now)
  );
}

/** A domain a transactional template may send from, as the template editor offers them. */
function senderDomain(domains: readonly MailboxDomain[]): string | null {
  return (
    domains.find(domain => domain.kind === 'organization' || domain.provider === 'log')?.domain ??
    null
  );
}

/**
 * The quick start: three setup steps, a key that only sends transactional mail (shown once), code
 * samples in plain HTTP, and a test send to the caller's own address that is followed for up to
 * 30 seconds.
 */
export function QuickStartTab({
  organization,
  mode,
  domains,
}: {
  organization: MailOrganization;
  mode: DeveloperMode;
  domains: readonly MailboxDomain[];
}) {
  const t = useT('developer');
  const apiKeys = useApiKeys();
  const templates = useMailTemplates(organization.id, 'transactional');
  const firstMessage = useTransactionalLog(organization.id, { mode }, { perPage: 1 });
  const [created, setCreated] = useState<CreatedKey | null>(null);
  const [chosenTemplateId, setChosenTemplateId] = useState<number | null>(null);
  const [sent, setSent] = useState<TransactionalMessage | null>(null);
  const [now] = useState(() => Date.now());

  const items = templates.data ?? [];
  const template = items.find(item => item.id === chosenTemplateId) ?? items[0] ?? null;
  const detail = useMailTemplate(organization.id, template?.id ?? null);

  const domain = senderDomain(domains);
  const ownKeys = (apiKeys.data?.items ?? []).filter(apiKey => isQuickStartKey(apiKey, mode, now));
  const createdHere = created?.mode === mode ? created : null;
  const sentHere = sent?.mode === mode ? sent : null;
  const hasMessage = (firstMessage.data?.pages[0]?.items.length ?? 0) > 0 || sentHere !== null;
  const modeName = t(`start.modeName.${mode}`);

  const variables = useMemo(() => {
    if (!template) return { code: '482913' };
    const samples = detail.data?.id === template.id ? detail.data.sample_variables : {};
    return Object.fromEntries(
      template.variable_definitions.map(definition => [
        definition.name,
        samples[definition.name] ?? `<${definition.name}>`,
      ])
    );
  }, [template, detail.data]);

  const snippets = buildSnippets({
    apiOrigin: env.NEXT_PUBLIC_DEVELOPER_API_ORIGIN,
    organizationId: organization.id,
    apiKey:
      createdHere?.plaintext && createdHere.revealed
        ? createdHere.plaintext
        : t('start.keyPlaceholder'),
    template: template?.key ?? EXAMPLE_TEMPLATE_KEY,
    variables,
  });

  const steps = [
    {
      title: t('start.domainTitle'),
      description: domain ? t('start.domainDone', { domain }) : t('start.domainTodo'),
      done: domain !== null,
    },
    {
      title: t('start.keyTitle'),
      description:
        ownKeys.length > 0 || createdHere
          ? t('start.keyDone', {
              mode: modeName,
              prefix: createdHere?.prefix ?? ownKeys[0]?.key_prefix ?? '',
            })
          : t('start.keyTodo', { mode: modeName }),
      done: ownKeys.length > 0 || createdHere !== null,
    },
    {
      title: t('start.sendTitle'),
      description: hasMessage ? t('start.sendDone') : t('start.sendTodo'),
      done: hasMessage,
    },
  ];

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,26rem)]">
      <div className="min-w-0 space-y-5">
        <ol aria-label={t('start.steps')} className="space-y-3">
          {steps.map((step, index) => (
            <li key={step.title} className="flex gap-3 rounded-md border p-4">
              <span
                aria-hidden="true"
                className={cn(
                  'flex size-6 flex-none items-center justify-center rounded-full text-xs font-semibold',
                  step.done ? 'bg-success-subtle text-success' : 'bg-muted text-muted-foreground'
                )}
              >
                {step.done ? <Check className="size-3.5" /> : index + 1}
              </span>
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium">{step.title}</p>
                <p className="mt-0.5 text-sm break-words text-muted-foreground">
                  {step.description}
                </p>
              </div>
              <span
                className={cn(
                  'flex-none text-xs',
                  step.done ? 'text-success' : 'text-muted-foreground'
                )}
              >
                {step.done ? t('start.done') : t('start.todo')}
              </span>
            </li>
          ))}
        </ol>
        <KeyPanel
          mode={mode}
          created={createdHere}
          existing={ownKeys[0] ?? null}
          onCreated={setCreated}
          onRevealed={() =>
            setCreated(current => (current ? { ...current, revealed: true } : null))
          }
          onSaved={() =>
            setCreated(current =>
              current ? { ...current, plaintext: null, revealed: false } : null
            )
          }
        />
      </div>
      <div className="min-w-0 space-y-4">
        {items.length > 1 ? (
          <div className="flex items-center gap-2">
            <Label htmlFor="quick-start-template" className="text-xs text-muted-foreground">
              {t('start.template')}
            </Label>
            <select
              id="quick-start-template"
              className={cn(nativeSelectClassName, 'flex-1')}
              value={template?.id ?? ''}
              onChange={event => setChosenTemplateId(Number(event.target.value))}
            >
              {items.map(item => (
                <option key={item.id} value={item.id}>
                  {item.name} · {item.key}
                </option>
              ))}
            </select>
          </div>
        ) : null}
        {templates.isSuccess && items.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t('start.noTemplate')}</p>
        ) : null}
        <CodeSamples snippets={snippets} />
        {env.NEXT_PUBLIC_DEVELOPER_API_ORIGIN ? null : (
          <p className="text-xs text-muted-foreground">{t('start.originHint')}</p>
        )}
        <TestSend
          organizationId={organization.id}
          mode={mode}
          templateKey={template?.key ?? null}
          sent={sentHere}
          onSent={setSent}
        />
      </div>
    </div>
  );
}

function KeyPanel({
  mode,
  created,
  existing,
  onCreated,
  onRevealed,
  onSaved,
}: {
  mode: DeveloperMode;
  created: CreatedKey | null;
  existing: ApiKey | null;
  onCreated: (key: CreatedKey) => void;
  onRevealed: () => void;
  onSaved: () => void;
}) {
  const t = useT('developer');
  const errorMessage = useDeveloperErrorMessage();
  const createKey = useCreateApiKey();
  const revealed = created?.revealed ?? false;
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');

  const create = () => {
    createKey.mutate(
      {
        name: t(mode === 'live' ? 'start.keyNameLive' : 'start.keyNameTest'),
        scopes: [TRANSACTIONAL_SCOPE],
        mode,
      },
      {
        onSuccess: result => {
          setCopyState('idle');
          onCreated({
            mode,
            prefix: result.api_key.key_prefix,
            last4: result.plaintext_key.slice(-4),
            plaintext: result.plaintext_key,
            revealed: false,
          });
        },
      }
    );
  };

  const revealAndCopy = async () => {
    if (!created?.plaintext) return;
    onRevealed();
    try {
      await navigator.clipboard.writeText(created.plaintext);
      setCopyState('copied');
    } catch {
      setCopyState('failed');
    }
  };

  const shown = created
    ? created.plaintext && revealed
      ? created.plaintext
      : `${created.prefix}••••${created.last4}`
    : existing
      ? `${existing.key_prefix}••••`
      : t('start.noKey');

  return (
    <section className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex h-10 min-w-0 flex-1 items-center gap-2 rounded-md border bg-muted/40 px-3">
          <span className="flex-none text-xs text-muted-foreground">{t('start.keyLabel')}</span>
          <code className="min-w-0 flex-1 truncate font-mono text-sm" data-testid="quick-start-key">
            {shown}
          </code>
          <span className="hidden flex-none rounded-full bg-primary/10 px-2 py-0.5 text-xs text-primary sm:inline">
            {t('start.keyScope')}
          </span>
        </div>
        {created?.plaintext ? (
          revealed ? (
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                // The mutation result holds the plaintext too; drop it with the page state.
                createKey.reset();
                onSaved();
              }}
            >
              {t('start.keySaved')}
            </Button>
          ) : (
            <Button
              type="button"
              variant="outline"
              icon={<Copy className="size-4" />}
              onClick={() => void revealAndCopy()}
            >
              {t('start.copy')}
            </Button>
          )
        ) : (
          <Button
            type="button"
            icon={<KeyRound className="size-4" />}
            loading={createKey.isPending}
            onClick={create}
          >
            {t('start.createKey', { mode: t(`start.modeName.${mode}`) })}
          </Button>
        )}
      </div>
      <p aria-live="polite" className="min-h-4 text-xs text-muted-foreground">
        {created?.plaintext
          ? copyState === 'copied'
            ? t('start.copied')
            : copyState === 'failed'
              ? t('start.copyFailed')
              : t('start.keyOnce')
          : ''}
      </p>
      {createKey.error ? (
        <p role="alert" className="text-sm text-error">
          {errorMessage(createKey.error)}
        </p>
      ) : null}
      <p className="text-xs text-muted-foreground">{t('start.keyScopeNote')}</p>
      <p className="rounded-md bg-warning-subtle px-3 py-2 text-xs leading-relaxed text-warning">
        {t('start.holderNote')}
      </p>
    </section>
  );
}

function CodeSamples({ snippets }: { snippets: Snippets }) {
  const t = useT('developer');
  const [language, setLanguage] = useState<Language>('curl');
  return (
    <section className="min-w-0 space-y-2">
      <div
        role="group"
        aria-label={t('start.language')}
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
      <pre
        aria-label={t('start.samples')}
        tabIndex={0}
        className="max-w-full overflow-x-auto rounded-md border bg-muted p-4 font-mono text-xs leading-relaxed whitespace-pre text-foreground focus-ring"
      >
        <code>{snippets[language]}</code>
      </pre>
    </section>
  );
}

function TestSend({
  organizationId,
  mode,
  templateKey,
  sent,
  onSent,
}: {
  organizationId: number;
  mode: DeveloperMode;
  templateKey: string | null;
  sent: TransactionalMessage | null;
  onSent: (message: TransactionalMessage) => void;
}) {
  const t = useT('developer');
  const errorMessage = useDeveloperErrorMessage();
  const testSend = useTransactionalTestSend(organizationId);
  const [expired, setExpired] = useState<number | null>(null);
  const following = sent !== null && sent.mode === 'live' && expired !== sent.id;
  const entry = useTransactionalLogEntry(organizationId, sent?.id ?? null, {
    // Read again until the message settles or 30 seconds pass, whichever comes first.
    refetchInterval: following
      ? data => (data && isSettled(data) ? false : FOLLOW_INTERVAL_MS)
      : false,
  });
  const current: TransactionalLogEntry | TransactionalMessage | null =
    entry.data?.id === sent?.id ? (entry.data ?? sent) : sent;
  const settled = current !== null && isSettled(current);

  useEffect(() => {
    if (!sent || sent.mode !== 'live') return;
    const id = sent.id;
    const timer = window.setTimeout(() => setExpired(id), FOLLOW_MS);
    return () => window.clearTimeout(timer);
  }, [sent]);

  const send = () => {
    if (!templateKey) return;
    testSend.mutate(
      { template: templateKey, mode },
      {
        onSuccess: message => {
          setExpired(null);
          onSent(message);
        },
      }
    );
  };

  let result: string;
  if (!sent || !current) {
    result = t('start.sendTestHint');
  } else if (sent.mode === 'test') {
    result = t('start.testModeResult');
  } else {
    const parts = [t('start.sentTo', { to: sent.to, status: t(`status.${logStatus(current)}`) })];
    const latest = current.last_event;
    if (latest !== null) parts.push(t('start.latestEvent', { event: t(`events.${latest}`) }));
    else if (!settled && expired === sent.id) parts.push(t('start.noEventYet'));
    else if (!settled) parts.push(t('start.waiting'));
    result = parts.join(' · ');
  }

  return (
    <section className="space-y-2">
      <div className="flex flex-wrap items-center gap-3">
        <Button
          type="button"
          icon={<Send className="size-4" />}
          loading={testSend.isPending}
          disabled={!templateKey}
          onClick={send}
        >
          {t('start.sendTest')}
        </Button>
        <p aria-live="polite" className="min-w-0 flex-1 text-xs break-words text-muted-foreground">
          {result}
        </p>
      </div>
      {testSend.error ? (
        <p role="alert" className="text-sm text-error">
          {errorMessage(testSend.error)}
        </p>
      ) : null}
    </section>
  );
}
