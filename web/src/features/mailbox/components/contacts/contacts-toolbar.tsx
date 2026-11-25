'use client';

import { useId } from 'react';
import { Plus, Search, Upload, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import type { MailContactStatus } from '@/features/mailbox/types';
import { useT } from '@/i18n';

import { statuses, statusKey } from './contact-shared';

const ALL_STATUSES_VALUE = '__all__';

export function ContactsToolbar({
  query,
  status,
  showClearFilters,
  onQuery,
  onStatus,
  onClearFilters,
  onAdd,
  onImport,
}: {
  query: string;
  status: MailContactStatus | undefined;
  showClearFilters: boolean;
  onQuery: (value: string) => void;
  onStatus: (value: MailContactStatus | undefined) => void;
  onClearFilters: () => void;
  onAdd: () => void;
  onImport: () => void;
}) {
  const t = useT('mailbox');
  const searchId = useId();
  const statusId = useId();
  return (
    <div className="flex flex-wrap items-end gap-3">
      <div className="relative min-w-48 flex-1">
        <Label htmlFor={searchId} className="sr-only">
          {t('contacts.search')}
        </Label>
        <Search
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          id={searchId}
          type="search"
          value={query}
          className="pl-9"
          placeholder={t('contacts.searchPlaceholder')}
          onChange={event => onQuery(event.target.value)}
          onKeyDown={event => {
            if (event.key === 'Escape' && query !== '') {
              event.preventDefault();
              onQuery('');
            }
          }}
        />
      </div>
      <div>
        <Label htmlFor={statusId} className="sr-only">
          {t('contacts.statusFilter')}
        </Label>
        <Select
          value={status ?? ALL_STATUSES_VALUE}
          onValueChange={value =>
            onStatus(value === ALL_STATUSES_VALUE ? undefined : (value as MailContactStatus))
          }
        >
          <SelectTrigger id={statusId} className="min-w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_STATUSES_VALUE}>{t('contacts.allStatuses')}</SelectItem>
            {statuses.map(value => (
              <SelectItem key={value} value={value}>
                {t(statusKey[value])}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {showClearFilters ? (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          icon={<X className="size-3.5" aria-hidden="true" />}
          onClick={onClearFilters}
        >
          {t('emails.clearFilters')}
        </Button>
      ) : null}
      <Button
        type="button"
        variant="outline"
        icon={<Upload className="size-4" aria-hidden="true" />}
        onClick={onImport}
      >
        {t('contacts.import')}
      </Button>
      <Button type="button" icon={<Plus className="size-4" aria-hidden="true" />} onClick={onAdd}>
        {t('contacts.add')}
      </Button>
    </div>
  );
}
