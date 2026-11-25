'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { KeyRound, MailCheck } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
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
  DialogTrigger,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ROUTES } from '@/constants/routes';
import { useAcceptOrganizationInvitation } from '@/features/organization/hooks/use-organizations';
import { acceptOrganizationInvitationSchema } from '@/features/organization/schemas';
import {
  hasOrganizationFieldError,
  resolveOrganizationErrorKey,
} from '@/features/organization/utils/organization-error';
import { clearPendingInvitation } from '@/features/organization/utils/pending-invitation';
import { useT } from '@/i18n';

/**
 * Joins an organization with the one-time token from an invitation mail. `initialToken`, left in
 * this tab by an invitation link, opens the dialog in a clean confirmation state without exposing
 * the raw secret token input. Once joined, the invitee starts in the organization's mail.
 */
export function AcceptOrganizationInvitationDialog({
  initialToken = null,
}: {
  initialToken?: string | null;
}) {
  const t = useT('organization');
  const common = useT('common');
  const router = useRouter();
  const mutation = useAcceptOrganizationInvitation();
  const [open, setOpen] = useState(initialToken !== null);
  const [token, setToken] = useState(initialToken ?? '');
  const [manualEntry, setManualEntry] = useState(initialToken === null);
  const [clientError, setClientError] = useState<string>();

  const reset = () => {
    clearPendingInvitation();
    setToken('');
    setManualEntry(true);
    setClientError(undefined);
    mutation.reset();
  };

  const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const parsed = acceptOrganizationInvitationSchema.safeParse({ token });
    if (!parsed.success) {
      setClientError(t('invitationTokenInvalid'));
      return;
    }
    setClientError(undefined);
    mutation.mutate(parsed.data, {
      onSuccess: (organization) => {
        toast.success(t('acceptInvitationSuccess'));
        setOpen(false);
        reset();
        router.push(`${ROUTES.CONSOLE.MAILBOXES}?org=${organization.id}`);
      },
    });
  };

  const tokenError =
    clientError ??
    (hasOrganizationFieldError(mutation.error, 'token')
      ? t('invitationTokenInvalid')
      : undefined);

  const hasError = Boolean(mutation.error || clientError);

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (!nextOpen) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button variant="outline" icon={<KeyRound className="size-4" />}>
          {t('acceptInvitation')}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('acceptInvitation')}</DialogTitle>
          <DialogDescription>
            {manualEntry
              ? t('acceptInvitationDescription')
              : t('acceptInvitationReadyDescription')}
          </DialogDescription>
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
            {!manualEntry && clientError ? (
              <Alert variant="destructive">
                <AlertDescription>{clientError}</AlertDescription>
              </Alert>
            ) : null}
            {manualEntry ? (
              <div className="space-y-2">
                <Label htmlFor="organization-invitation-token">
                  {t('invitationToken')}
                </Label>
                <Input
                  id="organization-invitation-token"
                  name="token"
                  type="password"
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  required
                  maxLength={256}
                  value={token}
                  errorText={tokenError}
                  disabled={mutation.isPending}
                  placeholder={t('invitationTokenPlaceholder')}
                  onChange={(event) => {
                    setToken(event.target.value);
                    setClientError(undefined);
                    mutation.reset();
                  }}
                />
              </div>
            ) : (
              <div className="space-y-3">
                <div className="flex items-start gap-3 rounded-lg border border-border/80 bg-muted/40 p-4">
                  <div className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
                    <MailCheck className="size-4" aria-hidden="true" />
                  </div>
                  <p className="text-sm leading-relaxed text-foreground">
                    {t('invitationReadyHint')}
                  </p>
                </div>
                {hasError ? (
                  <div className="flex justify-end">
                    <Button
                      type="button"
                      variant="link"
                      size="sm"
                      className="h-auto px-0 text-xs"
                      onClick={() => setManualEntry(true)}
                    >
                      {t('enterTokenManually')}
                    </Button>
                  </div>
                ) : null}
              </div>
            )}
          </DialogBody>
          <DialogFooter className="pt-5">
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={mutation.isPending}>
                {common('cancel')}
              </Button>
            </DialogClose>
            <Button type="submit" loading={mutation.isPending}>
              {manualEntry ? t('acceptInvitation') : t('confirmJoinOrganization')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
