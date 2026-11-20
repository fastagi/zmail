import { CalendarDays } from 'lucide-react';
import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { isSelectableDay, todayUtc } from '@/features/mail-operations/types';

interface DayPickerProps {
  /** The selected UTC day, or undefined for today. */
  value: string | undefined;
  onChange: (date: string | undefined) => void;
}

/** Picks one UTC calendar day up to today; today is represented by an absent value. */
export function DayPicker({ value, onChange }: DayPickerProps) {
  const { t } = useTranslation();
  const id = useId();
  const today = todayUtc();
  return (
    <div className="flex flex-wrap items-end gap-2">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={id}>{t('mailOperations.day')}</Label>
        <Input
          id={id}
          type="date"
          max={today}
          className="w-44"
          value={value ?? today}
          onChange={(event) => {
            const next = event.target.value;
            if (isSelectableDay(next)) {
              onChange(next === today ? undefined : next);
            }
          }}
        />
      </div>
      <Button
        type="button"
        variant="outline"
        disabled={value === undefined || value === today}
        onClick={() => onChange(undefined)}
      >
        <CalendarDays aria-hidden="true" />
        {t('mailOperations.today')}
      </Button>
    </div>
  );
}
