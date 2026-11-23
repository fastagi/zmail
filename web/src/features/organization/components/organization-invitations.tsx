'use client';

import { useEffect, useState } from 'react';
import { useLocale } from 'next-intl';
import { AlertCircle, RefreshCw, RotateCcw, Send, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { CreateOrganizationInvitationDialog } from '@/features/organization/components/create-organization-invitation-dialog';
import { InvitationLinkPanel } from '@/features/organization/components/invitation-link-panel';
import {
  useCreateOrganizationInvitation,
  useOrganizationInvitations,
  useResendOrganizationInvitation,
  useRevokeOrganizationInvitation,
} from '@/features/organization/hooks/use-organizations';
import type {
  InvitationEmailSendStatus,
  OrganizationInvitation,
  OrganizationInvitationStatus,
} from '@/features/organization/types';
import { formatOrganizationDate } from '@/features/organization/utils/format-organization-date';
import { resolveOrganizationErrorKey } from '@/features/organization/utils/organization-error';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';

export function OrganizationInvitations({ organizationId }: { organizationId: number }) {
  const t = useT('organization');
  const common = useT('common');
  const locale = useLocale();
  const query = useOrganizationInvitations(organizationId);
  const mutation = useRevokeOrganizationInvitation(organizationId);
  const resend = useResendOrganizationInvitation(organizationId);
  const reinvite = useCreateOrganizationInvitation(organizationId);
  const cooldown = useResendCooldowns();
  const overview = summarizeInvitations(query.data?.items ?? []);
  // The one-time link of the latest resend; component state only, dropped when its dialog closes.
  const [reissued, setReissued] = useState<ReissuedInvitationLink>();

  const resendInvitation = (invitation: OrganizationInvitation) => {
    resend.mutate(invitation.id, {
      onSuccess: ({ email_send_status: status, invitation_url: url }) => {
        cooldown.start(invitation.id, RESEND_COOLDOWN_SECONDS);
        if (url) {
          setReissued({ email: invitation.email, url, status });
          return;
        }
        const notify = status === 'accepted_by_provider' ? toast.success : toast.warning;
        notify(t(`emailSendStatus.${status}`));
      },
      onError: error => {
        if (error instanceof ApiError && error.retryAfterSeconds) {
          cooldown.start(invitation.id, error.retryAfterSeconds);
        }
        toast.error(t(resolveOrganizationErrorKey(error)));
      },
    });
  };
  const [selected, setSelected] = useState<OrganizationInvitation>();

  // An expired or revoked invitation cannot be resent; a new one goes to the same person instead.
  const inviteAgain = (invitation: OrganizationInvitation) => {
    reinvite.mutate(
      { email: invitation.email, role: invitation.role },
      {
        onSuccess: ({ email_send_status: status, invitation_url: url }) => {
          if (url) {
            setReissued({ email: invitation.email, url, status });
            return;
          }
          if (status === 'accepted_by_provider') {
            toast.success(t('reinviteSent', { email: invitation.email }));
          } else {
            toast.warning(t(`emailSendStatus.${status}`));
          }
        },
        onError: error => toast.error(t(resolveOrganizationErrorKey(error))),
      }
    );
  };

  const revoke = () => {
    if (!selected) return;
    mutation.mutate(selected.id, {
      onSuccess: () => {
        toast.success(t('revokeInvitationSuccess'));
        setSelected(undefined);
      },
    });
  };

  return (
    <section className="py-6" aria-labelledby="organization-invitations-heading">
      <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 id="organization-invitations-heading" className="text-base font-semibold">
            {t('invitationsTitle')}
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">{t('invitationsDescription')}</p>
        </div>
        <CreateOrganizationInvitationDialog organizationId={organizationId} />
      </div>

      {overview.total > 0 ? (
        <ul
          aria-label={t('summaryLabel')}
          className="mb-4 flex flex-wrap gap-2 text-sm"
        >
          <SummaryChip tone="success">{t('summaryJoined', { count: overview.joined })}</SummaryChip>
          <SummaryChip tone="warning">{t('summaryWaiting', { count: overview.waiting })}</SummaryChip>
          {overview.expired > 0 ? (
            <SummaryChip tone="muted">{t('summaryExpired', { count: overview.expired })}</SummaryChip>
          ) : null}
        </ul>
      ) : null}

      {query.isPending ? <InvitationTableSkeleton /> : null}
      {query.error ? (
        <Alert variant="destructive">
          <AlertCircle aria-hidden="true" className="size-4" />
          <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
            <span>{t(resolveOrganizationErrorKey(query.error))}</span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<RefreshCw className="size-3.5" />}
              onClick={() => query.refetch()}
            >
              {t('retry')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}
      {query.data?.items.length === 0 ? (
        <div className="border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
          {t('noInvitations')}
        </div>
      ) : null}
      {query.data && query.data.items.length > 0 ? (
        <div className="overflow-x-auto rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('email')}</TableHead>
                <TableHead>{t('role')}</TableHead>
                <TableHead>{t('status')}</TableHead>
                <TableHead className="hidden lg:table-cell">{t('expiresAt')}</TableHead>
                <TableHead className="w-32 text-right">
                  <span className="sr-only">{t('actions')}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {overview.ordered.map(invitation => (
                <TableRow key={invitation.id}>
                  <TableCell className="font-medium">{invitation.email}</TableCell>
                  <TableCell>{t(`roles.${invitation.role}`)}</TableCell>
                  <TableCell>
                    <InvitationStatusBadge status={invitation.status} />
                    {invitation.status === 'pending' ? (
                      <>
                        <InvitationEmailState invitation={invitation} />
                        <p className="mt-1 max-w-[20rem] text-xs text-muted-foreground">
                          {invitation.email_send_status === 'accepted_by_provider' ||
                          invitation.email_send_status === 'failed'
                            ? t('invitationLink.rowHint')
                            : t('invitationLink.rowHintWithoutEmail')}
                        </p>
                      </>
                    ) : null}
                  </TableCell>
                  <TableCell className="hidden text-sm text-muted-foreground lg:table-cell">
                    {formatOrganizationDate(invitation.expires_at, locale)}
                  </TableCell>
                  <TableCell className="text-right">
                    {invitation.status === 'pending' ? (
                      <div className="flex justify-end gap-1">
                        <Button
                          type="button"
                          size="sm"
                          variant="ghost"
                          icon={<Send className="size-3.5" />}
                          aria-label={t('resendInvitationLabel', { email: invitation.email })}
                          loading={resend.isPending && resend.variables === invitation.id}
                          disabled={resend.isPending || cooldown.remaining(invitation.id) > 0}
                          onClick={() => resendInvitation(invitation)}
                        >
                          {cooldown.remaining(invitation.id) > 0
                            ? t('resendInvitationCooldown', {
                                seconds: cooldown.remaining(invitation.id),
                              })
                            : t('resendInvitation')}
                        </Button>
                        <Button
                          type="button"
                          size="sm"
                          variant="ghost"
                          icon={<Trash2 className="size-3.5" />}
                          onClick={() => {
                            mutation.reset();
                            setSelected(invitation);
                          }}
                        >
                          {t('revokeInvitation')}
                        </Button>
                      </div>
                    ) : overview.canInviteAgain(invitation) ? (
                      <div className="flex justify-end">
                        <Button
                          type="button"
                          size="sm"
                          variant="ghost"
                          icon={<RotateCcw className="size-3.5" />}
                          aria-label={t('reinviteLabel', { email: invitation.email })}
                          loading={
                            reinvite.isPending &&
                            (reinvite.variables as { email?: string } | undefined)?.email ===
                              invitation.email
                          }
                          disabled={reinvite.isPending}
                          onClick={() => inviteAgain(invitation)}
                        >
                          {t('reinvite')}
                        </Button>
                      </div>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}

      <Dialog
        open={Boolean(reissued)}
        onOpenChange={open => {
          if (open) return;
          setReissued(undefined);
          resend.reset();
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('invitationLink.resentTitle')}</DialogTitle>
            <DialogDescription>
              {reissued ? t('invitationLink.resentDescription', { email: reissued.email }) : null}
            </DialogDescription>
          </DialogHeader>
          {reissued ? (
            <DialogBody className="space-y-4">
              <p className="text-sm text-muted-foreground">
                {t(`emailSendStatus.${reissued.status}`)}
              </p>
              <InvitationLinkPanel email={reissued.email} url={reissued.url} />
            </DialogBody>
          ) : null}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button">{t('invitationLink.done')}</Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={Boolean(selected)}
        onOpenChange={open => {
          if (!open && !mutation.isPending) {
            setSelected(undefined);
            mutation.reset();
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('revokeInvitationTitle')}</DialogTitle>
            <DialogDescription>{t('revokeInvitationDescription')}</DialogDescription>
          </DialogHeader>
          <DialogBody>
            {mutation.error ? (
              <Alert variant="destructive">
                <AlertDescription>
                  {t(resolveOrganizationErrorKey(mutation.error))}
                </AlertDescription>
              </Alert>
            ) : null}
            <p className="text-sm font-medium text-foreground">{selected?.email}</p>
          </DialogBody>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={mutation.isPending}>
                {common('cancel')}
              </Button>
            </DialogClose>
            <Button
              type="button"
              variant="destructive"
              loading={mutation.isPending}
              onClick={revoke}
            >
              {t('revokeInvitation')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

const statusOrder: Record<OrganizationInvitationStatus, number> = {
  pending: 0,
  expired: 1,
  accepted: 2,
  revoked: 3,
};

/**
 * Where each invited person stands, from their latest invitation: joined, still waiting, or
 * expired. An older invitation to someone invited again since is history and counts for nothing.
 * Rows list people still waiting first, newest first within a status.
 */
export function summarizeInvitations(items: OrganizationInvitation[]) {
  const newest = (a: OrganizationInvitation, b: OrganizationInvitation) =>
    b.created_at.localeCompare(a.created_at) || b.id - a.id;
  const latest = new Map<string, OrganizationInvitation>();
  for (const item of [...items].sort(newest)) {
    if (!latest.has(item.email)) latest.set(item.email, item);
  }
  const current = [...latest.values()];
  const count = (status: OrganizationInvitationStatus) =>
    current.filter(item => item.status === status).length;
  return {
    total: items.length,
    joined: count('accepted'),
    waiting: count('pending'),
    expired: count('expired'),
    ordered: [...items].sort(
      (a, b) => statusOrder[a.status] - statusOrder[b.status] || newest(a, b)
    ),
    canInviteAgain: (item: OrganizationInvitation) =>
      (item.status === 'expired' || item.status === 'revoked') &&
      latest.get(item.email)?.id === item.id,
  };
}

function SummaryChip({
  tone,
  children,
}: {
  tone: 'success' | 'warning' | 'muted';
  children: React.ReactNode;
}) {
  return (
    <li
      className={
        tone === 'success'
          ? 'rounded-full bg-success-subtle px-2.5 py-0.5 text-success'
          : tone === 'warning'
            ? 'rounded-full bg-warning-subtle px-2.5 py-0.5 text-warning'
            : 'rounded-full bg-muted px-2.5 py-0.5 text-muted-foreground'
      }
    >
      {children}
    </li>
  );
}

function InvitationStatusBadge({ status }: { status: OrganizationInvitationStatus }) {
  const t = useT('organization');
  const variant = status === 'pending' ? 'default' : status === 'expired' ? 'outline' : 'secondary';
  return <Badge variant={variant}>{t(`invitationStatus.${status}`)}</Badge>;
}

function InvitationEmailState({ invitation }: { invitation: OrganizationInvitation }) {
  const t = useT('organization');
  const status = invitation.email_send_status;
  if (!status) return null;
  if (status === 'accepted_by_provider') {
    return (
      <p className="mt-1 text-xs text-muted-foreground">
        {t('emailDelivery.accepted_by_provider')}
      </p>
    );
  }
  return (
    <p className="mt-1 text-xs text-error" title={t(`emailDeliveryHint.${status}`)}>
      {t(`emailDelivery.${status}`)}
      <span className="sr-only"> {t(`emailDeliveryHint.${status}`)}</span>
    </p>
  );
}

function InvitationTableSkeleton() {
  return (
    <div className="space-y-3 rounded-md border p-4" aria-hidden="true">
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-10 w-full" />
      <Skeleton className="h-10 w-full" />
    </div>
  );
}

interface ReissuedInvitationLink {
  email: string;
  url: string;
  status: InvitationEmailSendStatus;
}

// Each invitation may be resent once a minute; a 429's Retry-After replaces this wait.
const RESEND_COOLDOWN_SECONDS = 60;

/** Per-invitation resend cooldowns that count down once a second while any is active. */
function useResendCooldowns() {
  const [availableAt, setAvailableAt] = useState<Record<number, number>>({});
  const [now, setNow] = useState(() => Date.now());
  const latest = Math.max(0, ...Object.values(availableAt));

  useEffect(() => {
    if (latest <= Date.now()) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [latest]);

  return {
    start(invitationId: number, seconds: number) {
      const current = Date.now();
      setNow(current);
      setAvailableAt(previous => ({ ...previous, [invitationId]: current + seconds * 1000 }));
    },
    remaining(invitationId: number): number {
      return Math.max(0, Math.ceil(((availableAt[invitationId] ?? 0) - now) / 1000));
    },
  };
}
