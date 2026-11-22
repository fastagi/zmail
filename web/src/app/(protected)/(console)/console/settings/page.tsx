import { lazy, Suspense } from 'react';
import { KeyRound, ShieldCheck, SlidersHorizontal, UserRound } from 'lucide-react';

import { Skeleton } from '@/components/ui/skeleton';
import { TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { isWebFeatureEnabled } from '@/config/features';
import { ApiKeyPanel } from '@/features/api-key';
import { getT } from '@/i18n/server';

import { SettingsTabs } from './settings-tabs';

const AccountSecurityPanel = lazy(async () => {
  const feature = await import('@/features/auth/components/account-security-panel');
  return { default: feature.AccountSecurityPanel };
});

const ProfilePanel = lazy(async () => {
  const feature = await import('@/features/auth/components/profile-panel');
  return { default: feature.ProfilePanel };
});

const UserSettingPanel = lazy(async () => {
  const feature = await import('@/features/setting/components/user-setting-panel');
  return { default: feature.UserSettingPanel };
});

const settingsTabs = ['profile', 'preferences', 'security', 'api'] as const;

/**
 * Replaceable settings surface for the starter console. `?tab=` opens one tab directly.
 */
export default async function SettingsPage() {
  const t = await getT('settings');
  const settingEnabled = isWebFeatureEnabled('setting');
  const tabs = settingsTabs.filter(value => value !== 'preferences' || settingEnabled);

  return (
    <div className="mx-auto w-full max-w-5xl flex-1 px-4 py-6 sm:px-6 lg:py-8">
      <h1 className="text-2xl font-semibold">{t('title')}</h1>

      <SettingsTabs tabs={tabs} className="mt-5">
        <TabsList className="max-w-full overflow-x-auto">
          <TabsTrigger value="profile">
            <UserRound aria-hidden="true" />
            {t('tabs.profile')}
          </TabsTrigger>
          {settingEnabled ? (
            <TabsTrigger value="preferences">
              <SlidersHorizontal aria-hidden="true" />
              {t('tabs.preferences')}
            </TabsTrigger>
          ) : null}
          <TabsTrigger value="security">
            <ShieldCheck aria-hidden="true" />
            {t('tabs.security')}
          </TabsTrigger>
          <TabsTrigger value="api">
            <KeyRound aria-hidden="true" />
            {t('tabs.api')}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="profile" className="pt-5">
          <Suspense fallback={<UserSettingPanelFallback />}>
            <ProfilePanel />
          </Suspense>
        </TabsContent>

        {settingEnabled ? (
          <TabsContent value="preferences" className="pt-5">
            <Suspense fallback={<UserSettingPanelFallback />}>
              <UserSettingPanel />
            </Suspense>
          </TabsContent>
        ) : null}

        <TabsContent value="security" className="pt-5">
          <Suspense fallback={<UserSettingPanelFallback />}>
            <AccountSecurityPanel />
          </Suspense>
        </TabsContent>

        <TabsContent value="api" className="pt-5">
          <ApiKeyPanel />
        </TabsContent>
      </SettingsTabs>
    </div>
  );
}

function UserSettingPanelFallback() {
  return (
    <div className="max-w-2xl space-y-5 py-2" aria-hidden="true">
      <Skeleton className="h-14 w-full" />
      <Skeleton className="h-20 w-full" />
      <Skeleton className="h-20 w-full" />
    </div>
  );
}
