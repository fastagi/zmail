'use client';

import { useState } from 'react';
import { useLocale } from 'next-intl';
import { CalendarClock, Clock, RotateCcw } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import {
  formatPresetPreview,
  laterTodayPreset,
  nextMondayAtPreset,
  parseCustomFutureIso,
  toDateTimeLocalValue,
  tomorrowAtPreset,
} from '@/features/mailbox/components/schedule-snooze-presets';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const MIN_SNOOZE_LEAD_MS = 60_000;

/**
 * Gmail-style Clock popover inside an inbox row's quick-action cluster. Lets the user snooze a
 * message/thread to a preset or custom time, or clear an active snooze when in the Snoozed view.
 */
export function SnoozeMenu({
  label,
  tooltip,
  timeZone,
  isSnoozed = false,
  onOpenChange,
  onSnooze,
  onUnsnooze,
}: {
  label: string;
  tooltip: string;
  timeZone?: string;
  isSnoozed?: boolean;
  onOpenChange?: (open: boolean) => void;
  onSnooze: (snoozedUntilIso: string) => void;
  onUnsnooze?: () => void;
}) {
  const t = useT('mailbox');
  const locale = useLocale();
  const [open, setOpen] = useState(false);
  const [openedAt, setOpenedAt] = useState<Date>(() => new Date());
  const [showCustom, setShowCustom] = useState(false);
  const [customValue, setCustomValue] = useState('');
  const [customError, setCustomError] = useState(false);

  const presets = [
    {
      key: 'laterToday',
      label: t('inbox.snooze.laterToday'),
      date: laterTodayPreset(openedAt),
    },
    {
      key: 'tomorrowMorning',
      label: t('inbox.snooze.tomorrowMorning'),
      date: tomorrowAtPreset(9, openedAt),
    },
    {
      key: 'mondayMorning',
      label: t('inbox.snooze.mondayMorning'),
      date: nextMondayAtPreset(9, openedAt),
    },
  ] as const;

  const setMenuOpen = (next: boolean) => {
    setOpen(next);
    onOpenChange?.(next);
    if (next) {
      setOpenedAt(new Date());
    } else {
      setShowCustom(false);
      setCustomError(false);
    }
  };

  const openCustomPicker = () => {
    setShowCustom(true);
    setCustomError(false);
    setCustomValue(current => current || toDateTimeLocalValue(tomorrowAtPreset(9)));
  };

  const pickPreset = (date: Date) => {
    setMenuOpen(false);
    onSnooze(date.toISOString());
  };

  const confirmCustom = () => {
    const iso = parseCustomFutureIso(customValue, MIN_SNOOZE_LEAD_MS);
    if (!iso) {
      setCustomError(true);
      return;
    }
    setMenuOpen(false);
    onSnooze(iso);
  };

  const handleUnsnooze = () => {
    setMenuOpen(false);
    onUnsnooze?.();
  };

  return (
    <Popover open={open} onOpenChange={setMenuOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          isIcon
          size="xs"
          variant="ghost"
          aria-label={label}
          title={tooltip}
          className={cn(
            'size-6 text-text-muted hover:bg-row-hover hover:text-text-main',
            isSnoozed && 'text-amber-600 hover:text-amber-700 dark:text-amber-400'
          )}
          onClick={event => event.stopPropagation()}
        >
          <Clock className="size-3.5" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        side="bottom"
        onClick={event => event.stopPropagation()}
        className="w-72 rounded-xl border-border bg-popover p-1.5 shadow-lg"
      >
        <div className="flex items-center gap-2 px-2.5 py-1.5 text-xs font-medium text-muted-foreground">
          <Clock className="size-3.5" aria-hidden="true" />
          <span>{t('inbox.snooze.title')}</span>
        </div>
        {isSnoozed && onUnsnooze ? (
          <>
            <button
              type="button"
              onClick={handleUnsnooze}
              className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs font-medium text-foreground transition-colors hover:bg-accent hover:text-accent-foreground focus-ring"
            >
              <RotateCcw className="size-3.5 text-muted-foreground" aria-hidden="true" />
              <span>{t('inbox.snooze.unsnooze')}</span>
            </button>
            <div className="my-1 border-t border-border/60" />
          </>
        ) : null}
        <div className="flex flex-col gap-0.5">
          {presets.map(preset => (
            <button
              key={preset.key}
              type="button"
              onClick={() => pickPreset(preset.date)}
              className="flex w-full items-center justify-between gap-2 rounded-lg px-2.5 py-2 text-left text-xs transition-colors hover:bg-accent hover:text-accent-foreground focus-ring"
            >
              <span className="font-medium text-foreground">{preset.label}</span>
              <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
                {formatPresetPreview(preset.date, locale, timeZone)}
              </span>
            </button>
          ))}
        </div>
        <div className="my-1 border-t border-border/60" />
        {showCustom ? (
          <div className="space-y-2 px-2 py-1.5">
            <input
              type="datetime-local"
              aria-label={t('inbox.snooze.customInput')}
              value={customValue}
              min={toDateTimeLocalValue(new Date(openedAt.getTime() + MIN_SNOOZE_LEAD_MS))}
              onChange={event => {
                setCustomValue(event.target.value);
                setCustomError(false);
              }}
              className="h-8 w-full rounded-md border border-input bg-background px-2 text-xs text-foreground focus-ring"
            />
            {customError ? (
              <p role="alert" className="text-[11px] text-destructive">
                {t('inbox.snooze.customInvalid')}
              </p>
            ) : null}
            <div className="flex justify-end gap-1.5">
              <Button
                type="button"
                size="xs"
                variant="ghost"
                onClick={() => {
                  setShowCustom(false);
                  setCustomError(false);
                }}
              >
                {t('actions.cancel')}
              </Button>
              <Button type="button" size="xs" onClick={confirmCustom}>
                {t('inbox.snooze.confirm')}
              </Button>
            </div>
          </div>
        ) : (
          <button
            type="button"
            onClick={openCustomPicker}
            className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs font-medium text-foreground transition-colors hover:bg-accent hover:text-accent-foreground focus-ring"
          >
            <CalendarClock className="size-3.5 text-muted-foreground" aria-hidden="true" />
            <span>{t('inbox.snooze.customToggle')}</span>
          </button>
        )}
      </PopoverContent>
    </Popover>
  );
}
