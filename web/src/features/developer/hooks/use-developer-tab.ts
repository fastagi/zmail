'use client';

import { usePathname, useRouter, useSearchParams } from 'next/navigation';

import {
  DEVELOPER_MODES,
  DEVELOPER_TABS,
  type DeveloperMode,
  type DeveloperTab,
} from '@/features/developer/types';

function oneOf<T extends string>(allowed: readonly T[], value: string | null, fallback: T): T {
  return allowed.find(item => item === value) ?? fallback;
}

/**
 * The console's tab and key mode, kept in `?tab=` and `?mode=` so a reload or a shared link shows
 * the same view. Unknown values fall back to the quick start in live mode; other query
 * parameters, such as the organization, are kept.
 */
export function useDeveloperTab(): {
  tab: DeveloperTab;
  mode: DeveloperMode;
  setTab: (tab: DeveloperTab) => void;
  setMode: (mode: DeveloperMode) => void;
} {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const tab = oneOf(DEVELOPER_TABS, searchParams.get('tab'), 'start');
  const mode = oneOf(DEVELOPER_MODES, searchParams.get('mode'), 'live');

  const write = (next: { tab: DeveloperTab; mode: DeveloperMode }) => {
    const params = new URLSearchParams(searchParams.toString());
    params.set('tab', next.tab);
    params.set('mode', next.mode);
    router.replace(`${pathname}?${params.toString()}`, { scroll: false });
  };

  return {
    tab,
    mode,
    setTab: nextTab => write({ tab: nextTab, mode }),
    setMode: nextMode => write({ tab, mode: nextMode }),
  };
}
