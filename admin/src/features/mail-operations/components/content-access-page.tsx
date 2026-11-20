import { FileSearch, Search, X } from 'lucide-react';
import { type FormEvent, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { SectionCard } from '@/components/layout/section-card';
import { Button } from '@/components/ui/button';
import { DataTable, type DataTableColumn } from '@/components/ui/data-table';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { PagePagination } from '@/components/ui/pagination';
import { LoadFailure } from '@/features/mail-operations/components/load-failure';
import { useContentAccessAudits } from '@/features/mail-operations/hooks/use-mail-operations';
import {
  type ContentAccess,
  type ContentAccessSearch,
  isSelectableDay,
} from '@/features/mail-operations/types';

interface ContentAccessPageProps {
  search: ContentAccessSearch;
  onSearchChange: (search: ContentAccessSearch) => void;
}

const timeFormat = new Intl.DateTimeFormat('en-GB', {
  timeZone: 'UTC',
  dateStyle: 'medium',
  timeStyle: 'medium',
});

function positiveId(value: string): number | undefined {
  const parsed = Number.parseInt(value, 10);
  return /^\d+$/.test(value.trim()) && parsed > 0 ? parsed : undefined;
}

function selectableDay(value: string): string | undefined {
  return isSelectableDay(value) ? value : undefined;
}

/** Records of operators reading broadcast bodies. Bodies and addresses are never part of them. */
export function ContentAccessPage({ search, onSearchChange }: ContentAccessPageProps) {
  const { t } = useTranslation();
  const records = useContentAccessAudits(search);
  const [organizationId, setOrganizationId] = useState(
    search.organization_id ? String(search.organization_id) : '',
  );
  const [viewerId, setViewerId] = useState(
    search.viewer_user_id ? String(search.viewer_user_id) : '',
  );
  const [from, setFrom] = useState(search.from ?? '');
  const [to, setTo] = useState(search.to ?? '');
  const meta = records.data?.meta;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const fromDay = selectableDay(from);
    const toDay = selectableDay(to);
    onSearchChange({
      organization_id: positiveId(organizationId),
      viewer_user_id: positiveId(viewerId),
      from: fromDay,
      // A range that ends before it starts would be rejected; drop the end instead.
      to: fromDay && toDay && toDay < fromDay ? undefined : toDay,
      page: undefined,
    });
  };

  const clear = () => {
    setOrganizationId('');
    setViewerId('');
    setFrom('');
    setTo('');
    onSearchChange({});
  };

  const columns: DataTableColumn<ContentAccess>[] = [
    {
      id: 'time',
      header: t('mailOperations.access.columns.time'),
      className: 'whitespace-nowrap tabular-nums',
      cell: (record) => `${timeFormat.format(new Date(record.accessed_at))} UTC`,
    },
    {
      id: 'viewer',
      header: t('mailOperations.access.columns.viewer'),
      className: 'whitespace-nowrap',
      cell: (record) => t('mailOperations.access.userRef', { id: record.viewer_user_id }),
    },
    {
      id: 'organization',
      header: t('mailOperations.access.columns.organization'),
      cell: (record) => (
        <>
          <div className="font-medium">
            {record.organization_name || t('mailOperations.access.unknownOrganization')}
          </div>
          <div className="text-xs text-muted-foreground">#{record.organization_id}</div>
        </>
      ),
    },
    {
      id: 'message',
      header: t('mailOperations.access.columns.message'),
      className: 'whitespace-nowrap',
      cell: (record) => t('mailOperations.access.messageRef', { id: record.message_id }),
    },
    {
      id: 'reason',
      header: t('mailOperations.access.columns.reason'),
      className: 'max-w-md whitespace-pre-wrap break-words',
      cell: (record) => record.reason,
    },
  ];

  return (
    <div className="page-stack">
      <header className="page-header">
        <p className="page-eyebrow">{t('mailOperations.eyebrow')}</p>
        <h1>{t('mailOperations.access.title')}</h1>
        <p>{t('mailOperations.access.description')}</p>
      </header>

      <SectionCard
        icon={FileSearch}
        titleId="content-access-title"
        title={t('mailOperations.access.listTitle')}
        {...(meta ? { description: t('mailOperations.access.total', { count: meta.total }) } : {})}
        contentClassName="p-0"
        toolbar={
          <form
            role="search"
            onSubmit={submit}
            className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-6"
          >
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="access-organization">
                {t('mailOperations.access.filters.organizationId')}
              </Label>
              <Input
                id="access-organization"
                inputMode="numeric"
                pattern="[0-9]*"
                value={organizationId}
                onChange={(event) => setOrganizationId(event.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="access-viewer">
                {t('mailOperations.access.filters.viewerUserId')}
              </Label>
              <Input
                id="access-viewer"
                inputMode="numeric"
                pattern="[0-9]*"
                value={viewerId}
                onChange={(event) => setViewerId(event.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="access-from">{t('mailOperations.access.filters.from')}</Label>
              <Input
                id="access-from"
                type="date"
                value={from}
                onChange={(event) => setFrom(event.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="access-to">{t('mailOperations.access.filters.to')}</Label>
              <Input
                id="access-to"
                type="date"
                value={to}
                onChange={(event) => setTo(event.target.value)}
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
                {t('mailOperations.access.clear')}
              </Button>
            </div>
          </form>
        }
      >
        {records.isError ? (
          <LoadFailure
            error={records.error}
            onRetry={() => void records.refetch()}
            retrying={records.isFetching}
            forbiddenDescription={t('mailOperations.access.forbidden')}
            unavailableMessage={t('mailOperations.access.unavailable')}
          />
        ) : (
          <DataTable
            columns={columns}
            rows={records.data?.data}
            rowKey={(record) => record.id}
            status={records.status}
            errorMessage={t('mailOperations.access.unavailable')}
            emptyMessage={t('mailOperations.access.empty')}
            minWidthClassName="min-w-[760px]"
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
    </div>
  );
}
