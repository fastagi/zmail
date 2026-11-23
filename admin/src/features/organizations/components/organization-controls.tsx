import { CircleCheck, Ban, Gauge } from 'lucide-react';
import { type FormEvent, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { SectionCard } from '@/components/layout/section-card';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useOrganizationControl } from '@/features/organizations/hooks/use-organizations';
import { parseSendLimit } from '@/features/organizations/send-limit';
import {
  maxSuspensionReasonLength,
  type OperatorOrganization,
} from '@/features/organizations/types';
import { ApiError } from '@/http/client';
import { ApiErrorCode } from '@/http/codes';
import { cn } from '@/lib/cn';

function controlErrorKey(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.errorCode === ApiErrorCode.ORGANIZATION_NOT_FOUND) {
      return 'organizations.errors.notFound';
    }
    if (error.status === 422 || error.status === 400) {
      return 'organizations.errors.rejected';
    }
  }
  return 'organizations.errors.unavailable';
}

export function OrganizationControls({ organization }: { organization: OperatorOrganization }) {
  const { t } = useTranslation();
  const control = useOrganizationControl(organization.id);
  const limitControl = useOrganizationControl(organization.id);
  const [confirming, setConfirming] = useState(false);
  const [reason, setReason] = useState('');
  const [reasonTouched, setReasonTouched] = useState(false);
  const [limit, setLimit] = useState(organization.daily_send_limit?.toString() ?? '');
  const [limitTouched, setLimitTouched] = useState(false);
  const reasonId = useId();
  const reasonHintId = useId();
  const limitId = useId();
  const limitHintId = useId();
  const suspended = organization.suspended_at !== null;
  const action = suspended ? 'restore' : 'suspend';
  const trimmedReason = reason.trim();
  const reasonInvalid =
    trimmedReason === '' || [...trimmedReason].length > maxSuspensionReasonLength;
  const parsedLimit = parseSendLimit(limit);
  const limitUnchanged = parsedLimit === organization.daily_send_limit;

  const closeDialog = () => {
    setConfirming(false);
    setReason('');
    setReasonTouched(false);
    control.reset();
  };

  const confirm = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (action === 'suspend' && reasonInvalid) {
      setReasonTouched(true);
      return;
    }
    control.mutate(action === 'suspend' ? { action, reason: trimmedReason } : { action }, {
      onSuccess: (updated) => {
        toast.success(t(`organizations.controls.done.${action}`, { name: updated.name }));
        closeDialog();
      },
    });
  };

  const saveLimit = (next: number | null) => {
    limitControl.mutate(
      { action: 'setSendLimit', dailySendLimit: next },
      {
        onSuccess: (updated) => {
          setLimit(updated.daily_send_limit?.toString() ?? '');
          setLimitTouched(false);
          toast.success(t('organizations.controls.done.setSendLimit', { name: updated.name }));
        },
      },
    );
  };

  const submitLimit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setLimitTouched(true);
    if (parsedLimit === undefined || limitUnchanged) {
      return;
    }
    saveLimit(parsedLimit);
  };

  return (
    <SectionCard
      icon={Gauge}
      titleId="organization-controls-title"
      title={t('organizations.controls.title')}
      description={t('organizations.controls.description')}
    >
      <div className="grid gap-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="grid gap-1">
            <p className="text-sm font-medium">{t('organizations.controls.statusLabel')}</p>
            <p className="text-xs text-muted-foreground">
              {t(
                suspended
                  ? 'organizations.controls.restoreHint'
                  : 'organizations.controls.suspendHint',
              )}
            </p>
          </div>
          <Button
            type="button"
            variant={suspended ? 'default' : 'destructive'}
            onClick={() => setConfirming(true)}
          >
            {suspended ? <CircleCheck aria-hidden="true" /> : <Ban aria-hidden="true" />}
            {t(`organizations.controls.${action}`)}
          </Button>
        </div>

        <form onSubmit={submitLimit} className="grid gap-1.5" noValidate>
          <Label htmlFor={limitId}>{t('organizations.controls.limitLabel')}</Label>
          <div className="flex flex-wrap items-center gap-2">
            <Input
              id={limitId}
              name="daily_send_limit"
              inputMode="numeric"
              autoComplete="off"
              className="w-48"
              placeholder={t('organizations.controls.limitPlaceholder')}
              value={limit}
              aria-describedby={limitHintId}
              aria-invalid={limitTouched && parsedLimit === undefined ? true : undefined}
              onChange={(event) => {
                setLimit(event.target.value);
                limitControl.reset();
              }}
            />
            <Button
              type="submit"
              variant="outline"
              disabled={limitControl.isPending || limitUnchanged}
            >
              {t('organizations.controls.saveLimit')}
            </Button>
            {organization.daily_send_limit !== null ? (
              <Button
                type="button"
                variant="ghost"
                disabled={limitControl.isPending}
                onClick={() => saveLimit(null)}
              >
                {t('organizations.controls.clearLimit')}
              </Button>
            ) : null}
          </div>
          <p
            id={limitHintId}
            role={limitTouched && parsedLimit === undefined ? 'alert' : undefined}
            className={cn(
              'text-xs',
              limitTouched && parsedLimit === undefined
                ? 'text-destructive'
                : 'text-muted-foreground',
            )}
          >
            {limitTouched && parsedLimit === undefined
              ? t('organizations.errors.limitInvalid')
              : t('organizations.controls.limitHint')}
          </p>
          {limitControl.isError ? (
            <p role="alert" className="text-xs text-destructive">
              {t(controlErrorKey(limitControl.error))}
            </p>
          ) : null}
        </form>
      </div>

      <Dialog
        open={confirming}
        onOpenChange={(open) => (open ? setConfirming(true) : closeDialog())}
      >
        <DialogContent>
          <form onSubmit={confirm} className="grid gap-4" noValidate>
            <DialogTitle>
              {t(`organizations.controls.confirm.${action}.title`, { name: organization.name })}
            </DialogTitle>
            <DialogDescription>
              {t(`organizations.controls.confirm.${action}.description`)}
            </DialogDescription>
            {action === 'suspend' ? (
              <div className="grid gap-1.5">
                <Label htmlFor={reasonId}>{t('organizations.controls.reasonLabel')}</Label>
                <textarea
                  id={reasonId}
                  name="reason"
                  rows={3}
                  maxLength={maxSuspensionReasonLength}
                  value={reason}
                  placeholder={t('organizations.controls.reasonPlaceholder')}
                  aria-describedby={reasonTouched && reasonInvalid ? reasonHintId : undefined}
                  aria-invalid={reasonTouched && reasonInvalid ? true : undefined}
                  onChange={(event) => setReason(event.target.value)}
                  className={cn(
                    'w-full rounded-md border border-input bg-transparent px-3 py-2 text-base shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring md:text-sm',
                    reasonTouched && reasonInvalid ? 'border-destructive' : undefined,
                  )}
                />
                {reasonTouched && reasonInvalid ? (
                  <p id={reasonHintId} role="alert" className="text-xs text-destructive">
                    {t('organizations.errors.reasonRequired')}
                  </p>
                ) : null}
              </div>
            ) : null}
            {control.isError ? (
              <p role="alert" className="text-sm text-destructive">
                {t(controlErrorKey(control.error))}
              </p>
            ) : null}
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline">
                  {t('organizations.controls.cancel')}
                </Button>
              </DialogClose>
              <Button
                type="submit"
                variant={action === 'restore' ? 'default' : 'destructive'}
                disabled={control.isPending}
              >
                {t(`organizations.controls.${action}`)}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </SectionCard>
  );
}
