'use client';

import { WebhookManagement } from '@/features/webhook/components/webhook-management';
import { useT } from '@/i18n';

const TRANSACTIONAL_EVENT_PREFIX = 'email.';

const TRANSACTIONAL_EVENTS = [
  'sent',
  'delivered',
  'bounced',
  'complained',
  'suppressed',
  'failed',
] as const;

// A delivery as contracts/WEBHOOKS.md frames it, carrying the transactional event `data` of
// contracts/MAILBOXES.md "Transactional Mail Events". It holds no subject, body, or variable.
const SAMPLE_PAYLOAD = JSON.stringify(
  {
    id: 'msg_01JD3Q7Z8K4M2N6P9R5T0V1W2X',
    type: 'email.bounced',
    timestamp: '2026-11-19T14:22:08Z',
    data: {
      message_id: 1204,
      template: 'signup-code',
      to: 'lilei@example.com',
      mode: 'live',
      simulated: false,
      bounce_type: 'permanent',
      reason: '5.1.1 permanent_failure',
    },
  },
  null,
  2
);

/** The developer console's webhook tab: endpoints that subscribe to transactional mail events. */
export function DeveloperWebhooksTab({ organizationId }: { organizationId: number }) {
  const t = useT('developer');

  return (
    <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_380px]">
      <div className="min-w-0">
        <WebhookManagement
          organizationId={organizationId}
          eventTypePrefix={TRANSACTIONAL_EVENT_PREFIX}
        />
      </div>
      <aside className="min-w-0 space-y-6 py-6">
        <section aria-labelledby="developer-webhook-events">
          <h3 id="developer-webhook-events" className="text-sm font-semibold">
            {t('webhooks.eventsTitle')}
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">{t('webhooks.eventsDescription')}</p>
          <ul aria-labelledby="developer-webhook-events" className="mt-3 divide-y border-y">
            {TRANSACTIONAL_EVENTS.map(event => (
              <li key={event} className="py-2">
                <span className="font-mono text-xs">{`${TRANSACTIONAL_EVENT_PREFIX}${event}`}</span>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {t(`webhooks.events.${event}`)}
                </p>
              </li>
            ))}
          </ul>
        </section>
        <section aria-labelledby="developer-webhook-sample">
          <h3 id="developer-webhook-sample" className="text-sm font-semibold">
            {t('webhooks.sampleTitle')}
          </h3>
          <pre
            data-testid="developer-webhook-sample"
            className="mt-2 overflow-x-auto rounded-lg border bg-muted p-4 font-mono text-xs leading-relaxed"
          >
            {SAMPLE_PAYLOAD}
          </pre>
          <p className="mt-2 text-xs text-muted-foreground">{t('webhooks.sampleNote')}</p>
          <p className="mt-2 text-xs text-muted-foreground">{t('webhooks.retryNote')}</p>
        </section>
      </aside>
    </div>
  );
}
