'use client';

import { useMemo, useState, type FormEvent } from 'react';
import { History, Search } from 'lucide-react';
import { useLocale } from 'next-intl';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { SendLogDetail } from '@/features/developer/components/send-log-detail';
import { useDeveloperTab } from '@/features/developer/hooks/use-developer-tab';
import { useTransactionalLog } from '@/features/developer/hooks/use-developer';
import {
  TRANSACTIONAL_LOG_RECIPIENT_MAX_LENGTH,
  TRANSACTIONAL_TEMPLATE_KEY_PATTERN,
} from '@/features/developer/schemas';
import type { TransactionalLogFilters, TransactionalOutcome } from '@/features/developer/types';
import { useDeveloperErrorMessage } from '@/features/developer/utils/developer-error';
import { LOG_STATUS_CLASSES, logStatus } from '@/features/developer/utils/log-status';
import { OrganizationLostState } from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  nativeSelectClassName,
} from '@/features/mailbox/components/mail-shared';
import type { MailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailTemplates } from '@/features/mailbox/hooks/use-mail-templates';
import { isOrganizationLost } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** URL parameters of the log's filters; the recipient search stays out of the URL. */
export const LOG_OUTCOME_PARAM = 'outcome';
export const LOG_TEMPLATE_PARAM = 'template';

const CHIPS = ['all', 'delivered', 'problem', 'test'] as const;
type Chip = (typeof CHIPS)[number];

function outcomeOf(value: string | null): TransactionalOutcome | undefined {
  return value === 'delivered' || value === 'problem' ? value : undefined;
}

function templateOf(value: string | null): string | undefined {
  return value !== null && TRANSACTIONAL_TEMPLATE_KEY_PATTERN.test(value) ? value : undefined;
}

/**
 * The transactional send log: outcome chips (Test switches the page to test mode), an exact
 * recipient search, and a template filter; newest first, with "Older" for the next keyset page.
 * Rows show only masked recipients and subjects. A selected row opens its timeline.
 */
