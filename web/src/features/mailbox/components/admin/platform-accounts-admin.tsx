'use client';

import { useEffect, useState } from 'react';
import { ArrowRightLeft, AtSign, MoreHorizontal, Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { HandOverPlatformAccountsDialog } from '@/features/mailbox/components/admin/hand-over-platform-accounts-dialog';
import { PlatformAccountDrawer } from '@/features/mailbox/components/admin/platform-account-drawer';
import {
  usePlatformAccountLookups,
  type PlatformAccountLookups,
} from '@/features/mailbox/components/admin/platform-account-lookups';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import { memberName } from '@/features/mailbox/components/mailbox-people';
import {
  ConfirmDialog,
  EmptyState,
  ErrorAlert,
  nativeSelectClassName,
} from '@/features/mailbox/components/mail-shared';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import {
  useDeletePlatformAccount,
  usePlatformAccounts,
} from '@/features/mailbox/hooks/use-platform-accounts';
import {
  PLATFORM_ACCOUNT_QUERY_MAX_LENGTH,
  PLATFORM_ACCOUNT_STATUSES,
  type PlatformAccount,
  type PlatformAccountFilters,
  type PlatformAccountStatus,
} from '@/features/mailbox/platform-account-schemas';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** How long typing pauses before the search runs. */
const SEARCH_DEBOUNCE_MS = 300;
const PAGE_SIZE = 15;

/** The filter selects' values: an id or a status as text, empty for any. */
interface FilterDraft {
  roleId: string;
  mailboxId: string;
  ownerUserId: string;
  status: string;
}

const noFilters: FilterDraft = { roleId: '', mailboxId: '', ownerUserId: '', status: '' };

type Dialog =
  | { kind: 'create' }
  | { kind: 'handover' }
  | { kind: 'edit' | 'delete'; account: PlatformAccount }
  | null;

/**
 * Admin page of the organization's platform account ledger: a searchable, filterable, numbered
 * table with a create and edit drawer (with the account's history), delete, and a bulk handover
 * between two people. Owners and admins only; the API remains the authority for every change.
 */
export function PlatformAccountsAdmin() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader
        title={t('platformAccounts.title')}
        description={t('platformAccounts.description')}
        scope={scope}
      />
      <MailOrganizationBoundary scope={scope}>
        {() => <PlatformAccounts />}
      </MailOrganizationBoundary>
    </div>
  );
}

function useDebounced(value: string, delay: number): string {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return debounced;
}

function filtersOf(query: string, draft: FilterDraft): PlatformAccountFilters {
  const filters: PlatformAccountFilters = {};
  if (query !== '') filters.q = query;
  if (draft.status !== '') filters.status = draft.status as PlatformAccountStatus;
  if (draft.roleId !== '') filters.roleId = Number(draft.roleId);
  if (draft.mailboxId !== '') filters.mailboxId = Number(draft.mailboxId);
  if (draft.ownerUserId !== '') filters.ownerUserId = Number(draft.ownerUserId);
  return filters;
}

