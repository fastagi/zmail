import { fireEvent, render, screen, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { DeveloperWebhooksTab } from '@/features/developer/components/developer-webhooks-tab';
import { MockWebhookStore } from '@/features/webhook/server/mock-webhook-store';
import { messages } from '@/i18n/modules';

const EMAIL_EVENTS = [
  'email.sent',
  'email.delivered',
  'email.bounced',
  'email.complained',
  'email.suppressed',
  'email.failed',
] as const;

const state = vi.hoisted(() => {
  const query = () => ({
    data: undefined as unknown,
    error: null as unknown,
    isPending: false,
    isFetching: false,
    isError: false,
    refetch: vi.fn(),
  });
  const asyncMutation = () => ({
    error: null as unknown,
    isPending: false,
    mutateAsync: vi.fn(),
    reset: vi.fn(),
  });
  const mutation = () => ({ isPending: false, mutate: vi.fn() });
  return {
    endpoints: query(),
    deliveries: query(),
    attempts: query(),
    eventTypes: query(),
    create: asyncMutation(),
    update: asyncMutation(),
    remove: asyncMutation(),
    rotate: asyncMutation(),
    status: mutation(),
    test: mutation(),
  };
});

vi.mock('@/features/webhook/hooks/use-webhooks', () => ({
  useWebhookEndpoints: () => state.endpoints,
  useWebhookDeliveries: () => state.deliveries,
  useWebhookAttempts: () => state.attempts,
  useWebhookEventTypes: () => state.eventTypes,
  useCreateWebhookEndpoint: () => state.create,
  useUpdateWebhookEndpoint: () => state.update,
  useDeleteWebhookEndpoint: () => state.remove,
  useRotateWebhookEndpointSecret: () => state.rotate,
  useReplaceWebhookEndpointStatus: () => state.status,
  useTestWebhookEndpoint: () => state.test,
}));

describe('developer webhooks tab', () => {
  beforeEach(() => {
    state.endpoints.data = page([]);
    state.deliveries.data = page([]);
    state.attempts.data = page([]);
    state.eventTypes.data = ['mail.message.sent', 'webhook.test', ...EMAIL_EVENTS];
    state.eventTypes.isPending = false;
    state.create.mutateAsync.mockReset();
  });

  it('keeps Create disabled while the event types load', () => {
    state.eventTypes.data = undefined;
    state.eventTypes.isPending = true;
    renderWithMessages(<DeveloperWebhooksTab organizationId={1} />);
    expect(screen.getByRole('button', { name: 'Create endpoint' })).toBeDisabled();
  });

  it('webhooks tab offers only email events', async () => {
    state.create.mutateAsync.mockResolvedValue({
      endpoint: { id: 1 },
      signing_secret: `whsec_${'A'.repeat(43)}=`,
      previous_secret_expiry: null,
    });
    renderWithMessages(<DeveloperWebhooksTab organizationId={1} />);

    fireEvent.click(screen.getByRole('button', { name: 'Create endpoint' }));
    const dialog = screen.getByRole('dialog');
    const offered = within(dialog)
      .getAllByRole('checkbox')
      .map(box => box.id.replace('webhook-event-', ''));
    expect(offered).toEqual([...EMAIL_EVENTS]);
    expect(within(dialog).queryByText('webhook.test')).not.toBeInTheDocument();
    expect(within(dialog).queryByText('mail.message.sent')).not.toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'App' } });
    fireEvent.change(within(dialog).getByLabelText('Target URL'), {
      target: { value: 'https://api.example.com/hooks/zmail' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create endpoint' }));
    await vi.waitFor(() =>
      expect(state.create.mutateAsync).toHaveBeenCalledWith({
        name: 'App',
        url: 'https://api.example.com/hooks/zmail',
        event_types: [...EMAIL_EVENTS],
      })
    );
  });

  it('describes every email event', () => {
    renderWithMessages(<DeveloperWebhooksTab organizationId={1} />);
    const list = screen.getByRole('list', { name: 'Events' });
    for (const eventType of EMAIL_EVENTS) {
      expect(within(list).getByText(eventType)).toBeInTheDocument();
    }
    expect(within(list).getAllByRole('listitem')).toHaveLength(EMAIL_EVENTS.length);
  });

  it('sample payload has the contract fields and no code', () => {
    renderWithMessages(<DeveloperWebhooksTab organizationId={1} />);
    const sample = screen.getByTestId('developer-webhook-sample');
    const payload = JSON.parse(sample.textContent ?? '') as {
      type: string;
      data: Record<string, unknown>;
    };

    expect(payload.type).toBe('email.bounced');
    expect(Object.keys(payload.data).sort()).toEqual(
      ['bounce_type', 'message_id', 'mode', 'reason', 'simulated', 'template', 'to'].sort()
    );
    expect(payload.data.to).toBe('lilei@example.com');
    expect(payload.data.bounce_type).toMatch(/^(permanent|temporary)$/u);
    expect(typeof payload.data.simulated).toBe('boolean');
    expect(sample.textContent).not.toMatch(/"code"|subject|body|••/u);
  });

  it('describes the contract retry schedule, not the prototype one', () => {
    renderWithMessages(<DeveloperWebhooksTab organizationId={1} />);
    const note = screen.getByText(/retried after about 1 minute, 5 minutes, 15 minutes, 1 hour/u);
    expect(note.textContent).toMatch(/up to 24 hours apart, over about three days/u);
    expect(note.textContent).toMatch(/only an operator can replay it/u);
    expect(note.textContent).not.toMatch(/30 minutes|delivery list/u);
  });
});

describe('mock webhook event catalog', () => {
  it('lists the six transactional email events', () => {
    const eventTypes = new MockWebhookStore().eventTypes();
    expect(eventTypes).toContain('webhook.test');
    expect(eventTypes).toContain('mail.message.sent');
    for (const eventType of EMAIL_EVENTS) expect(eventTypes).toContain(eventType);
  });
});

function page<T>(items: T[]) {
  return {
    items,
    meta: { current_page: 1, per_page: 25, total: items.length, last_page: 1, from: 0, to: 0 },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function renderWithMessages(children: React.ReactNode) {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}
