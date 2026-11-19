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
  /** The current value of another query parameter of the console, such as a log filter. */
  param: (name: string) => string | null;
  /** Writes several parameters at once; null removes one. Tab and mode keep their values unless given. */
  update: (patch: Readonly<Record<string, string | null>>) => void;
} {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const tab = oneOf(DEVELOPER_TABS, searchParams.get('tab'), 'start');
  const mode = oneOf(DEVELOPER_MODES, searchParams.get('mode'), 'live');

  const update = (patch: Readonly<Record<string, string | null>>) => {
    const params = new URLSearchParams(searchParams.toString());
    params.set('tab', tab);
    params.set('mode', mode);
    for (const [name, value] of Object.entries(patch)) {
      if (value === null || value === '') params.delete(name);
      else params.set(name, value);
    }
    router.replace(`${pathname}?${params.toString()}`, { scroll: false });
  };

  return {
    tab,
    mode,
    setTab: nextTab => update({ tab: nextTab }),
    setMode: nextMode => update({ mode: nextMode }),
    param: name => searchParams.get(name),
    update,
  };
}