function PlatformAccounts() {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const lookups = usePlatformAccountLookups(organizationId);
  const [search, setSearch] = useState('');
  const query = useDebounced(search.trim(), SEARCH_DEBOUNCE_MS);
  const [draft, setDraft] = useState<FilterDraft>(noFilters);
  const [page, setPage] = useState(1);
  const [dialog, setDialog] = useState<Dialog>(null);

  // A new search or filter starts again at the first page.
  const filterKey = JSON.stringify([query, draft]);
  const [filtered, setFiltered] = useState(filterKey);
  if (filtered !== filterKey) {
    setFiltered(filterKey);
    setPage(1);
  }

  const filters = filtersOf(query, draft);
  const ledger = usePlatformAccounts(organizationId, filters, page, PAGE_SIZE);
  const meta = ledger.data?.meta;
  const items = ledger.data?.data ?? [];
  const filtering = Object.keys(filters).length > 0;

  if (isOrganizationLost(ledger.error)) return <OrganizationLostState />;

  const changeFilter = (next: Partial<FilterDraft>) =>
    setDraft(current => ({ ...current, ...next }));

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          {meta ? t('platformAccounts.total', { count: meta.total }) : null}
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<ArrowRightLeft className="size-4" />}
            onClick={() => setDialog({ kind: 'handover' })}
          >
            {t('platformAccounts.handOver')}
          </Button>
          <Button
            type="button"
            size="sm"
            icon={<Plus className="size-4" />}
            onClick={() => setDialog({ kind: 'create' })}
          >
            {t('platformAccounts.create')}
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Input
          type="search"
          aria-label={t('platformAccounts.search')}
          placeholder={t('platformAccounts.search')}
          className="w-full sm:max-w-xs"
          maxLength={PLATFORM_ACCOUNT_QUERY_MAX_LENGTH}
          value={search}
          onChange={event => setSearch(event.target.value)}
        />
        <FilterSelect
          label={t('platformAccounts.filters.role')}
          value={draft.roleId}
          anyLabel={t('platformAccounts.filters.allRoles')}
          options={lookups.roles.map(role => ({ value: String(role.id), label: role.name }))}
          onChange={roleId => changeFilter({ roleId })}
        />
        <FilterSelect
          label={t('platformAccounts.filters.mailbox')}
          value={draft.mailboxId}
          anyLabel={t('platformAccounts.filters.allMailboxes')}
          options={lookups.mailboxes.map(mailbox => ({
            value: String(mailbox.id),
            label: mailbox.address,
          }))}
          onChange={mailboxId => changeFilter({ mailboxId })}
        />
        <FilterSelect
          label={t('platformAccounts.filters.owner')}
          value={draft.ownerUserId}
          anyLabel={t('platformAccounts.filters.allOwners')}
          options={lookups.people.map(person => ({
            value: String(person.user_id),
            label: memberName(person),
          }))}
          onChange={ownerUserId => changeFilter({ ownerUserId })}
        />
        <FilterSelect
          label={t('platformAccounts.filters.status')}
          value={draft.status}
          anyLabel={t('platformAccounts.filters.allStatuses')}
          options={PLATFORM_ACCOUNT_STATUSES.map(status => ({
            value: status,
            label: t(`platformAccounts.statuses.${status}`),
          }))}
          onChange={status => changeFilter({ status })}
        />
      </div>

      {ledger.isPending ? (
        <Skeleton className="h-48 w-full" aria-hidden="true" />
      ) : ledger.error || !ledger.data ? (
        <ErrorAlert
          message={t(resolveMailboxErrorKey(ledger.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void ledger.refetch()}
        />
      ) : items.length === 0 && page === 1 ? (
        <EmptyState
          icon={<AtSign className="size-5" aria-hidden="true" />}
          title={filtering ? t('platformAccounts.noMatches') : t('platformAccounts.emptyTitle')}
          description={filtering ? undefined : t('platformAccounts.empty')}
        />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('platformAccounts.columns.platform')}</TableHead>
                <TableHead>{t('platformAccounts.columns.handle')}</TableHead>
                <TableHead>{t('platformAccounts.columns.role')}</TableHead>
                <TableHead>{t('platformAccounts.columns.mailbox')}</TableHead>
                <TableHead>{t('platformAccounts.columns.owner')}</TableHead>
                <TableHead>{t('platformAccounts.columns.status')}</TableHead>
                <TableHead className="text-right">
                  {t('platformAccounts.columns.actions')}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map(account => (
                <AccountRow
                  key={account.id}
                  account={account}
                  lookups={lookups}
                  onAction={kind => setDialog({ kind, account })}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {/* One page needs no pager. */}
      {meta && meta.last_page > 1 ? (
        <div className="flex items-center justify-end gap-2">
          <span className="text-sm text-muted-foreground tabular-nums">
            {t('platformAccounts.pageOf', { page: meta.current_page, last: meta.last_page })}
          </span>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={page <= 1}
            onClick={() => setPage(current => Math.max(1, current - 1))}
          >
            {t('platformAccounts.previous')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={meta.current_page >= meta.last_page || ledger.isFetching}
            onClick={() => setPage(current => current + 1)}
          >
            {t('platformAccounts.next')}
          </Button>
        </div>
      ) : null}

      <PlatformAccountDrawer
        open={dialog?.kind === 'create' || dialog?.kind === 'edit'}
        account={dialog?.kind === 'edit' ? dialog.account : null}
        lookups={lookups}
        onClose={() => setDialog(null)}
      />
      <HandOverPlatformAccountsDialog
        open={dialog?.kind === 'handover'}
        initialFrom={draft.ownerUserId}
        formerOwnerIds={items.flatMap(account =>
          account.owner_user_id === null ? [] : [account.owner_user_id]
        )}
        lookups={lookups}
        onClose={() => setDialog(null)}
      />
      <DeleteAccountDialog
        account={dialog?.kind === 'delete' ? dialog.account : null}
        onClose={() => setDialog(null)}
        onDeleted={() => {
          // The page may now be empty; going back keeps the table useful.
          if (items.length === 1 && page > 1) setPage(current => current - 1);
        }}
      />
    </div>
  );
}

function FilterSelect({
  label,
  value,
  anyLabel,
  options,
  onChange,
}: {
  label: string;
  value: string;
  anyLabel: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
}) {
  return (
    <select
      aria-label={label}
      title={label}
      className={cn(nativeSelectClassName, 'w-full sm:w-auto sm:max-w-48')}
      value={value}
      onChange={event => onChange(event.target.value)}
    >
      <option value="">{anyLabel}</option>
      {options.map(option => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  );
}

const statusStyles: Record<PlatformAccountStatus, string> = {
  invited: 'border-transparent bg-info-subtle text-info',
  awaiting_confirmation: 'border-transparent bg-warning-subtle text-warning',
  active: 'border-transparent bg-success-subtle text-success',
  timed_out: 'border-transparent bg-error-subtle text-error',
  needs_review: 'border-transparent bg-warning-subtle text-warning',
  retired: 'border-transparent bg-muted text-muted-foreground',
};

export function PlatformAccountStatusBadge({ status }: { status: PlatformAccountStatus }) {
  const t = useT('mailbox');
  return (
    <Badge variant="outline" className={statusStyles[status]}>
      {t(`platformAccounts.statuses.${status}`)}
    </Badge>
  );
}

function AccountRow({
  account,
  lookups,
  onAction,
}: {
  account: PlatformAccount;
  lookups: PlatformAccountLookups;
  onAction: (kind: 'edit' | 'delete') => void;
}) {
  const t = useT('mailbox');
  const names = { platform: account.platform, handle: account.handle };
  const none = <span className="text-muted-foreground">{t('platformAccounts.none')}</span>;
  const role = account.role_id === null ? undefined : lookups.roleName(account.role_id);
  return (
    <TableRow>
      <TableCell className="font-medium">{account.platform}</TableCell>
      <TableCell>
        {account.profile_url ? (
          <a
            href={account.profile_url}
            target="_blank"
            rel="noopener noreferrer"
            className="hover:text-primary hover:underline"
          >
            {account.handle}
          </a>
        ) : (
          account.handle
        )}
      </TableCell>
      <TableCell>{role ?? none}</TableCell>
      <TableCell className={cn(account.mailbox_address && 'font-mono text-xs')}>
        {account.mailbox_address ??
          (account.mailbox_id === null ? (
            none
          ) : (
            <span className="text-muted-foreground">{t('platformAccounts.hiddenMailbox')}</span>
          ))}
      </TableCell>
      <TableCell>
        {account.owner_user_id === null ? none : lookups.nameOf(account.owner_user_id)}
      </TableCell>
      <TableCell>
        <PlatformAccountStatusBadge status={account.status} />
      </TableCell>
      <TableCell className="text-right">
        <div className="flex items-center justify-end gap-1">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            icon={<Pencil className="size-3.5" />}
            aria-label={t('platformAccounts.editOf', names)}
            onClick={() => onAction('edit')}
          >
            {t('platformAccounts.edit')}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                isIcon
                noScale
                aria-label={t('platformAccounts.moreActions', names)}
                title={t('platformAccounts.moreActions', names)}
              >
                <MoreHorizontal className="size-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem variant="destructive" onSelect={() => onAction('delete')}>
                <Trash2 />
                {t('platformAccounts.delete')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </TableCell>
    </TableRow>
  );
}

function DeleteAccountDialog({
  account,
  onClose,
  onDeleted,
}: {
  account: PlatformAccount | null;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const t = useT('mailbox');
  const remove = useDeletePlatformAccount(useMailOrganizationId());

  const confirm = async () => {
    if (!account) return;
    try {
      await remove.mutateAsync(account.id);
      toast.success(t('platformAccounts.deleteDialog.deleted'));
      onDeleted();
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <ConfirmDialog
      open={account !== null}
      title={t('platformAccounts.deleteDialog.title', {
        platform: account?.platform ?? '',
        handle: account?.handle ?? '',
      })}
      description={t('platformAccounts.deleteDialog.description')}
      confirmLabel={t('actions.delete')}
      cancelLabel={t('actions.cancel')}
      pending={remove.isPending}
      icon={<Trash2 className="size-4" />}
      onConfirm={() => void confirm()}
      onCancel={onClose}
    />
  );
}
