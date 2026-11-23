import { render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { describe, expect, it, vi } from 'vitest';

import { DeveloperAccessRequired } from '@/features/developer';
import { MemberFeatureGate } from '@/features/mailbox/components/member-feature-gate';
import { messages } from '@/i18n/modules';
import zhHansDeveloper from '@/i18n/modules/developer/zh-Hans';

const grants = vi.hoisted(() => ({ features: undefined as Record<string, boolean> | undefined }));
vi.mock('@/features/mailbox/hooks/use-mail-organization', () => ({
  useChosenMemberFeatures: () => grants.features,
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => ({ isError: false, data: { items: [{ id: 12 }] } }),
}));

const memberGrants = {
  broadcasts: false,
  contacts: true,
  templates: true,
  api_keys: true,
  mail_clients: true,
  developer: false,
};

function renderPage() {
  return render(
    <NextIntlClientProvider
      locale="zh-Hans"
      messages={{ ...messages, developer: zhHansDeveloper } as unknown as AbstractIntlMessages}
    >
      <MemberFeatureGate feature="developer" fallback={<DeveloperAccessRequired />}>
        <p>Developer console</p>
      </MemberFeatureGate>
    </NextIntlClientProvider>
  );
}

describe('developer console access', () => {
  it('explains how to get developer access instead of failing on every request', () => {
    // API keys alone no longer open the developer console.
    grants.features = memberGrants;
    renderPage();

    expect(screen.getByRole('status')).toHaveTextContent('需要管理员开通开发者授权');
    expect(screen.getByText(/请联系管理员/)).toBeInTheDocument();
    expect(screen.queryByText('Developer console')).not.toBeInTheDocument();
  });

  it('opens for a member with developer access', () => {
    grants.features = { ...memberGrants, developer: true };
    renderPage();

    expect(screen.getByText('Developer console')).toBeInTheDocument();
  });
});
