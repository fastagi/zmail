'use client';

import { useSyncExternalStore } from 'react';
import { Monitor, Moon, Sun } from 'lucide-react';
import { useTheme } from 'next-themes';

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useT } from '@/i18n';

const supportedThemes = ['light', 'dark', 'system'] as const;
type SupportedTheme = (typeof supportedThemes)[number];

function isSupportedTheme(theme: string | undefined): theme is SupportedTheme {
  return supportedThemes.some(candidate => candidate === theme);
}

const subscribeToHydration = () => () => undefined;
const getClientHydrationSnapshot = () => true;
const getServerHydrationSnapshot = () => false;

/**
 * Theme toggle component that allows switching between light and dark modes
 */
export function ThemeToggle() {
  const { theme, setTheme } = useTheme();
  const t = useT('common');
  const hydrated = useSyncExternalStore(
    subscribeToHydration,
    getClientHydrationSnapshot,
    getServerHydrationSnapshot
  );
  const selectedTheme = hydrated && isSupportedTheme(theme) ? theme : 'system';
  const ThemeIcon = selectedTheme === 'light' ? Sun : selectedTheme === 'dark' ? Moon : Monitor;

  return (
    <Select value={selectedTheme} onValueChange={setTheme}>
      <SelectTrigger
        aria-label={t('toggleTheme')}
        title={t('toggleTheme')}
        data-performance-interaction="theme-selector"
        className="size-9 justify-center px-0 [&>svg:last-child]:hidden"
      >
        <ThemeIcon className="size-[1.2rem]" aria-hidden="true" />
        <span className="sr-only">
          <SelectValue />
        </span>
      </SelectTrigger>
      <SelectContent align="end">
        <SelectItem value="light">{t('themeLight')}</SelectItem>
        <SelectItem value="dark">{t('themeDark')}</SelectItem>
        <SelectItem value="system">{t('themeSystem')}</SelectItem>
      </SelectContent>
    </Select>
  );
}
