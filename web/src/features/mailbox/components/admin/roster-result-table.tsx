'use client';

import Link from 'next/link';
import { MailCheck } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { getOrganizationRoute } from '@/constants/routes';
import {
  canEditPrefix,
  isTaken,
  needsAttention,
  noteTone,
  rowNote,
  statusVariant,
} from '@/features/mailbox/components/admin/roster-import-helpers';
import { useMemberWelcomeSetting } from '@/features/mailbox/hooks/use-member-welcome';
import type { MemberImportRowInput, MemberImportRowResult } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * Tells the admin before importing that new members get a welcome email, or that it is off, with
 * a link to the setting on the Invitations tab. Says nothing while the setting is unknown.
 */
export function WelcomePreviewNote({
  organizationId,
  count,
  onNavigate,
}: {
  organizationId: number;
  count: number;
  onNavigate: () => void;
}) {
  const t = useT('mailbox');
  const setting = useMemberWelcomeSetting(organizationId);
  if (!setting.data) return null;
  const enabled = setting.data.enabled;
  return (
    <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
      <MailCheck className="size-4 shrink-0" aria-hidden="true" />
      <span className="min-w-0">
        {enabled
          ? t('admin.rosterImport.welcomePreview', { count })
          : t('admin.rosterImport.welcomePreviewOff')}
      </span>
      <Link
        href={`${getOrganizationRoute(organizationId)}?tab=invitations`}
        className="font-medium text-foreground underline underline-offset-4"
        onClick={onNavigate}
      >
        {enabled
          ? t('admin.rosterImport.welcomeSettingsLink')
          : t('admin.rosterImport.welcomeSettingsLinkOff')}
      </Link>
    </p>
  );
}

/** Why a provisioned row's welcome email did not go out; nothing when it did or was off. */
function WelcomeNote({ row, className }: { row: MemberImportRowResult; className?: string }) {
  const t = useT('mailbox');
  if (row.welcome_mail_status !== 'failed' && row.welcome_mail_status !== 'suppressed') return null;
  return (
    <p className={cn('text-warning', className)}>
      {t(`admin.rosterImport.welcomeNotes.${row.welcome_mail_status}`)}
    </p>
  );
}

export function RosterResultTable({
  rows,
  inputs,
  edits,
  disabled,
  onEdit,
}: {
  rows: MemberImportRowResult[];
  inputs: MemberImportRowInput[];
  edits: Record<number, string>;
  disabled: boolean;
  onEdit: (index: number, value: string) => void;
}) {
  const t = useT('mailbox');
  const roleOf = (row: MemberImportRowResult) => {
    const raw = (inputs[row.row - 1]?.role ?? '').trim();
    const role = raw.toLowerCase();
    if (role === '' || role === 'member') return t('admin.rosterImport.roles.member');
    if (role === 'admin') return t('admin.rosterImport.roles.admin');
    return raw;
  };
  const statusBadge = (row: MemberImportRowResult) => {
    const taken = isTaken(row);
    return (
      <Badge
        variant={taken ? 'outline' : statusVariant(row.status)}
        className={cn(taken && 'border-warning/40 bg-warning-subtle text-warning')}
      >
        {t(taken ? 'admin.rosterImport.status.taken' : `admin.rosterImport.status.${row.status}`)}
      </Badge>
    );
  };
  return (
    <div className="max-h-80 overflow-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-12">{t('admin.rosterImport.columns.row')}</TableHead>
            <TableHead>{t('admin.rosterImport.columns.email')}</TableHead>
            <TableHead className="hidden sm:table-cell">
              {t('admin.rosterImport.columns.role')}
            </TableHead>
            <TableHead className="hidden sm:table-cell">
              {t('admin.rosterImport.columns.address')}
            </TableHead>
            <TableHead className="hidden sm:table-cell">
              {t('admin.rosterImport.columns.status')}
            </TableHead>
            <TableHead className="hidden sm:table-cell">
              {t('admin.rosterImport.columns.note')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map(row => {
            const index = row.row - 1;
            return (
              <TableRow
                key={row.row}
                data-status={row.status}
                data-attention={needsAttention(row) ? 'true' : 'false'}
                className={cn(
                  row.status === 'failed' && 'bg-destructive/5 hover:bg-destructive/10',
                  isTaken(row) && 'bg-warning-subtle hover:bg-warning-subtle'
                )}
              >
                <TableCell className="align-top tabular-nums text-muted-foreground">
                  {row.row}
                </TableCell>
                <TableCell className="max-w-56 whitespace-normal align-top" title={row.email}>
                  <span className="break-all">{row.email || '—'}</span>
                  {/* On a phone the other columns stack here instead of scrolling sideways. */}
                  <div className="mt-1 space-y-1 sm:hidden">
                    <p className="text-xs text-muted-foreground">{roleOf(row)}</p>
                    {row.address ? (
                      <p className="break-all text-xs text-muted-foreground">{row.address}</p>
                    ) : null}
                    {statusBadge(row)}
                    <p className={cn('text-xs', noteTone(row))}>{rowNote(t, row)}</p>
                    <WelcomeNote row={row} className="text-xs" />
                  </div>
                  {canEditPrefix(row) ? (
                    <Input
                      className="mt-2 h-8 max-w-48 border-warning/40 text-sm dark:bg-background"
                      aria-label={t('admin.rosterImport.newPrefixLabel', { email: row.email })}
                      placeholder={t('admin.rosterImport.newPrefixPlaceholder')}
                      value={edits[index] ?? ''}
                      disabled={disabled}
                      spellCheck={false}
                      autoCapitalize="off"
                      onChange={event => onEdit(index, event.target.value)}
                    />
                  ) : null}
                </TableCell>
                <TableCell className="hidden align-top text-sm sm:table-cell">
                  {roleOf(row)}
                </TableCell>
                <TableCell
                  className="hidden max-w-48 truncate align-top sm:table-cell"
                  title={row.address}
                >
                  {row.address || '—'}
                </TableCell>
                <TableCell className="hidden align-top sm:table-cell">{statusBadge(row)}</TableCell>
                <TableCell
                  className={cn(
                    'hidden min-w-48 whitespace-normal align-top text-sm sm:table-cell',
                    noteTone(row)
                  )}
                >
                  {rowNote(t, row)}
                  <WelcomeNote row={row} className="mt-1" />
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
