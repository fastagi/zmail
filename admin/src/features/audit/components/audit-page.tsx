import { ChevronRight, Copy, History, Search, X } from 'lucide-react';
import { type FormEvent, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { SectionCard } from '@/components/layout/section-card';
import { Button } from '@/components/ui/button';
import { DataTable, type DataTableColumn } from '@/components/ui/data-table';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Pagination } from '@/components/ui/pagination';
import { StatusBadge } from '@/components/ui/status-badge';
import { useAuditLogs } from '@/features/audit/hooks/use-audit-logs';
import type { AuditEntry, AuditSearch } from '@/features/audit/types';
import {
  buildDebugSummary,
  formatAuditTitle,
  formatHttpStatus,
  formatRelativeTime,
  isAuditError,
  isAuditServerError,
  methodBadgeClass,
} from '@/features/audit/utils/format-audit';
import { ApiError } from '@/http/client';
import { ApiErrorCode } from '@/http/codes';
import { copyText } from '@/lib/clipboard';
import { cn } from '@/lib/cn';

type AuditFilter = 'action' | 'user_id' | 'from' | 'to';
type StatusQuickFilter = 'all' | 'errors' | '5xx';

const auditFilters: readonly AuditFilter[] = ['action', 'user_id', 'from', 'to'];
const filterLabelKey = {
  action: 'audit.filters.action',
  user_id: 'audit.filters.userId',
  from: 'audit.filters.from',
  to: 'audit.filters.to',
} as const;

const TIME_PRESETS = [
  { id: 'last1h', labelKey: 'audit.presets.last1h', ms: 60 * 60 * 1000 },
  { id: 'last24h', labelKey: 'audit.presets.last24h', ms: 24 * 60 * 60 * 1000 },
  { id: 'last7d', labelKey: 'audit.presets.last7d', ms: 7 * 24 * 60 * 60 * 1000 },
] as const;

function isoSinceMs(ms: number): string {
  return new Date(Date.now() - ms).toISOString();
}

function toDateInputValue(value: string): string {
  if (!value || /^\d{4}-\d{2}-\d{2}$/.test(value)) return value;
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? '' : parsed.toLocaleDateString('en-CA');
}

interface AuditPageProps {
  search: AuditSearch;
  onSearchChange: (search: AuditSearch) => void;
}

