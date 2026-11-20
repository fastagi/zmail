import { ArrowLeft, FileText, Search, Send, X } from 'lucide-react';
import { type FormEvent, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { SectionCard } from '@/components/layout/section-card';
import { Button } from '@/components/ui/button';
import { DataTable, type DataTableColumn } from '@/components/ui/data-table';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { PagePagination } from '@/components/ui/pagination';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { StatusBadge } from '@/components/ui/status-badge';
import { BroadcastContentDialog } from '@/features/mail-operations/components/broadcast-content-dialog';
import { DayPicker } from '@/features/mail-operations/components/day-picker';
import { LoadFailure } from '@/features/mail-operations/components/load-failure';
import { useOutboundMessages } from '@/features/mail-operations/hooks/use-mail-operations';
import {
  type OutboundMessage,
  type OutboundMessageSearch,
  type OutboundStatus,
  outboundStatuses,
  todayUtc,
} from '@/features/mail-operations/types';
import { useOperatorCapability } from '@/features/operator-session/hooks/use-operator-session';

interface OutboundMessagesPageProps {
  search: OutboundMessageSearch;
  onSearchChange: (search: OutboundMessageSearch) => void;
  /** Returns to the organization list of the selected day. */
  onBack: () => void;
}

// Radix Select reserves the empty value, so the "any" filter uses a sentinel item.
const ANY = 'any';

const statusTone = { sent: 'success', failed: 'danger', queued: 'neutral' } as const;
const knownReceipts = [
  'accepted',
  'delivered',
  'deferred',
  'bounced',
  'failed',
  'rejected',
  'complained',
] as const;

function positiveId(value: string): number | undefined {
  const parsed = Number.parseInt(value, 10);
  return /^\d+$/.test(value.trim()) && parsed > 0 ? parsed : undefined;
}

export function OutboundMessagesPage({
  search,
  onSearchChange,
  onBack,
}: OutboundMessagesPageProps) {
  const { t } = useTranslation();
  const messages = useOutboundMessages(search);
  // UX only: the API checks mail_content:read and the broadcast rule on every read.
  const canReadContent = useOperatorCapability('mail_content:read');
  const [reading, setReading] = useState<OutboundMessage | null>(null);
  const [status, setStatus] = useState<string>(search.status ?? '');
  const [organizationId, setOrganizationId] = useState(
    search.organization_id ? String(search.organization_id) : '',
  );
  const [mailboxId, setMailboxId] = useState(search.mailbox_id ? String(search.mailbox_id) : '');
  const meta = messages.data?.meta;
  const day = search.date ?? todayUtc();
  const timeFormat = new Intl.DateTimeFormat('en-GB', {
    timeZone: 'UTC',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    onSearchChange({
      date: search.date,
      status: (status || undefined) as OutboundStatus | undefined,
      organization_id: positiveId(organizationId),
      mailbox_id: positiveId(mailboxId),
      page: undefined,
    });
  };

  const clear = () => {
    setStatus('');
    setOrganizationId('');
    setMailboxId('');
    onSearchChange({ date: search.date });
  };

  const columns: DataTableColumn<OutboundMessage>[] = [
    {
      id: 'time',
      header: t('mailOperations.messages.columns.time'),
      className: 'whitespace-nowrap tabular-nums',
      cell: (message) => `${timeFormat.format(new Date(message.created_at))} UTC`,
    },
    {
      id: 'organization',
      header: t('mailOperations.messages.columns.organization'),
      cell: (message) => (
        <>
          <div className="font-medium">{message.organization_name}</div>
          <div className="text-xs text-muted-foreground">#{message.organization_id}</div>
        </>
      ),
    },
    {
      id: 'from',
      header: t('mailOperations.messages.columns.from'),
      cell: (message) => (
        <>
          <div>{message.from_address}</div>
          {message.mailbox_address !== message.from_address ? (
            <div className="text-xs text-muted-foreground">{message.mailbox_address}</div>
          ) : null}
        </>
      ),
    },
    {
      id: 'subject',
      header: t('mailOperations.messages.columns.subject'),
      className: 'max-w-xs truncate',
      cell: (message) => message.subject || t('mailOperations.messages.noSubject'),
    },
    {
      id: 'status',
      header: t('mailOperations.messages.columns.status'),
      cell: (message) => (
        <>
          <StatusBadge tone={statusTone[message.status]}>
            {t(`mailOperations.status.${message.status}`)}
          </StatusBadge>
          {message.failure_code ? (
            <div className="mt-1 text-xs text-muted-foreground">
              <code>{message.failure_code}</code>
            </div>
          ) : null}
        </>
      ),
    },
    {
      id: 'receipts',
      header: t('mailOperations.messages.columns.receipts'),
      cell: (message) =>
        (knownReceipts as readonly string[]).includes(message.last_event)
          ? t(`mailOperations.receipt.${message.last_event as (typeof knownReceipts)[number]}`)
          : t('mailOperations.messages.noReceipts'),
    },
    ...(canReadContent
      ? [
          {
            id: 'actions',
            header: t('mailOperations.messages.columns.actions'),
            className: 'whitespace-nowrap',
            // Only broadcast messages have a readable body; every other row has no action.
            cell: (message: OutboundMessage) =>
              message.broadcast_id ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  aria-label={t('mailOperations.messages.viewContentOf', {
                    subject: message.subject || t('mailOperations.messages.noSubject'),
                  })}
                  onClick={() => setReading(message)}
                >
                  <FileText aria-hidden="true" />
                  {t('mailOperations.messages.viewContent')}
                </Button>
              ) : null,
          } satisfies DataTableColumn<OutboundMessage>,
        ]
      : []),
  ];

  return (
    <div className="page-stack">
      <div>
        <Button type="button" variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft aria-hidden="true" />
          {t('mailOperations.backToOrganizations')}
        </Button>
      </div>
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <header className="page-header">
          <p className="page-eyebrow">{t('mailOperations.eyebrow')}</p>
          <h1>{t('mailOperations.messages.title', { date: day })}</h1>
          <p>{t('mailOperations.messages.description')}</p>
        </header>
        <DayPicker
          value={search.date}
          onChange={(date) => onSearchChange({ ...search, date, page: undefined })}
        />
      </div>

      <SectionCard
        icon={Send}
        titleId="outbound-messages-title"
        title={t('mailOperations.messages.listTitle')}
        description={
          meta
            ? t('mailOperations.messages.total', { count: meta.total })
            : t('mailOperations.messages.privacyHint')
        }
        contentClassName="p-0"
        toolbar={
          <form
            role="search"
            onSubmit={submit}
            className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-5"
          >
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="outbound-status">{t('mailOperations.messages.filters.status')}</Label>
              <Select
                name="status"
                value={status || ANY}
                onValueChange={(value) => setStatus(value === ANY ? '' : value)}
              >
                <SelectTrigger id="outbound-status">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ANY}>{t('mailOperations.any')}</SelectItem>
                  {outboundStatuses.map((value) => (
                    <SelectItem key={value} value={value}>
                      {t(`mailOperations.status.${value}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="outbound-organization">
                {t('mailOperations.messages.filters.organizationId')}
              </Label>
              <Input
                id="outbound-organization"
                inputMode="numeric"
                pattern="[0-9]*"
                value={organizationId}
                onChange={(event) => setOrganizationId(event.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="outbound-mailbox">
                {t('mailOperations.messages.filters.mailboxId')}
              </Label>
              <Input
                id="outbound-mailbox"
                inputMode="numeric"
                pattern="[0-9]*"
                value={mailboxId}
                onChange={(event) => setMailboxId(event.target.value)}
              />
            </div>
            <div className="flex items-end">
              <Button type="submit" className="w-full">
                <Search aria-hidden="true" />
                {t('mailOperations.apply')}
              </Button>
            </div>
            <div className="flex items-end">
              <Button type="button" variant="outline" className="w-full" onClick={clear}>
                <X aria-hidden="true" />
                {t('mailOperations.messages.clear')}
              </Button>
            </div>
          </form>
        }
      >
        {messages.isError ? (
          <LoadFailure
            error={messages.error}
            onRetry={() => void messages.refetch()}
            retrying={messages.isFetching}
          />
        ) : (
          <DataTable
            columns={columns}
            rows={messages.data?.data}
            rowKey={(message) => message.id}
            status={messages.status}
            errorMessage={t('mailOperations.errors.unavailable')}
            emptyMessage={t('mailOperations.messages.empty')}
            minWidthClassName="min-w-[860px]"
            alignTop
            skeletonRows={5}
          />
        )}
        <PagePagination
          meta={meta}
          label={t('mailOperations.pagination')}
          onPageChange={(page) => onSearchChange({ ...search, page })}
        />
      </SectionCard>
      <BroadcastContentDialog message={reading} onClose={() => setReading(null)} />
    </div>
  );
}
