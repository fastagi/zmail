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
  const [clientErrors, setClientErrors] = useState<Record<string, string>>({});
  // The one-time link lives only here, never in the query cache, and is dropped on close.
  const [issued, setIssued] = useState<IssuedInvitationLink>();

  const reset = () => {
    setEmail('');
    setRole('member');
    setDeveloper(false);
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
    if (!parsed.success) {
      const invalidFields = new Set(
        parsed.error.issues.map((issue) => issue.path[0])
      );
      setClientErrors({
        ...(invalidFields.has('email') ? { email: t('emailInvalid') } : {}),
      });
      return;
    }

    setClientErrors({});
    mutation.mutate(parsed.data, {
      onSuccess: ({ invitation, email_send_status: status, invitation_url: url }) => {
        if (url) {
          setIssued({ email: invitation.email, url, status });
          return;
        }
        toast.success(t('invitationCreated'), {
          description: t(`emailSendStatus.${status}`),
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
            <form onSubmit={handleSubmit} aria-busy={mutation.isPending}>
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
                    disabled={mutation.isPending}
                    placeholder={t('emailPlaceholder')}
                    onChange={(event) => {
                      setEmail(event.target.value);
                      setClientErrors({});
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
                    disabled={mutation.isPending}
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
                {role === 'member' ? (
                  <div className="flex items-start gap-2">
                    <Checkbox
                      id="organization-invitation-developer"
                      checked={developer}
                      disabled={mutation.isPending}
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
                  <Button type="button" variant="outline" disabled={mutation.isPending}>
                    {common('cancel')}
                  </Button>
                </DialogClose>
                <Button type="submit" loading={mutation.isPending}>
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

interface IssuedInvitationLink {
  email: string;
  url: string;
  status: InvitationEmailSendStatus;
}