export function AuditPage({ search, onSearchChange }: AuditPageProps) {
  const { t, i18n } = useTranslation();
  const logs = useAuditLogs(search);
  const [action, setAction] = useState(search.action ?? '');
  const [userId, setUserId] = useState(search.user_id ? String(search.user_id) : '');
  const [from, setFrom] = useState(search.from ?? '');
  const [to, setTo] = useState(search.to ?? '');
  const [statusFilter, setStatusFilter] = useState<StatusQuickFilter>('all');
  // Cursors of the pages before the current one, so Previous retraces the visited pages. A page
  // opened from a link has no trail, and Previous returns to the newest entries.
  const [trail, setTrail] = useState<string[]>([]);
  const [expanded, setExpanded] = useState<number | null>(null);
  const meta = logs.data?.meta;
  const activeFilters = auditFilters.filter((key) => search[key] !== undefined);
  const locale = i18n.language;

  const dateFormat = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        dateStyle: 'medium',
        timeStyle: 'medium',
      }),
    [locale],
  );

  const allRows = useMemo(() => logs.data?.data ?? [], [logs.data?.data]);

  const summary = useMemo(() => {
    let ok = 0;
    let clientError = 0;
    let serverError = 0;
    for (const entry of allRows) {
      if (isAuditServerError(entry)) {
        serverError += 1;
      } else if (isAuditError(entry)) {
        clientError += 1;
      } else {
        ok += 1;
      }
    }
    return {
      total: allRows.length,
      ok,
      clientError,
      serverError,
      errors: clientError + serverError,
    };
  }, [allRows]);

  const filteredRows = useMemo(() => {
    if (statusFilter === '5xx') {
      return allRows.filter(isAuditServerError);
    }
    if (statusFilter === 'errors') {
      return allRows.filter(isAuditError);
    }
    return allRows;
  }, [allRows, statusFilter]);

  const removeFilter = (key: AuditFilter) => {
    const setters = { action: setAction, user_id: setUserId, from: setFrom, to: setTo };
    setters[key]('');
    setTrail([]);
    onSearchChange({ ...search, [key]: undefined, cursor: undefined });
  };

  const applyTimePreset = (ms: number) => {
    const nextFrom = isoSinceMs(ms);
    setFrom(nextFrom);
    setTo('');
    setTrail([]);
    onSearchChange({
      ...search,
      from: nextFrom,
      to: undefined,
      cursor: undefined,
    });
  };

  const formatActor = (entry: AuditEntry) =>
    entry.user_id
      ? t('audit.actorUser', { id: entry.user_id })
      : t(`audit.actorType.${entry.actor_type}`, entry.actor_type);

  const copyRequestId = async (requestId: string) => {
    if (await copyText(requestId)) {
      toast.success(t('audit.copied', { id: requestId }));
    } else {
      toast.error(t('audit.copyFailed'));
    }
  };

  const copyDebugSummary = async (entry: AuditEntry) => {
    const title = formatAuditTitle(entry, locale);
    const timestamp = dateFormat.format(new Date(entry.created_at));
    const status = formatHttpStatus(entry.status_code, locale);
    const text = buildDebugSummary({
      entry,
      title,
      timestamp,
      actor: formatActor(entry),
      statusLabel: status.label,
    });
    if (await copyText(text)) {
      toast.success(t('audit.copiedSummary'));
    } else {
      toast.error(t('audit.copyFailed'));
    }
  };

  const goTo = (cursor: string | undefined, nextTrail: string[]) => {
    setTrail(nextTrail);
    onSearchChange({ ...search, cursor });
  };

  const formatFilterValue = (key: AuditFilter, value: string | number | undefined) => {
    const str = String(value ?? '');
    if ((key === 'from' || key === 'to') && str.includes('T')) {
      const parsed = new Date(str);
      if (!Number.isNaN(parsed.getTime())) {
        return dateFormat.format(parsed);
      }
    }
    return str;
  };

  const invalidRange =
    logs.error instanceof ApiError && logs.error.errorCode === ApiErrorCode.COMMON_INVALID_INPUT;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const parsedUser = Number.parseInt(userId, 10);
    onSearchChange({
      action: action.trim() || undefined,
      user_id: Number.isInteger(parsedUser) && parsedUser > 0 ? parsedUser : undefined,
      from: from || undefined,
      to: to || undefined,
      cursor: undefined,
    });
    setTrail([]);
  };

  const detailsId = (entry: AuditEntry) => `audit-entry-${entry.id}`;
  const columns: DataTableColumn<AuditEntry>[] = [
    {
      id: 'details',
      header: <span className="sr-only">{t('audit.columns.details')}</span>,
      headerClassName: 'w-10 pr-0',
      className: 'w-10 pr-0',
      cell: (entry) => {
        const open = expanded === entry.id;
        return (
          <Button
            type="button"
            size="icon"
            variant="ghost"
            className="size-7"
            aria-expanded={open}
            aria-controls={detailsId(entry)}
            aria-label={t(open ? 'audit.hideDetails' : 'audit.showDetails', {
              action: entry.action,
            })}
            onClick={() => setExpanded(open ? null : entry.id)}
          >
            <ChevronRight
              aria-hidden="true"
              className={cn('transition-transform', open && 'rotate-90')}
            />
          </Button>
        );
      },
    },
    {
      id: 'time',
      header: t('audit.columns.time'),
      className: 'whitespace-nowrap',
      cell: (entry) => {
        const formatted = dateFormat.format(new Date(entry.created_at));
        const relative = formatRelativeTime(entry.created_at, locale);
        return (
          <div title={formatted}>
            <div className="font-medium text-foreground">{relative}</div>
            <div className="text-xs text-muted-foreground tabular-nums">{formatted}</div>
          </div>
        );
      },
    },
    {
      id: 'actor',
      header: t('audit.columns.actor'),
      className: 'whitespace-nowrap',
      cell: (entry) => formatActor(entry),
    },
    {
      id: 'action',
      header: t('audit.columns.action'),
      cell: (entry) => (
        <div className="min-w-0">
          <div className="font-medium text-foreground">{formatAuditTitle(entry, locale)}</div>
          <div className="font-mono text-xs text-muted-foreground">
            {entry.action} · {entry.resource}
          </div>
        </div>
      ),
    },
    {
      id: 'request',
      header: t('audit.columns.request'),
      cell: (entry) => (
        <div className="min-w-0 space-y-1">
          <div className="flex items-center gap-1.5">
            <span
              className={cn(
                'inline-flex shrink-0 items-center rounded border px-1.5 py-0.5 font-mono text-[11px] font-semibold leading-none tracking-wide',
                methodBadgeClass(entry.method),
              )}
            >
              {entry.method}
            </span>
            <code
              className="block max-w-[260px] truncate text-xs text-foreground"
              title={entry.path}
            >
              {entry.path}
            </code>
          </div>
          {entry.request_id ? (
            <div className="font-mono text-xs text-muted-foreground">{entry.request_id}</div>
          ) : null}
        </div>
      ),
    },
    {
      id: 'status',
      header: t('audit.columns.status'),
      className: 'whitespace-nowrap',
      cell: (entry) => {
        const status = formatHttpStatus(entry.status_code, locale);
        return <StatusBadge tone={status.tone}>{status.label}</StatusBadge>;
      },
    },
  ];

  const rowClassName = (entry: AuditEntry) =>
    isAuditServerError(entry)
      ? '[&>td:first-child]:shadow-[inset_3px_0_0_0_var(--danger)] bg-danger-subtle/30 hover:bg-danger-subtle/50'
      : isAuditError(entry)
        ? '[&>td:first-child]:shadow-[inset_3px_0_0_0_var(--warning)] bg-warning-subtle/25 hover:bg-warning-subtle/45'
        : undefined;

  const renderDetails = (entry: AuditEntry) => {
    if (expanded !== entry.id) return null;
    const status = formatHttpStatus(entry.status_code, locale);
    const title = formatAuditTitle(entry, locale);
    return (
      <div className="rounded-lg border border-border/80 bg-card p-4 shadow-xs">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2 border-b border-border/70 pb-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-foreground">{title}</span>
            <StatusBadge tone={status.tone}>{status.label}</StatusBadge>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 text-xs"
              onClick={() => void copyDebugSummary(entry)}
            >
              <Copy aria-hidden="true" className="size-3.5" />
              {t('audit.copyDebugSummary')}
            </Button>
          </div>
        </div>
        <dl className="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-[max-content_1fr]">
          <dt className="text-muted-foreground">{t('audit.details.action')}</dt>
          <dd>
            <code>{entry.action}</code>
          </dd>
          <dt className="text-muted-foreground">{t('audit.details.resource')}</dt>
          <dd>
            <code>{entry.resource}</code>
          </dd>
          <dt className="text-muted-foreground">{t('audit.details.target')}</dt>
          <dd>
            {entry.target_type || entry.target_id ? (
              <code>{[entry.target_type, entry.target_id].filter(Boolean).join(' · ')}</code>
            ) : (
              t('audit.details.none')
            )}
          </dd>
          <dt className="text-muted-foreground">{t('audit.details.result')}</dt>
          <dd>{entry.result || t('audit.details.none')}</dd>
          <dt className="text-muted-foreground">{t('audit.details.request')}</dt>
          <dd className="break-all">
            <code>
              {entry.method} {entry.path}
            </code>
          </dd>
          {entry.request_id ? (
            <>
              <dt className="text-muted-foreground">{t('audit.details.requestId')}</dt>
              <dd className="flex items-center gap-2">
                <code>{entry.request_id}</code>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-7"
                  onClick={() => void copyRequestId(entry.request_id ?? '')}
                >
                  <Copy aria-hidden="true" />
                  {t('audit.copyRequestId')}
                </Button>
              </dd>
            </>
          ) : null}
        </dl>
      </div>
    );
  };

  return (
    <div className="page-stack">
      <header className="page-header">
        <p className="page-eyebrow">{t('audit.eyebrow')}</p>
        <h1>{t('audit.title')}</h1>
        <p>{t('audit.description')}</p>
      </header>

      <SectionCard
        icon={History}
        titleId="audit-title"
        title={t('audit.listTitle')}
        description={t('audit.rangeHint')}
        contentClassName="p-0"
        toolbar={
          <div className="space-y-3">
            <form
              role="search"
              onSubmit={submit}
              className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-5"
            >
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="audit-action">{t('audit.filters.action')}</Label>
                <Input
                  id="audit-action"
                  maxLength={120}
                  value={action}
                  onChange={(event) => setAction(event.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="audit-user-id">{t('audit.filters.userId')}</Label>
                <Input
                  id="audit-user-id"
                  inputMode="numeric"
                  pattern="[0-9]*"
                  value={userId}
                  onChange={(event) => setUserId(event.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="audit-from">{t('audit.filters.from')}</Label>
                <Input
                  id="audit-from"
                  type="date"
                  value={toDateInputValue(from)}
                  onChange={(event) => setFrom(event.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="audit-to">{t('audit.filters.to')}</Label>
                <Input
                  id="audit-to"
                  type="date"
                  value={toDateInputValue(to)}
                  onChange={(event) => setTo(event.target.value)}
                />
              </div>
              <div className="flex items-end">
                <Button type="submit" className="w-full">
                  <Search aria-hidden="true" />
                  {t('audit.apply')}
                </Button>
              </div>
            </form>

            <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label={t('audit.presets.label')}>
              <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                <History aria-hidden="true" className="size-3.5" />
                {t('audit.presets.label')}
              </span>
              {TIME_PRESETS.map((preset) => (
                <Button
                  key={preset.id}
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-7 rounded-full px-2.5 text-xs"
                  onClick={() => applyTimePreset(preset.ms)}
                >
                  {t(preset.labelKey)}
                </Button>
              ))}
            </div>

            {activeFilters.length > 0 ? (
              <ul className="flex flex-wrap gap-2" aria-label={t('audit.activeFilters')}>
                {activeFilters.map((key) => (
                  <li
                    key={key}
                    className="inline-flex h-7 items-center gap-1 rounded-full bg-brand-subtle pr-1 pl-3 text-xs font-medium text-brand-strong"
                  >
                    {t(filterLabelKey[key])}: {formatFilterValue(key, search[key])}
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      className="size-5 rounded-full text-brand-strong hover:bg-brand/10"
                      aria-label={t('audit.removeFilter', { filter: t(filterLabelKey[key]) })}
                      onClick={() => removeFilter(key)}
                    >
                      <X aria-hidden="true" className="size-3" />
                    </Button>
                  </li>
                ))}
              </ul>
            ) : null}

            <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border/70 pt-3">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground tabular-nums">
                <span className="font-medium text-foreground">
                  {t('audit.summary.total', { count: summary.total })}
                </span>
                <span aria-hidden="true">·</span>
                <span className="text-success-strong">
                  {t('audit.summary.ok', { count: summary.ok })}
                </span>
                <span aria-hidden="true">·</span>
                <span className={cn(summary.clientError > 0 && 'font-medium text-warning-strong')}>
                  {t('audit.summary.clientError', { count: summary.clientError })}
                </span>
                <span aria-hidden="true">·</span>
                <span className={cn(summary.serverError > 0 && 'font-medium text-danger-strong')}>
                  {t('audit.summary.serverError', { count: summary.serverError })}
                </span>
              </div>

              <div
                className="flex flex-wrap items-center gap-1.5"
                role="group"
                aria-label={t('audit.quickFilter.label')}
              >
                {(
                  [
                    { id: 'all', label: t('audit.quickFilter.all'), count: summary.total },
                    { id: 'errors', label: t('audit.quickFilter.errors'), count: summary.errors },
                    {
                      id: '5xx',
                      label: t('audit.quickFilter.serverErrors'),
                      count: summary.serverError,
                    },
                  ] as const
                ).map((item) => {
                  const active = statusFilter === item.id;
                  return (
                    <button
                      key={item.id}
                      type="button"
                      aria-pressed={active}
                      onClick={() => setStatusFilter(item.id)}
                      className={cn(
                        'inline-flex h-7 items-center gap-1.5 rounded-full border px-2.5 text-xs font-medium transition-colors',
                        active
                          ? item.id === '5xx'
                            ? 'border-danger/40 bg-danger-subtle text-danger-strong'
                            : item.id === 'errors'
                              ? 'border-warning/40 bg-warning-subtle text-warning-strong'
                              : 'border-brand/30 bg-brand-subtle text-brand-strong'
                          : 'border-border bg-background text-muted-foreground hover:bg-muted hover:text-foreground',
                      )}
                    >
                      <span>{item.label}</span>
                      {item.id !== 'all' && item.count > 0 ? (
                        <span className="rounded-full bg-background/80 px-1.5 text-[11px] tabular-nums">
                          {item.count}
                        </span>
                      ) : null}
                    </button>
                  );
                })}
              </div>
            </div>
          </div>
        }
      >
        <DataTable
          columns={columns}
          rows={logs.status === 'success' ? filteredRows : logs.data?.data}
          rowKey={(entry) => entry.id}
          rowClassName={rowClassName}
          status={logs.status}
          errorMessage={
            invalidRange ? t('audit.errors.invalidRange') : t('audit.errors.unavailable')
          }
          emptyMessage={
            statusFilter !== 'all' && allRows.length > 0
              ? t('audit.quickFilter.empty')
              : t('audit.empty')
          }
          minWidthClassName="min-w-[780px]"
          alignTop
          skeletonRows={5}
          details={{ id: detailsId, render: renderDetails }}
        />

        {meta && (search.cursor || meta.has_more) ? (
          <Pagination
            label={t('audit.pagination')}
            hasPrevious={Boolean(search.cursor)}
            hasNext={Boolean(meta.next_cursor)}
            onPrevious={() => goTo(trail.at(-1) || undefined, trail.slice(0, -1))}
            onNext={() => goTo(meta.next_cursor ?? undefined, [...trail, search.cursor ?? ''])}
          />
        ) : null}
      </SectionCard>
    </div>
  );
}
