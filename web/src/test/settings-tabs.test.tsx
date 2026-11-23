import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';

import { TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { SettingsTabs } from '@/app/(protected)/(console)/console/settings/settings-tabs';
import { MemberFeatureGate } from '@/features/mailbox/components/member-feature-gate';
import { messages } from '@/i18n/modules';

let search = '';
vi.mock('next/navigation', () => ({
  useSearchParams: () => new URLSearchParams(search),
}));

const grants = vi.hoisted(() => ({ features: undefined as Record<string, boolean> | undefined }));
vi.mock('@/features/mailbox/hooks/use-mail-organization', () => ({
  useChosenMemberFeatures: () => grants.features,
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => ({ isError: false, data: { items: [{ id: 12 }] } }),
}));

function renderTabs() {
  return render(
    <SettingsTabs tabs={['profile', 'security']}>
      <TabsList>
        <TabsTrigger value="profile">Profile</TabsTrigger>
        <TabsTrigger value="security">Security</TabsTrigger>
      </TabsList>
      <TabsContent value="profile">Profile panel</TabsContent>
      <TabsContent value="security">Security panel</TabsContent>
    </SettingsTabs>
  );
}

describe('SettingsTabs', () => {
  afterEach(() => {
    search = '';
    window.history.replaceState(null, '', '/');
  });

  it('opens the first tab without a valid ?tab=', () => {
    search = 'tab=unknown';
    renderTabs();
    expect(screen.getByText('Profile panel')).toBeInTheDocument();
  });

  it('writes the chosen tab to the address', () => {
    const view = renderTabs();
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Security' }), { button: 0 });
    expect(window.location.search).toBe('?tab=security');
    search = 'tab=security';
    view.rerender(<></>);
    renderTabs();
    expect(screen.getByText('Security panel')).toBeInTheDocument();
  });

  it('follows a ?tab= link while the page is open', () => {
    search = 'tab=security';
    const view = renderTabs();
    expect(screen.getByText('Security panel')).toBeInTheDocument();
    search = 'tab=profile';
    act(() => {
      view.rerender(
        <SettingsTabs tabs={['profile', 'security']}>
          <TabsList>
            <TabsTrigger value="profile">Profile</TabsTrigger>
            <TabsTrigger value="security">Security</TabsTrigger>
          </TabsList>
          <TabsContent value="profile">Profile panel</TabsContent>
          <TabsContent value="security">Security panel</TabsContent>
        </SettingsTabs>
      );
    });
    expect(screen.getByText('Profile panel')).toBeInTheDocument();
  });

  it('hides the tab without the mail_clients grant', () => {
    const features = {
      broadcasts: true,
      contacts: true,
      templates: true,
      api_keys: true,
      mail_clients: false,
    };
    const renderGated = () =>
      render(
        <NextIntlClientProvider
          locale="en-US"
          messages={messages as unknown as AbstractIntlMessages}
        >
          <SettingsTabs tabs={['profile', 'mail-clients']}>
            <TabsList>
              <TabsTrigger value="profile">Profile</TabsTrigger>
              <MemberFeatureGate feature="mail_clients" fallback={null}>
                <TabsTrigger value="mail-clients">Mail apps</TabsTrigger>
              </MemberFeatureGate>
            </TabsList>
            <TabsContent value="profile">Profile panel</TabsContent>
          </SettingsTabs>
        </NextIntlClientProvider>
      );

    grants.features = features;
    const view = renderGated();
    expect(screen.queryByRole('tab', { name: 'Mail apps' })).not.toBeInTheDocument();
    view.unmount();

    grants.features = { ...features, mail_clients: true };
    renderGated();
    expect(screen.getByRole('tab', { name: 'Mail apps' })).toBeInTheDocument();
  });
});
