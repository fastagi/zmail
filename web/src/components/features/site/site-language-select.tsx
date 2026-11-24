'use client';

import { useLocale } from '@/hooks/use-locale';
import { locales, localeNames } from '@/i18n/locales';

export function SiteLanguageSelect({ label }: { label: string }) {
  const { locale, setLocale, isPending } = useLocale();
  return (
    <select
      aria-label={label}
      className="rounded-md bg-bg-surface p-1 text-sm focus-ring"
      value={locale}
      disabled={isPending}
      onChange={event => {
        const selected = locales.find(value => value === event.target.value);
        if (selected) setLocale(selected);
      }}
    >
      {locales.map(value => (
        <option key={value} value={value}>
          {localeNames[value]}
        </option>
      ))}
    </select>
  );
}
