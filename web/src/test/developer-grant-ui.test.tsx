import { fireEvent, render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MemberFeatureToggles } from '@/features/organization/components/member-feature-toggles';
import type {
  OrganizationContext,
  OrganizationMember,
} from '@/features/organization/types';
import { messages } from '@/i18n/modules';
import zhHansOrganization from '@/i18n/modules/organization/zh-Hans';

const domains = vi.hoisted(() => ({
  data: undefined as unknown,
  error: null as unknown,
  isPending: false,
}));

vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxDomains: () => domains,
}));

const grants = {
  broadcasts: false,
  contacts: true,
  templates: true,
  api_keys: false,
  mail_clients: true,
  developer: false,
};

function mutation() {
  return {
    mutate: vi.fn(),
    isPending: false,
    variables: undefined,
  } as unknown as Parameters<typeof MemberFeatureToggles>[0]['mutation'];
}

const owner: OrganizationContext = {
  organization_id: 42,
  organization_name: 'Acme',
  organization_slug: 'acme',
  membership_id: 91,
  user_id: 17,
  role: 'owner',
};

function member(overrides: Partial<OrganizationMember> = {}): OrganizationMember {
  return {
    id: 92,
    user_id: 18,
    username: 'alex',
    nickname: 'Alex',
    role: 'member',
    features: grants,
    joined_at: '2026-07-15T10:00:00Z',
    updated_at: '2026-07-15T10:00:00Z',
    ...overrides,
  };
}

function renderToggles(
  props: Parameters<typeof MemberFeatureToggles>[0],
  locale: 'en-US' | 'zh-Hans' = 'zh-Hans'
) {
  const localized =
    locale === 'en-US' ? messages : { ...messages, organization: zhHansOrganization };
  return render(
    <NextIntlClientProvider locale={locale} messages={localized as unknown as AbstractIntlMessages}>
      <MemberFeatureToggles {...props} />
    </NextIntlClientProvider>
  );
}

function domainView(domain: string, kind: 'shared' | 'organization') {
  return { domain, kind };
}

describe('developer access grant', () => {
  beforeEach(() => {
    domains.data = [
      domainView('zmail.example', 'shared'),
      domainView('fast-ui.com', 'organization'),
      domainView('acme.dev', 'organization'),
    ];
    domains.error = null;
    domains.isPending = false;
  });

  it('opens a dialog that offers only verified domains and preselects the first', () => {
    const save = mutation();
    renderToggles({ context: owner, member: member(), mutation: save });

    fireEvent.click(screen.getByRole('button', { name: 'Alex 的开发者' }));

    expect(screen.getByRole('dialog', { name: 'Alex 的开发者授权' })).toBeInTheDocument();
    expect(screen.queryByText('zmail.example')).not.toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'fast-ui.com' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'acme.dev' })).not.toBeChecked();
    // Opening the dialog saves nothing.
    expect(save.mutate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '开通' }));
    expect(save.mutate).toHaveBeenCalledWith(
      { memberId: 92, input: { developer: true, developer_domains: ['fast-ui.com'] } },
      expect.any(Object)
    );
  });

  it('grants every verified domain', () => {
    const save = mutation();
    renderToggles({ context: owner, member: member(), mutation: save });
    fireEvent.click(screen.getByRole('button', { name: 'Alex 的开发者' }));

    fireEvent.click(screen.getByRole('checkbox', { name: '全部已验证域名' }));
    expect(screen.getByRole('checkbox', { name: 'acme.dev' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '开通' }));

    expect(save.mutate).toHaveBeenCalledWith(
      { memberId: 92, input: { developer: true, developer_all_domains: true } },
      expect.any(Object)
    );
  });

  it('cannot grant without a verified domain', () => {
    domains.data = [domainView('zmail.example', 'shared')];
    renderToggles({ context: owner, member: member(), mutation: mutation() });
    fireEvent.click(screen.getByRole('button', { name: 'Alex 的开发者' }));

    expect(screen.getByText(/组织还没有验证通过的自有域名/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '开通' })).toBeDisabled();
  });

  it('shows the scope on the chip and keeps API keys on while granted', () => {
    const save = mutation();
    renderToggles({
      context: owner,
      member: member({
        features: { ...grants, developer: true, api_keys: true },
        developer_scope: { all_domains: false, domains: ['fast-ui.com'] },
      }),
      mutation: save,
    });

    expect(screen.getByRole('button', { name: 'Alex 的开发者' })).toHaveTextContent(
      '开发者 · fast-ui.com'
    );
    const apiKeys = screen.getByRole('button', { name: 'Alex 的API 密钥' });
    expect(apiKeys).toBeDisabled();
    expect(apiKeys).toHaveAttribute('title', '已包含在开发者授权中');

    fireEvent.click(screen.getByRole('button', { name: 'Alex 的开发者' }));
    fireEvent.click(screen.getByRole('button', { name: '收回授权' }));
    expect(save.mutate).toHaveBeenCalledWith(
      { memberId: 92, input: { developer: false } },
      expect.any(Object)
    );
  });

  it('shows a member the scope read-only', () => {
    renderToggles({
      context: { ...owner, role: 'member' },
      member: member({
        features: { ...grants, developer: true, api_keys: true },
        developer_scope: { all_domains: true, domains: [] },
      }),
      mutation: mutation(),
    });

    expect(screen.getByText('开发者 · 全部域名')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
