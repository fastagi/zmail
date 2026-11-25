'use client';

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useLocale } from '@/hooks/use-locale';
import { locales, localeNames } from '@/i18n/locales';

export function SiteLanguageSelect({ label }: { label: string }) {
  const { locale, setLocale, isPending } = useLocale();
  return (
    <Select
      value={locale}
      disabled={isPending}
      onValueChange={next => {
        const selected = locales.find(value => value === next);
        if (selected) setLocale(selected);
      }}
    >
      <SelectTrigger
        size="sm"
        aria-label={label}
        className="border-border/60 bg-bg-surface text-sm"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent align="end">
        {locales.map(value => (
          <SelectItem key={value} value={value}>
            {localeNames[value]}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
