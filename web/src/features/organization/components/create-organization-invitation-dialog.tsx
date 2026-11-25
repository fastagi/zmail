'use client';

import { useState } from 'react';
import { MailPlus } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { Mailbox, MailboxPermission } from '@/features/mailbox/types';
import { InvitationLinkPanel } from '@/features/organization/components/invitation-link-panel';
import { useCreateOrganizationInvitation } from '@/features/organization/hooks/use-organizations';
import { createOrganizationInvitationSchema } from '@/features/organization/schemas';
import type {
  InvitationEmailSendStatus,
  OrganizationInvitationRole,
} from '@/features/organization/types';
import {
  hasOrganizationFieldError,
  resolveOrganizationErrorKey,
} from '@/features/organization/utils/organization-error';
import { useT } from '@/i18n';

type MailboxAssignmentMode = 'none' | 'existing';

export function CreateOrganizationInvitationDialog({
  organizationId,
}: {
  organizationId: number;
}) {
  const t = useT('organization');
  const common = useT('common');
  const mutation = useCreateOrganizationInvitation(organizationId);
  const [open, setOpen] = useState(false);
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<OrganizationInvitationRole>('member');
  const [developer, setDeveloper] = useState(false);
  const [mailboxMode, setMailboxMode] = useState<MailboxAssignmentMode>('none');
  const [selectedMailbox, setSelectedMailbox] = useState<SelectedMailbox>();
  const [mailboxPermission, setMailboxPermission] = useState<MailboxPermission>('send');
  const [assigningMailbox, setAssigningMailbox] = useState(false);
  const [clientErrors, setClientErrors] = useState<Record<string, string>>({});
  // The one-time link lives only here, never in the query cache, and is dropped on close.
  const [issued, setIssued] = useState<IssuedInvitationLink>();

  const busy = mutation.isPending || assigningMailbox;

  const reset = () => {
    setEmail('');
    setRole('member');
    setDeveloper(false);
    setMailboxMode('none');
    setSelectedMailbox(undefined);
    setMailboxPermission('send');
    setAssigningMailbox(false);
    setClientErrors({});
    setIssued(undefined);
    mutation.reset();
  };

  const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const parsed = createOrganizationInvitationSchema.safeParse({
      email,
      role,
      // Admins have every feature; only a member invitation carries developer access.
      ...(role === 'member' && developer ? { developer: true } : {}),
    });
    const nextErrors: Record<string, string> = {};
    if (!parsed.success) {
      const invalidFields = new Set(
        parsed.error.issues.map((issue) => issue.path[0])
      );
      if (invalidFields.has('email')) nextErrors.email = t('emailInvalid');
    }
    if (mailboxMode === 'existing' && !selectedMailbox) {
      nextErrors.mailbox = t('invitationMailbox.required');
    }
    if (Object.keys(nextErrors).length > 0 || !parsed.success) {
      setClientErrors(nextErrors);
      return;
    }

    const targetMailbox = mailboxMode === 'existing' ? selectedMailbox : undefined;
    const targetPermission = mailboxPermission;

    setClientErrors({});
    mutation.mutate(parsed.data, {
      onSuccess: async ({ invitation, email_send_status: status, invitation_url: url }) => {
        let assignedMailbox: AssignedMailboxSummary | undefined;
        if (targetMailbox) {
          setAssigningMailbox(true);
          try {
            await mailboxService.setPendingMember(
              organizationId,
              targetMailbox.id,
              invitation.email,
              targetPermission
            );
            assignedMailbox = {
              address: targetMailbox.address,
              permission: targetPermission,
            };
          } catch {
            toast.warning(
              t('invitationMailbox.assignFailed', { address: targetMailbox.address })
            );
          } finally {
            setAssigningMailbox(false);
          }
        }
        if (url) {
          setIssued({ email: invitation.email, url, status, assignedMailbox });
          return;
        }
        toast.success(t('invitationCreated'), {
          description: assignedMailbox
            ? t('invitationMailbox.autoGrantHint', {
                address: assignedMailbox.address,
                permission: t(`invitationMailbox.permissions.${assignedMailbox.permission}`),
              })
            : t(`emailSendStatus.${status}`),
        });
        setOpen(false);
        reset();
      },
    });
  };

  const emailError =
    clientErrors.email ??
    (hasOrganizationFieldError(mutation.error, 'email')
      ? t('emailInvalid')
      : undefined);

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (!nextOpen) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button icon={<MailPlus className="size-4" />}>{t('invite')}</Button>
      </DialogTrigger>
      <DialogContent>
        {issued ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('invitationCreated')}</DialogTitle>
              <DialogDescription>
                {t('invitationLink.createdDescription', { email: issued.email })}
              </DialogDescription>
            </DialogHeader>
            <DialogBody className="space-y-4 py-1">
              <p className="text-sm text-muted-foreground">
                {t(`emailSendStatus.${issued.status}`)}
              </p>
              {issued.assignedMailbox ? (
                <p className="rounded-md border bg-muted/40 px-3 py-2 text-sm text-foreground">
                  {t('invitationMailbox.autoGrantHint', {
                    address: issued.assignedMailbox.address,
                    permission: t(
                      `invitationMailbox.permissions.${issued.assignedMailbox.permission}`
                    ),
                  })}
                </p>
              ) : null}
              <InvitationLinkPanel email={issued.email} url={issued.url} />
            </DialogBody>
            <DialogFooter className="pt-5">
              <DialogClose asChild>
                <Button type="button">{t('invitationLink.done')}</Button>
              </DialogClose>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t('invite')}</DialogTitle>
              <DialogDescription>{t('inviteDescription')}</DialogDescription>
            </DialogHeader>
            <form onSubmit={handleSubmit} aria-busy={busy}>
              <DialogBody className="space-y-4 py-1">
                {mutation.error ? (
                  <Alert variant="destructive">
                    <AlertDescription>
                      {t(resolveOrganizationErrorKey(mutation.error))}
                    </AlertDescription>
                  </Alert>
                ) : null}
                <div className="space-y-2">
                  <Label htmlFor="organization-invitation-email">{t('email')}</Label>
                  <Input
                    id="organization-invitation-email"
                    name="email"
                    type="email"
                    autoComplete="email"
                    autoCapitalize="none"
                    spellCheck={false}
                    required
                    maxLength={100}
                    value={email}
                    errorText={emailError}
                    disabled={busy}
                    placeholder={t('emailPlaceholder')}
                    onChange={(event) => {
                      setEmail(event.target.value);
                      setClientErrors((prev) => ({ ...prev, email: '' }));
                      mutation.reset();
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="organization-invitation-role">
                    {t('invitationRole')}
                  </Label>
                  <Select
                    value={role}
                    disabled={busy}
                    onValueChange={(value) => {
                      setRole(value as OrganizationInvitationRole);
                      mutation.reset();
                    }}
                  >
                    <SelectTrigger id="organization-invitation-role" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="member">{t('roles.member')}</SelectItem>
                      <SelectItem value="admin">{t('roles.admin')}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-3 rounded-md border p-3">
                  <div className="space-y-2">
                    <Label htmlFor="organization-invitation-mailbox-mode">
                      {t('invitationMailbox.label')}
                    </Label>
                    <Select
                      value={mailboxMode}
                      disabled={busy}
                      onValueChange={(value) => {
                        const next = value as MailboxAssignmentMode;
                        setMailboxMode(next);
                        if (next === 'none') setSelectedMailbox(undefined);
                        setClientErrors((prev) => {
                          const { mailbox: _removed, ...rest } = prev;
                          return rest;
                        });
                      }}
                    >
                      <SelectTrigger id="organization-invitation-mailbox-mode" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="none">{t('invitationMailbox.modeNone')}</SelectItem>
                        <SelectItem value="existing">
                          {t('invitationMailbox.modeExisting')}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  {mailboxMode === 'existing' ? (
                    <InviteExistingMailboxFields
                      organizationId={organizationId}
                      disabled={busy}
                      selectedMailbox={selectedMailbox}
                      permission={mailboxPermission}
                      error={clientErrors.mailbox}
                      onSelectMailbox={(mailbox) => {
                        setSelectedMailbox(mailbox);
                        setClientErrors((prev) => {
                          const { mailbox: _removed, ...rest } = prev;
                          return rest;
                        });
                      }}
                      onChangePermission={setMailboxPermission}
                    />
                  ) : null}
                </div>
                {role === 'member' ? (
                  <div className="flex items-start gap-2">
                    <Checkbox
                      id="organization-invitation-developer"
                      checked={developer}
                      disabled={busy}
                      onCheckedChange={checked => setDeveloper(checked === true)}
                    />
                    <div className="grid gap-1">
                      <Label htmlFor="organization-invitation-developer">
                        {t('invitationDeveloper')}
                      </Label>
                      <p className="text-xs text-muted-foreground">
                        {t('invitationDeveloperHint')}
                      </p>
                    </div>
                  </div>
                ) : null}
              </DialogBody>
              <DialogFooter className="pt-5">
                <DialogClose asChild>
                  <Button type="button" variant="outline" disabled={busy}>
                    {common('cancel')}
                  </Button>
                </DialogClose>
                <Button type="submit" loading={busy}>
                  {t('sendInvitation')}
                </Button>
              </DialogFooter>
            </form>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function InviteExistingMailboxFields({
  organizationId,
  disabled,
  selectedMailbox,
  permission,
  error,
  onSelectMailbox,
  onChangePermission,
}: {
  organizationId: number;
  disabled: boolean;
  selectedMailbox: SelectedMailbox | undefined;
  permission: MailboxPermission;
  error: string | undefined;
  onSelectMailbox: (mailbox: SelectedMailbox) => void;
  onChangePermission: (permission: MailboxPermission) => void;
}) {
  const t = useT('organization');
  const mailboxesQuery = useMailboxes(organizationId);
  const assignable = (mailboxesQuery.data ?? []).filter(
    (mailbox): mailbox is Mailbox => mailbox.type === 'shared' && mailbox.access.manage_members
  );

  if (mailboxesQuery.isPending) {
    return (
      <p className="text-xs text-muted-foreground">{t('invitationMailbox.loading')}</p>
    );
  }
  if (assignable.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">{t('invitationMailbox.empty')}</p>
    );
  }

  return (
    <div className="space-y-3">
      <div className="space-y-2">
        <Label htmlFor="organization-invitation-mailbox-select">
          {t('invitationMailbox.selectLabel')}
        </Label>
        <Select
          value={selectedMailbox ? String(selectedMailbox.id) : ''}
          disabled={disabled}
          onValueChange={(value) => {
            const found = assignable.find((item) => String(item.id) === value);
            if (found) onSelectMailbox({ id: found.id, address: found.address });
          }}
        >
          <SelectTrigger id="organization-invitation-mailbox-select" className="w-full">
            <SelectValue placeholder={t('invitationMailbox.selectPlaceholder')} />
          </SelectTrigger>
          <SelectContent>
            {assignable.map((mailbox) => (
              <SelectItem key={mailbox.id} value={String(mailbox.id)}>
                {mailbox.display_name
                  ? `${mailbox.display_name} (${mailbox.address})`
                  : mailbox.address}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {error ? <p className="text-xs text-destructive">{error}</p> : null}
      </div>
      <div className="space-y-2">
        <Label htmlFor="organization-invitation-mailbox-permission">
          {t('invitationMailbox.permissionLabel')}
        </Label>
        <Select
          value={permission}
          disabled={disabled}
          onValueChange={(value) => onChangePermission(value as MailboxPermission)}
        >
          <SelectTrigger id="organization-invitation-mailbox-permission" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="send">{t('invitationMailbox.permissions.send')}</SelectItem>
            <SelectItem value="manage">{t('invitationMailbox.permissions.manage')}</SelectItem>
            <SelectItem value="view">{t('invitationMailbox.permissions.view')}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {selectedMailbox ? (
        <p className="text-xs text-muted-foreground">
          {t('invitationMailbox.autoGrantHint', {
            address: selectedMailbox.address,
            permission: t(`invitationMailbox.permissions.${permission}`),
          })}
        </p>
      ) : null}
    </div>
  );
}

interface SelectedMailbox {
  id: number;
  address: string;
}

interface AssignedMailboxSummary {
  address: string;
  permission: MailboxPermission;
}

interface IssuedInvitationLink {
  email: string;
  url: string;
  status: InvitationEmailSendStatus;
  assignedMailbox?: AssignedMailboxSummary;
}
