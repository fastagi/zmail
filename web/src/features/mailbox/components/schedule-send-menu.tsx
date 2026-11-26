'use client';

import { useState } from 'react';
import { useLocale } from 'next-intl';
import { CalendarClock, ChevronDown, Clock } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import {
  formatPresetPreview,
  MIN_SCHEDULE_LEAD_MS,
  nextMondayAtPreset,
  parseCustomFutureIso,
  toDateTimeLocalValue,
  tomorrowAtPreset,
} from '@/features/mailbox/components/schedule-snooze-presets';
import { useT } from '@/i18n';

/**
 * Gmail-style split chevron popover beside the Compose Send button. Offers quick schedule presets
 * plus a custom `datetime-local` picker and hands back an ISO-8601 UTC timestamp on confirmation.
 */
export function ScheduleSendMenu({
  disabled = false,
  onSchedule,
}: {
  disabled?: boolean;
  onSchedule: (scheduledAtIso: string) => void;
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
      key: 'tomorrowMorning',
      label: t('compose.scheduleSend.tomorrowMorning'),
      date: tomorrowAtPreset(9, openedAt),
    },
    {
      key: 'tomorrowAfternoon',
      label: t('compose.scheduleSend.tomorrowAfternoon'),
      date: tomorrowAtPreset(13, openedAt),
    },
    {
      key: 'mondayMorning',
      label: t('compose.scheduleSend.mondayMorning'),
      date: nextMondayAtPreset(9, openedAt),
    },
  ] as const;

  const handleOpenChange = (next: boolean) => {
    setOpen(next);
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
    setOpen(false);
    setShowCustom(false);
    setCustomError(false);
    onSchedule(date.toISOString());
  };

  const confirmCustom = () => {
    const iso = parseCustomFutureIso(customValue, MIN_SCHEDULE_LEAD_MS);
    if (!iso) {
      setCustomError(true);
      return;
    }
    setOpen(false);
    setShowCustom(false);
    setCustomError(false);
    onSchedule(iso);
  };

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="default"
          disabled={disabled}
          aria-label={t('compose.scheduleSend.trigger')}
          title={t('compose.scheduleSend.title')}
          className="rounded-l-none border-l border-primary-foreground/25 px-2"
        >
          <ChevronDown className="size-4" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        side="top"
        className="w-72 rounded-xl border-border bg-popover p-1.5 shadow-lg"
      >
        <div className="flex items-center gap-2 px-2.5 py-1.5 text-xs font-medium text-muted-foreground">
          <Clock className="size-3.5" aria-hidden="true" />
          <span>{t('compose.scheduleSend.title')}</span>
        </div>
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
                {formatPresetPreview(preset.date, locale)}
              </span>
            </button>
          ))}
        </div>
        <div className="my-1 border-t border-border/60" />
        {showCustom ? (
          <div className="space-y-2 px-2 py-1.5">
            <input
              type="datetime-local"
              aria-label={t('compose.scheduleSend.customInput')}
              value={customValue}
              min={toDateTimeLocalValue(new Date(openedAt.getTime() + MIN_SCHEDULE_LEAD_MS))}
              onChange={event => {
                setCustomValue(event.target.value);
                setCustomError(false);
              }}
              className="h-8 w-full rounded-md border border-input bg-background px-2 text-xs text-foreground focus-ring"
            />
            {customError ? (
              <p role="alert" className="text-[11px] text-destructive">
                {t('compose.scheduleSend.customInvalid')}
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
                {t('compose.scheduleSend.confirm')}
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
            <span>{t('compose.scheduleSend.customToggle')}</span>
          </button>
        )}
      </PopoverContent>
    </Popover>
  );
}
