'use client';

import {
  useState,
  type FocusEvent,
  type FormEvent,
  type KeyboardEvent,
  type ReactNode,
  type Ref,
} from 'react';
import { Calendar, Mail, Paperclip, Search, SlidersHorizontal } from 'lucide-react';

import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { INBOX_DATE_RANGES, MAIL_SEARCH_MAX_LENGTH } from '@/features/mailbox/schemas';
import {
  parseSearchOperators,
  type SearchDateRange,
} from '@/features/mailbox/utils/search-query-parser';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const ANY_DATE_RANGE = '__all__';

/**
 * Search form for the unified inbox header: parses Gmail-style operators (`is:unread`,
 * `has:attachment`, `after:7d`, `after:30d`) on submit and reveals a compact strip of clickable
 * quick-filter chips while focused or toggled open.
 */
export function InboxSearchBar({
  q,
  onSearch,
  unread,
  onUnreadChange,
  attachments = false,
  onAttachmentsChange,
  dateRange = null,
  onDateRangeChange,
  searchRef,
}: {
  q: string;
  onSearch: (q: string) => void;
  unread: boolean;
  onUnreadChange: (unread: boolean) => void;
  attachments?: boolean;
  onAttachmentsChange?: (attachments: boolean) => void;
  dateRange?: SearchDateRange | null;
  onDateRangeChange?: (range: SearchDateRange | null) => void;
  searchRef?: Ref<HTMLInputElement>;
}) {
  const t = useT('mailbox');
  const [search, setSearch] = useState(q);
  const [committed, setCommitted] = useState(q);
  const [focused, setFocused] = useState(false);
  const [chipsPinned, setChipsPinned] = useState(false);

  // A search changed from outside (a reset URL, another view) shows in the field.
  if (committed !== q) {
    setCommitted(q);
    setSearch(q);
  }

  const showChips = focused || chipsPinned;
  const hasActiveChip = unread || attachments || dateRange === 'week' || dateRange === 'month';

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const parsed = parseSearchOperators(search);
    const hasOperators =
      parsed.unread !== undefined ||
      parsed.attachments !== undefined ||
      parsed.dateRange !== undefined;
    if (parsed.unread) onUnreadChange(true);
    if (parsed.attachments) onAttachmentsChange?.(true);
    if (parsed.dateRange) onDateRangeChange?.(parsed.dateRange);
    if (hasOperators) setSearch(parsed.cleanQuery);
    onSearch(parsed.cleanQuery);
  };

  const change = (value: string) => {
    setSearch(value);
    if (value.trim() === '' && q !== '') onSearch('');
  };

  /** `Esc` clears the field; an empty field leaves `Esc` to the page. */
  const escape = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key !== 'Escape') return;
    if (search !== '') {
      event.preventDefault();
      change('');
      return;
    }
    if (chipsPinned) setChipsPinned(false);
  };

  const handleContainerBlur = (event: FocusEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setFocused(false);
    }
  };

  return (
    <div className="mt-3" onFocus={() => setFocused(true)} onBlur={handleContainerBlur}>
      <form
        role="search"
        aria-label={t('inbox.search.label')}
        onSubmit={submit}
        className="flex gap-2"
      >
        <div className="relative min-w-0 flex-1">
          <Search
            className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-text-muted"
            aria-hidden="true"
          />
          <Input
            ref={searchRef}
            type="search"
            value={search}
            maxLength={MAIL_SEARCH_MAX_LENGTH}
            aria-label={t('inbox.search.label')}
            placeholder={t('inbox.search.placeholder')}
            className="h-8 pr-8 pl-8 text-[13px] [&::-webkit-search-cancel-button]:appearance-none"
            onChange={event => change(event.target.value)}
            onKeyDown={escape}
          />
          <button
            type="button"
            aria-label={t('searchChips.toggle')}
            aria-expanded={showChips}
            title={t('searchChips.toggle')}
            className={cn(
              'absolute top-1/2 right-1 inline-flex size-6 -translate-y-1/2 items-center justify-center rounded-md text-text-muted transition-colors focus-ring hover:bg-row-hover hover:text-text-main motion-reduce:transition-none',
              (showChips || hasActiveChip) && 'text-brand'
            )}
            onClick={() => setChipsPinned(open => !open)}
          >
            <SlidersHorizontal className="size-3.5" aria-hidden="true" />
          </button>
        </div>
        {onDateRangeChange ? (
          <Select
            value={dateRange ?? ANY_DATE_RANGE}
            onValueChange={value =>
              onDateRangeChange(value === ANY_DATE_RANGE ? null : (value as SearchDateRange))
            }
          >
            <SelectTrigger
              size="sm"
              aria-label={t('inbox.date.label')}
              className={cn(
                'shrink-0 border-border-main bg-bg-surface px-2 text-[13px]',
                dateRange ? 'font-medium text-text-main' : 'text-text-muted'
              )}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="end">
              <SelectItem value={ANY_DATE_RANGE}>{t('inbox.date.any')}</SelectItem>
              {INBOX_DATE_RANGES.map(range => (
                <SelectItem key={range} value={range}>
                  {t(`inbox.date.${range}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
      </form>
      {showChips ? (
        <div
          role="group"
          aria-label={t('searchChips.label')}
          className="mt-2 flex flex-wrap items-center gap-1.5"
        >
          <QuickFilterChip
            active={unread}
            icon={<Mail className="size-3" aria-hidden="true" />}
            label={t('searchChips.unread')}
            onClick={() => onUnreadChange(!unread)}
          />
          {onAttachmentsChange ? (
            <QuickFilterChip
              active={attachments}
              icon={<Paperclip className="size-3" aria-hidden="true" />}
              label={t('searchChips.hasAttachment')}
              onClick={() => onAttachmentsChange(!attachments)}
            />
          ) : null}
          {onDateRangeChange ? (
            <>
              <QuickFilterChip
                active={dateRange === 'week'}
                icon={<Calendar className="size-3" aria-hidden="true" />}
                label={t('searchChips.last7Days')}
                onClick={() => onDateRangeChange(dateRange === 'week' ? null : 'week')}
              />
              <QuickFilterChip
                active={dateRange === 'month'}
                icon={<Calendar className="size-3" aria-hidden="true" />}
                label={t('searchChips.last30Days')}
                onClick={() => onDateRangeChange(dateRange === 'month' ? null : 'month')}
              />
            </>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function QuickFilterChip({
  active,
  icon,
  label,
  onClick,
}: {
  active: boolean;
  icon: ReactNode;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-[12px] leading-5 whitespace-nowrap transition-colors focus-ring motion-reduce:transition-none',
        active
          ? 'border-blue-200 bg-blue-50 font-medium text-blue-800 dark:border-blue-800 dark:bg-blue-950/70 dark:text-blue-200'
          : 'border-border-main bg-bg-surface text-text-muted hover:border-border-strong hover:bg-row-hover hover:text-text-main'
      )}
    >
      {icon}
      <span>{label}</span>
    </button>
  );
}
