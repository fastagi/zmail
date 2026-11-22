import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { SettingsTabs } from '@/app/(protected)/(console)/console/settings/settings-tabs';

let search = '';
vi.mock('next/navigation', () => ({
  useSearchParams: () => new URLSearchParams(search),
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
});
