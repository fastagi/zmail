import { ArrowLeft, Building2, Search } from 'lucide-react';
import { type FormEvent, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { SectionCard } from '@/components/layout/section-card';
import { Button } from '@/components/ui/button';
import { DataTable, type DataTableColumn } from '@/components/ui/data-table';
import { Input } from '@/components/ui/input';
import { PagePagination } from '@/components/ui/pagination';
import { DayPicker } from '@/features/mail-operations/components/day-picker';
import { LoadFailure } from '@/features/mail-operations/components/load-failure';
import { useMailOrganizations } from '@/features/mail-operations/hooks/use-mail-operations';
import {
  type MailOrganization,
  type MailOrganizationSearch,
  todayUtc,
} from '@/features/mail-operations/types';

interface MailOrganizationsPageProps {
  search: MailOrganizationSearch;
  onSearchChange: (search: MailOrganizationSearch) => void;
  onBack: () => void;
  /** Opens the existing member list of one organization. */
  onOpenMembers: (organizationId: number) => void;
  /** Opens one organization's outbound log for the selected day. */
  onOpenMessages: (organizationId: number) => void;
}

export function MailOrganizationsPage({
  search,
  onSearchChange,
  onBack,
  onOpenMembers,
  onOpenMessages,
}: MailOrganizationsPageProps) {
  const { t } = useTranslation();
  const organizations = useMailOrganizations(search);
  const [query, setQuery] = useState(search.q ?? '');
  const meta = organizations.data?.meta;
  const day = search.date ?? todayUtc();

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    onSearchChange({ ...search, q: query.trim() || undefined, page: undefined });
  };

  const columns: DataTableColumn<MailOrganization>[] = [
    {
      id: 'organization',
      header: t('mailOperations.organizations.columns.organization'),
      cell: (row) => (
        <>
          <div className="font-medium">{row.name}</div>
          <div className="text-xs text-muted-foreground">#{row.id}</div>
        </>
      ),
    },
    {
      id: 'members',
      header: t('mailOperations.organizations.columns.members'),
      cell: (row) => (
        <Button
          type="button"
          variant="link"
          className="h-auto p-0 tabular-nums"
          aria-label={t('mailOperations.organizations.openMembers', {
            name: row.name,
            count: row.members,
          })}
          onClick={() => onOpenMembers(row.id)}
        >
          {row.members}
        </Button>
      ),
    },
    {
      id: 'mailboxes',
      header: t('mailOperations.organizations.columns.mailboxes'),
      className: 'tabular-nums',
      cell: (row) => row.mailboxes,
    },
    {
      id: 'sent',
      header: t('mailOperations.status.sent'),
      cell: (row) => (
        <Button
          type="button"
          variant="link"
          className="h-auto p-0 tabular-nums"
          aria-label={t('mailOperations.organizations.openMessages', {
            name: row.name,
            count: row.sent,
          })}
          onClick={() => onOpenMessages(row.id)}
        >
          {row.sent}
        </Button>
      ),
    },
    {
      id: 'failed',
      header: t('mailOperations.status.failed'),
      className: 'tabular-nums',
      cell: (row) => row.failed,
    },
    {
      id: 'queued',
      header: t('mailOperations.status.queued'),
      className: 'tabular-nums',
      cell: (row) => row.queued,
    },
  ];

  return (
    <div className="page-stack">
      <div>
        <Button type="button" variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft aria-hidden="true" />
          {t('mailOperations.backToOverview')}
        </Button>
      </div>
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <header className="page-header">
          <p className="page-eyebrow">{t('mailOperations.eyebrow')}</p>
          <h1>{t('mailOperations.organizations.title', { date: day })}</h1>
          <p>{t('mailOperations.organizations.description')}</p>
        </header>
        <DayPicker
          value={search.date}
          onChange={(date) => onSearchChange({ ...search, date, page: undefined })}
        />
      </div>

      <SectionCard
        icon={Building2}
        titleId="mail-organizations-title"
        title={t('mailOperations.organizations.listTitle')}
        description={
          meta
            ? t('mailOperations.organizations.total', { count: meta.total })
            : t('mailOperations.utcHint')
        }
        contentClassName="p-0"
        toolbar={
          <form role="search" onSubmit={submit} className="flex gap-2">
            <Input
              aria-label={t('mailOperations.organizations.search')}
              placeholder={t('mailOperations.organizations.search')}
              value={query}
              maxLength={100}
              onChange={(event) => setQuery(event.target.value)}
            />
            <Button type="submit" variant="outline">
              <Search aria-hidden="true" />
              {t('mailOperations.apply')}
            </Button>
          </form>
        }
      >
        {organizations.isError ? (
          <LoadFailure
            error={organizations.error}
            onRetry={() => void organizations.refetch()}
            retrying={organizations.isFetching}
          />
        ) : (
          <DataTable
            columns={columns}
            rows={organizations.data?.data}
            rowKey={(row) => row.id}
            status={organizations.status}
            errorMessage={t('mailOperations.errors.unavailable')}
            emptyMessage={t('mailOperations.organizations.empty')}
            minWidthClassName="min-w-[640px]"
            skeletonRows={5}
          />
        )}
        <PagePagination
          meta={meta}
          label={t('mailOperations.pagination')}
          onPageChange={(page) => onSearchChange({ ...search, page })}
        />
      </SectionCard>
    </div>
  );
}
