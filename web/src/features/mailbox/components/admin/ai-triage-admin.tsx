'use client';

import { useId, useState, type FormEvent } from 'react';
import { AlertCircle, Info, ShieldAlert } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import { ErrorAlert, useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import {
  useMailTriageSetting,
  useSaveMailTriageSetting,
} from '@/features/mailbox/hooks/use-mail-triage';
import {
  MAIL_TRIAGE_DAILY_CAP_MAX,
  type MailTriageSetting,
} from '@/features/mailbox/triage-schemas';
import {
  isOrganizationLost,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/**
 * Admin page of the organization's AI triage switches: triage itself, the daily message cap, and
 * reply drafts. Without an instance model it says so instead of offering switches. The admin area
 * admits owners and admins only; the API remains the authority for every change.
 */
export function AiTriageAdmin() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader
        title={t('triageAdmin.title')}
        description={t('triageAdmin.description')}
        scope={scope}
      />
      <MailOrganizationBoundary scope={scope}>{() => <TriageSettings />}</MailOrganizationBoundary>
    </div>
  );
}

function TriageSettings() {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const setting = useMailTriageSetting(organizationId);

  if (isOrganizationLost(setting.error)) return <OrganizationLostState />;
  if (setting.isPending) return <Skeleton className="h-64 w-full" aria-hidden="true" />;
  if (setting.error || !setting.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(setting.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void setting.refetch()}
      />
    );
  }
  if (!setting.data.available) {
    return (
      <Alert role="status">
        <ShieldAlert className="size-4" aria-hidden="true" />
        <AlertTitle>{t('triageAdmin.unavailableTitle')}</AlertTitle>
        <AlertDescription>{t('triageAdmin.unavailable')}</AlertDescription>
      </Alert>
    );
  }
  // Remount after a save so the form starts again from the stored switches.
  return <TriageForm key={setting.data.updated_at ?? 'defaults'} setting={setting.data} />;
}

/** The cap as typed, or null when it is not a whole number in range. */
function parseCap(raw: string): number | null {
  if (!/^\d{1,6}$/u.test(raw.trim())) return null;
  const value = Number(raw.trim());
  return value >= 1 && value <= MAIL_TRIAGE_DAILY_CAP_MAX ? value : null;
}

function TriageForm({ setting }: { setting: MailTriageSetting }) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const organizationId = useMailOrganizationId();
  const save = useSaveMailTriageSetting(organizationId);
  const ids = {
    enabled: useId(),
    enabledHint: useId(),
    cap: useId(),
    capHint: useId(),
    capError: useId(),
    drafts: useId(),
    draftsHint: useId(),
  };
  const [enabled, setEnabled] = useState(setting.enabled);
  const [draftEnabled, setDraftEnabled] = useState(setting.draft_enabled);
  const [capText, setCapText] = useState(String(setting.daily_message_cap));
  const [failure, setFailure] = useState<MailboxErrorKey | null>(null);

  const cap = parseCap(capText);
  const changed =
    enabled !== setting.enabled ||
    draftEnabled !== setting.draft_enabled ||
    cap !== setting.daily_message_cap;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (cap === null || !changed) return;
    setFailure(null);
    try {
      await save.mutateAsync({ enabled, daily_message_cap: cap, draft_enabled: draftEnabled });
      toast.success(t('triageAdmin.saved'));
    } catch (error) {
      setFailure(resolveMailboxErrorKey(error));
    }
  };

  return (
    <form className="max-w-2xl space-y-5" onSubmit={event => void submit(event)} noValidate>
      {failure ? (
        <Alert variant="destructive" role="alert">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>
            {t(failure === 'errors.triageUnavailable' ? 'triageAdmin.unavailableSave' : failure)}
          </AlertDescription>
        </Alert>
      ) : null}

      <section className="flex items-start justify-between gap-4 rounded-md border p-4">
        <div className="min-w-0 space-y-1">
          <Label htmlFor={ids.enabled}>{t('triageAdmin.enabled')}</Label>
          <p id={ids.enabledHint} className="text-sm text-muted-foreground">
            {t('triageAdmin.enabledHint')}
          </p>
        </div>
        <Switch
          id={ids.enabled}
          aria-describedby={ids.enabledHint}
          checked={enabled}
          disabled={save.isPending}
          onCheckedChange={setEnabled}
        />
      </section>

      <section className="space-y-2 rounded-md border p-4">
        <Label htmlFor={ids.cap}>{t('triageAdmin.cap')}</Label>
        <Input
          id={ids.cap}
          type="number"
          inputMode="numeric"
          min={1}
          max={MAIL_TRIAGE_DAILY_CAP_MAX}
          step={1}
          className="max-w-40"
          value={capText}
          disabled={save.isPending}
          aria-invalid={cap === null}
          aria-describedby={cap === null ? `${ids.capHint} ${ids.capError}` : ids.capHint}
          onChange={event => setCapText(event.target.value)}
        />
        <p id={ids.capHint} className="text-sm text-muted-foreground">
          {t('triageAdmin.capHint', { instance: setting.instance_daily_cap })}
        </p>
        {cap === null ? (
          <p id={ids.capError} className="text-sm text-error">
            {t('triageAdmin.capInvalid', { max: MAIL_TRIAGE_DAILY_CAP_MAX })}
          </p>
        ) : null}
      </section>

      <section className="flex items-start justify-between gap-4 rounded-md border p-4">
        <div className="min-w-0 space-y-1">
          <Label htmlFor={ids.drafts}>{t('triageAdmin.drafts')}</Label>
          <p id={ids.draftsHint} className="text-sm text-muted-foreground">
            {t('triageAdmin.draftsHint')}
          </p>
        </div>
        <Switch
          id={ids.drafts}
          aria-describedby={ids.draftsHint}
          checked={draftEnabled}
          disabled={save.isPending}
          onCheckedChange={setDraftEnabled}
        />
      </section>

      <Alert role="note">
        <Info className="size-4" aria-hidden="true" />
        <AlertTitle>{t('triageAdmin.dataNoticeTitle')}</AlertTitle>
        <AlertDescription>{t('triageAdmin.dataNotice')}</AlertDescription>
      </Alert>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          {setting.updated_at === null
            ? t('triageAdmin.neverSaved')
            : t('triageAdmin.updatedAt', {
                date: formatter.format(new Date(setting.updated_at)),
              })}
        </p>
        <Button type="submit" disabled={cap === null || !changed} loading={save.isPending}>
          {t('triageAdmin.save')}
        </Button>
      </div>
    </form>
  );
}
