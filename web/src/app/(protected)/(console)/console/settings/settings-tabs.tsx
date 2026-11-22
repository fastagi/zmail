'use client';

import type { ReactNode } from 'react';
import { useSearchParams } from 'next/navigation';

import { Tabs } from '@/components/ui/tabs';

interface SettingsTabsProps {
  tabs: readonly string[];
  children: ReactNode;
  className?: string;
}

/**
 * Settings tabs that follow `?tab=`: choosing a tab writes it to the address, and a link to another
 * `?tab=` selects that tab even when this page is already open.
 */
export function SettingsTabs({ tabs, children, className }: SettingsTabsProps) {
  const requested = useSearchParams().get('tab');
  const value = tabs.find(tab => tab === requested) ?? tabs[0];

  return (
    <Tabs
      value={value}
      onValueChange={next => {
        const params = new URLSearchParams(window.location.search);
        params.set('tab', next);
        window.history.replaceState(null, '', `?${params.toString()}`);
      }}
      className={className}
    >
      {children}
    </Tabs>
  );
}