export function SendLogTab({ organization }: { organization: MailOrganization }) {
  const t = useT('developer');
  const tm = useT('mailbox');
  const locale = useLocale();
  const errorMessage = useDeveloperErrorMessage();
  const { mode, param, update } = useDeveloperTab();
  const outcome = mode === 'test' ? undefined : outcomeOf(param(LOG_OUTCOME_PARAM));
  const template = templateOf(param(LOG_TEMPLATE_PARAM));
  const [recipient, setRecipient] = useState('');
  const [to, setTo] = useState('');
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const templates = useMailTemplates(organization.id, 'transactional');

  const filters: TransactionalLogFilters = {
    mode,
    ...(outcome ? { outcome } : {}),
    ...(template ? { template } : {}),
    ...(to ? { to } : {}),
  };
  const log = useTransactionalLog(organization.id, filters);
  const entries = useMemo(() => log.data?.pages.flatMap(page => page.items) ?? [], [log.data]);
  const total24h = log.data?.pages[0]?.meta.total_24h;
  const selected = entries.find(entry => entry.id === selectedId) ?? null;
  const filtered = outcome !== undefined || template !== undefined || to !== '';

  const timeFormat = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
      }),
    [locale]
  );

  const chip: Chip = mode === 'test' ? 'test' : (outcome ?? 'all');
  const pickChip = (next: Chip) => {
    setSelectedId(null);
    if (next === 'test') update({ mode: 'test', [LOG_OUTCOME_PARAM]: null });
    else update({ mode: 'live', [LOG_OUTCOME_PARAM]: next === 'all' ? null : next });
  };

  const search = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSelectedId(null);
    setTo(recipient.trim().toLowerCase());
  };

  const templateKeys = (templates.data ?? []).map(item => item.key).filter(Boolean) as string[];
  if (template && !templateKeys.includes(template)) templateKeys.push(template);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <div role="group" aria-label={t('log.filters')} className="flex flex-wrap gap-2">
          {CHIPS.map(item => (
            <Button
              key={item}
              type="button"
              size="sm"
              variant={chip === item ? 'default' : 'outline'}
              aria-pressed={chip === item}
              className="rounded-full"
              onClick={() => pickChip(item)}
            >
              {t(`log.chips.${item}`)}
            </Button>
          ))}
        </div>
        <span className="flex-1" />
        <p className="text-xs text-muted-foreground">{t('log.banner')}</p>
      </div>

      <div className="flex flex-wrap items-end gap-3">
        <form onSubmit={search} className="flex min-w-0 flex-1 items-end gap-2" role="search">
          <div className="min-w-0 flex-1 space-y-1">
            <Label htmlFor="send-log-recipient" className="text-xs text-muted-foreground">
              {t('log.recipient')}
            </Label>
            <Input
              id="send-log-recipient"
              type="email"
              inputMode="email"
              autoComplete="off"
              spellCheck={false}
              maxLength={TRANSACTIONAL_LOG_RECIPIENT_MAX_LENGTH}
              placeholder={t('log.recipientPlaceholder')}
              value={recipient}
              onChange={event => setRecipient(event.target.value)}
            />
          </div>
          <Button type="submit" variant="outline" icon={<Search className="size-4" />}>
            {t('log.search')}
          </Button>
          {to ? (
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setRecipient('');
                setTo('');
              }}
            >
              {t('log.clearSearch')}
            </Button>
          ) : null}
        </form>
        <div className="space-y-1">
          <Label htmlFor="send-log-template" className="text-xs text-muted-foreground">
            {t('log.template')}
          </Label>
          <select
            id="send-log-template"
            className={cn(nativeSelectClassName, 'block w-48')}
            value={template ?? ''}
            onChange={event => {
              setSelectedId(null);
              update({ [LOG_TEMPLATE_PARAM]: event.target.value || null });
            }}
          >
            <option value="">{t('log.allTemplates')}</option>
            {templateKeys.map(key => (
              <option key={key} value={key}>
                {key}
              </option>
            ))}
          </select>
        </div>
      </div>

      {total24h !== undefined ? (
        <p className="text-xs text-muted-foreground">
          {t('log.total24h', { count: total24h.toLocaleString(locale) })}
        </p>
      ) : null}

      {log.isPending ? (
        <div className="space-y-2" aria-hidden="true">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </div>
      ) : log.error && entries.length === 0 ? (
        isOrganizationLost(log.error) ? (
          <OrganizationLostState />
        ) : (
          <ErrorAlert
            message={errorMessage(log.error)}
            retryLabel={tm('actions.retry')}
            onRetry={() => void log.refetch()}
          />
        )
      ) : entries.length === 0 ? (
        <EmptyState
          icon={<History className="size-5" aria-hidden="true" />}
          title={filtered ? t('log.noMatchTitle') : t('log.emptyTitle')}
          description={filtered ? t('log.noMatchDescription') : t('log.emptyDescription')}
        />
      ) : (
        <div
          className={cn('grid gap-4', selected && 'lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]')}
        >
          <div className="min-w-0 space-y-3">
            <div className="overflow-x-auto">
              <Table aria-label={t('log.table')}>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('log.columns.time')}</TableHead>
                    <TableHead>{t('log.columns.template')}</TableHead>
                    <TableHead>{t('log.columns.recipient')}</TableHead>
                    <TableHead>{t('log.columns.content')}</TableHead>
                    <TableHead>{t('log.columns.status')}</TableHead>
                    <TableHead>{t('log.columns.idempotencyKey')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {entries.map(entry => {
                    const status = logStatus(entry);
                    const active = entry.id === selectedId;
                    return (
                      <TableRow
                        key={entry.id}
                        data-state={active ? 'selected' : undefined}
                        className="cursor-pointer"
                        onClick={() => setSelectedId(entry.id)}
                      >
                        <TableCell className="whitespace-nowrap text-muted-foreground tabular-nums">
                          {timeFormat.format(new Date(entry.created_at))}
                        </TableCell>
                        <TableCell>
                          <button
                            type="button"
                            aria-label={t('log.open', {
                              template: entry.template,
                              to: entry.to_masked,
                            })}
                            aria-pressed={active}
                            className="rounded-sm font-mono text-xs focus-ring"
                            onClick={event => {
                              event.stopPropagation();
                              setSelectedId(entry.id);
                            }}
                          >
                            {entry.template}
                          </button>
                        </TableCell>
                        <TableCell className="whitespace-nowrap">{entry.to_masked}</TableCell>
                        <TableCell className="max-w-64 truncate text-muted-foreground">
                          {entry.subject}
                        </TableCell>
                        <TableCell>
                          <span
                            className={cn(
                              'inline-flex h-6 items-center rounded-full px-2.5 text-xs whitespace-nowrap',
                              LOG_STATUS_CLASSES[status]
                            )}
                          >
                            {t(`status.${status}`)}
                          </span>
                        </TableCell>
                        <TableCell className="max-w-48 truncate font-mono text-xs text-muted-foreground">
                          {entry.idempotency_key ?? '—'}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
            {log.hasNextPage ? (
              <Button
                type="button"
                variant="outline"
                loading={log.isFetchingNextPage}
                onClick={() => void log.fetchNextPage()}
              >
                {t('log.older')}
              </Button>
            ) : null}
          </div>
          {selected ? (
            <SendLogDetail
              organizationId={organization.id}
              entry={selected}
              onClose={() => setSelectedId(null)}
            />
          ) : null}
        </div>
      )}
    </div>
  );
}
