'use client';

import { useMemo, useState, type FormEvent } from 'react';
import {
  AlertCircle,
  Copy,
  KeyRound,
  Lock,
  Plus,
  RotateCw,
  Smartphone,
  Trash2,
} from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { useLocale } from 'next-intl';
import { toast } from 'sonner';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { totpService } from '@/features/auth/services/totp-service';
import { useCurrentUser } from '@/features/auth/store/auth-store';
import { ConfirmDialog } from '@/features/mailbox/components/confirm-dialog';
import { MailClientGuides } from '@/features/mailbox/components/mail-client-guides';
import { relativeTime, useRelativeTimeFormat } from '@/features/mailbox/components/mail-shared';
import {
  useCreateMailAppPassword,
  useMailAppPasswords,
  useMailClientSettings,
  useRevokeMailAppPassword,
  useUpdateMailClientSettings,
} from '@/features/mailbox/hooks/use-mail-app-passwords';
import { useChosenMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import {
  MAIL_APP_PASSWORD_NAME_MAX_LENGTH,
  type MailAppPassword,
  type MailAppPasswordList,
  type MailClientProtocol,
} from '@/features/mailbox/mail-client-schemas';
import type { Mailbox } from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';

type CreateError =
  | 'nameRequired'
  | 'scopeRequired'
  | 'passwordRequired'
  | 'passwordWrong'
  | 'totpRequired'
  | 'totpWrong'
  | 'rateLimited'
  | 'limitReached'
  | 'disabled'
  | 'failed';

/** What the dialog shows after creating: the secret, once, until the dialog closes. */
interface CreatedSecret {
  name: string;
  secret: string;
}

/** Every mail client scope, in the order the dialog offers them; a new device gets both. */
const MAIL_CLIENT_SCOPES = ['imap', 'smtp'] as const satisfies readonly MailClientProtocol[];

/**
 * The addresses of the member's own personal mailboxes, which a mail app signs in with. Connected
 * copies of outside accounts never sign in.
 */
function ownMailboxAddresses(
  mailboxes: readonly Pick<Mailbox, 'address' | 'type' | 'origin' | 'owner_user_id'>[],
  userId: string | number | undefined
): string[] {
  if (userId === undefined) return [];
  return mailboxes
    .filter(
      mailbox =>
        mailbox.type === 'personal' &&
        mailbox.origin !== 'connected' &&
        mailbox.owner_user_id !== null &&
        String(mailbox.owner_user_id) === String(userId)
    )
    .map(mailbox => mailbox.address);
}

/**
 * Settings tab for reading and sending mail in desktop and phone mail apps: the servers to enter, the member's
 * own mail client passwords (one per device, the secret shown once), setup steps per app, and, for
 * owners and admins, the organization switch. The API stays the authority on every rule.
 */
export function MailClientsTab() {
  const t = useT('settings.mailClients');
  const organization = useChosenMailOrganization();
  const organizationId = organization?.id;
  const canManage = organization?.role === 'owner' || organization?.role === 'admin';
  const passwords = useMailAppPasswords(organizationId);
  const [createOpen, setCreateOpen] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<MailAppPassword | null>(null);
  const revoke = useRevokeMailAppPassword(organizationId);

  const confirmRevoke = async () => {
    if (!revokeTarget) return;
    try {
      await revoke.mutateAsync(revokeTarget.id);
      toast.success(t('devices.revoked'));
      setRevokeTarget(null);
    } catch {
      toast.error(t('devices.revokeError'));
    }
  };

  const list = passwords.data;
  const canAdd = list !== undefined && list.access.enabled && list.items.length < list.limit;

  return (
    <div className="space-y-5">
      <div className="space-y-1">
        <h2 className="text-lg font-semibold">{t('title')}</h2>
        <p className="text-sm text-muted-foreground">{t('description')}</p>
      </div>

      {list && !list.access.enabled && list.access.reason ? (
        <Alert>
          <Lock className="size-4" />
          <AlertDescription>{t(`access.${list.access.reason}`)}</AlertDescription>
        </Alert>
      ) : null}

      {list ? <ServerCard server={list.server} organizationId={organizationId} /> : null}

      <Card>
        <CardHeader>
          <CardTitle>{t('devices.title')}</CardTitle>
          <CardDescription>
            {t('devices.description')}
            {list ? (
              <span className="block">
                {t('devices.count', { count: list.items.length, limit: list.limit })}
              </span>
            ) : null}
          </CardDescription>
          <CardAction>
            <Button
              size="sm"
              icon={<Plus className="size-4" />}
              disabled={!canAdd}
              onClick={() => setCreateOpen(true)}
            >
              {t('devices.add')}
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent>
          {passwords.isPending ? (
            <p className="flex min-h-24 items-center justify-center text-sm text-muted-foreground">
              {t('devices.loading')}
            </p>
          ) : passwords.isError ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" />
              <AlertTitle>{t('devices.loadError')}</AlertTitle>
              <AlertDescription>
                <Button
                  size="xs"
                  variant="outline"
                  icon={<RotateCw className="size-3" />}
                  onClick={() => passwords.refetch()}
                >
                  {t('devices.retry')}
                </Button>
              </AlertDescription>
            </Alert>
          ) : list && list.items.length === 0 ? (
            <div className="flex min-h-32 flex-col items-center justify-center gap-3 border border-dashed p-6 text-center">
              <Smartphone className="size-7 text-muted-foreground" aria-hidden="true" />
              <div>
                <p className="text-sm font-medium">{t('devices.emptyTitle')}</p>
                <p className="mt-1 text-sm text-muted-foreground">
                  {t('devices.emptyDescription')}
                </p>
              </div>
              {canAdd ? (
                <Button
                  size="sm"
                  variant="outline"
                  icon={<Plus className="size-4" />}
                  onClick={() => setCreateOpen(true)}
                >
                  {t('devices.add')}
                </Button>
              ) : null}
            </div>
          ) : list ? (
            <DeviceList items={list.items} onRevoke={setRevokeTarget} />
          ) : null}
        </CardContent>
      </Card>

      {canManage ? <OrganizationSwitch organizationId={organizationId} /> : null}

      <MailClientGuides />

      {createOpen ? (
        <CreateDeviceDialog organizationId={organizationId} onClose={() => setCreateOpen(false)} />
      ) : null}

      <ConfirmDialog
        open={revokeTarget !== null}
        title={t('devices.revokeTitle')}
        description={t('devices.revokeDescription', { name: revokeTarget?.name ?? '' })}
        confirmLabel={t('devices.revoke')}
        cancelLabel={t('devices.cancel')}
        pending={revoke.isPending}
        icon={<Trash2 className="size-4" />}
        onConfirm={() => void confirmRevoke()}
        onCancel={() => setRevokeTarget(null)}
      />
    </div>
  );
}

function ServerCard({
  server,
  organizationId,
}: {
  server: MailAppPasswordList['server'];
  organizationId: number | undefined;
}) {
  const t = useT('settings.mailClients.server');
  const user = useCurrentUser();
  const mailboxes = useMailboxes(organizationId ?? 0);
  const addresses = ownMailboxAddresses(mailboxes.data ?? [], user?.id);

  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      toast.success(t('copied'));
    } catch {
      toast.error(t('copyError'));
    }
  };

  const signInRows = (
    <>
      <dt className="text-muted-foreground">{t('username')}</dt>
      <dd className="min-w-0 space-y-1">
        {addresses.length > 0 ? (
          addresses.map(address => (
            <div key={address} className="flex min-w-0 items-center gap-2">
              <code className="break-all font-mono">{address}</code>
              <CopyButton
                label={t('copyAddress', { address })}
                onClick={() => void copy(address)}
              />
            </div>
          ))
        ) : (
          <span>{t('usernameValue')}</span>
        )}
      </dd>
      <ServerRow label={t('password')} value={t('passwordValue')} />
    </>
  );

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('title')}</CardTitle>
        <CardDescription>{t('description')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <section className="space-y-2">
          <h3 className="text-sm font-medium">{t('incoming')}</h3>
          <dl className="grid gap-x-4 gap-y-2 text-sm sm:grid-cols-[8rem_1fr]">
            <ServerRow
              label={t('host')}
              value={server.imap_host}
              copyLabel={t('copyHost')}
              onCopy={copy}
            />
            <ServerRow
              label={t('port')}
              value={String(server.imap_port)}
              copyLabel={t('copyPort')}
              onCopy={copy}
            />
            <ServerRow label={t('security')} value={t('securityValue')} />
            {signInRows}
          </dl>
        </section>
        <section className="space-y-2 border-t pt-4">
          <h3 className="text-sm font-medium">{t('outgoing')}</h3>
          <dl className="grid gap-x-4 gap-y-2 text-sm sm:grid-cols-[8rem_1fr]">
            <ServerRow
              label={t('host')}
              value={server.smtp_host}
              copyLabel={t('copyHost')}
              onCopy={copy}
            />
            <ServerRow
              label={t('port')}
              value={String(server.smtp_port)}
              copyLabel={t('copyPort')}
              onCopy={copy}
            />
            <ServerRow label={t('security')} value={t('securityValue')} />
            <ServerRow
              label={t('starttlsPort')}
              value={String(server.smtp_starttls_port)}
              copyLabel={t('copyStarttlsPort')}
              onCopy={copy}
            />
            {signInRows}
          </dl>
          <p className="text-xs text-muted-foreground">{t('sameSignIn')}</p>
        </section>
      </CardContent>
    </Card>
  );
}

function CopyButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button
      type="button"
      isIcon
      size="xs"
      variant="ghost"
      aria-label={label}
      title={label}
      onClick={onClick}
    >
      <Copy className="size-3" />
    </Button>
  );
}

function ServerRow({
  label,
  value,
  copyLabel,
  onCopy,
}: {
  label: string;
  value: string;
  copyLabel?: string;
  onCopy?: (value: string) => Promise<void>;
}) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="flex min-w-0 items-center gap-2">
        {onCopy ? <code className="break-all font-mono">{value}</code> : <span>{value}</span>}
        {onCopy && copyLabel ? (
          <CopyButton label={copyLabel} onClick={() => void onCopy(value)} />
        ) : null}
      </dd>
    </>
  );
}

function DeviceList({
  items,
  onRevoke,
}: {
  items: readonly MailAppPassword[];
  onRevoke: (password: MailAppPassword) => void;
}) {
  const t = useT('settings.mailClients.devices');
  const locale = useLocale();
  const relative = useRelativeTimeFormat();
  // Read once per mount: "last used" only needs minute precision.
  const [now] = useState(() => Date.now());
  const dateFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }),
    [locale]
  );

  return (
    <ul className="divide-y">
      {items.map(password => (
        <li key={password.id} className="flex items-start justify-between gap-3 py-3">
          <div className="min-w-0 space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="break-words font-medium">{password.name}</span>
              {password.scopes.map(scope => (
                <Badge key={scope} variant="outline">
                  {t(`scopes.${scope}`)}
                </Badge>
              ))}
            </div>
            <p className="text-xs text-muted-foreground">
              {t('hint', { hint: password.hint })} ·{' '}
              {t('created', { date: dateFormatter.format(new Date(password.created_at)) })}
            </p>
            <p className="text-xs text-muted-foreground">
              {password.last_used_at
                ? t('lastUsed', {
                    time: relativeTime(relative, new Date(password.last_used_at).getTime(), now),
                  })
                : t('neverUsed')}
            </p>
          </div>
          <Button
            isIcon
            size="sm"
            variant="ghost"
            aria-label={t('revokeLabel', { name: password.name })}
            title={t('revoke')}
            onClick={() => onRevoke(password)}
          >
            <Trash2 className="size-4" />
          </Button>
        </li>
      ))}
    </ul>
  );
}

function OrganizationSwitch({ organizationId }: { organizationId: number | undefined }) {
  const t = useT('settings.mailClients.organization');
  const settings = useMailClientSettings(organizationId, true);
  const update = useUpdateMailClientSettings(organizationId);

  const change = async (next: boolean) => {
    try {
      await update.mutateAsync(next);
      toast.success(t('saved'));
    } catch {
      toast.error(t('saveError'));
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('title')}</CardTitle>
        <CardDescription>{t('description')}</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="flex items-center justify-between gap-4">
          <Label htmlFor="mail-client-access">{t('toggle')}</Label>
          <Switch
            id="mail-client-access"
            checked={settings.data?.access_enabled ?? false}
            disabled={settings.data === undefined || update.isPending}
            onCheckedChange={value => void change(value)}
          />
        </div>
      </CardContent>
    </Card>
  );
}

/**
 * Creates a device's password. The secret is held in this component's state only and is gone
 * when the dialog closes: the dialog unmounts, and the mutation, which is never cached, is reset.
 */
function CreateDeviceDialog({
  organizationId,
  onClose,
}: {
  organizationId: number | undefined;
  onClose: () => void;
}) {
  const t = useT('settings.mailClients');
  const create = useCreateMailAppPassword(organizationId);
  const totp = useQuery({ queryKey: ['auth', 'totp'], queryFn: totpService.status });
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<readonly MailClientProtocol[]>(MAIL_CLIENT_SCOPES);
  const [currentPassword, setCurrentPassword] = useState('');
  const [totpCode, setTotpCode] = useState('');
  const [askCode, setAskCode] = useState(false);
  const [error, setError] = useState<CreateError | null>(null);
  const [created, setCreated] = useState<CreatedSecret | null>(null);
  const showCode = askCode || totp.data?.enabled === true;

  const close = () => {
    setCreated(null);
    setCurrentPassword('');
    setTotpCode('');
    create.reset();
    onClose();
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    const trimmed = name.trim();
    if (trimmed === '' || [...trimmed].length > MAIL_APP_PASSWORD_NAME_MAX_LENGTH) {
      setError('nameRequired');
      return;
    }
    if (scopes.length === 0) {
      setError('scopeRequired');
      return;
    }
    if (currentPassword === '') {
      setError('passwordRequired');
      return;
    }
    try {
      const result = await create.mutateAsync({
        name: trimmed,
        // In the order the dialog offers them.
        scopes: MAIL_CLIENT_SCOPES.filter(scope => scopes.includes(scope)),
        current_password: currentPassword,
        ...(showCode && totpCode.trim() ? { totp_code: totpCode.trim() } : {}),
      });
      setCreated({ name: result.name, secret: result.secret });
      setCurrentPassword('');
      setTotpCode('');
      create.reset();
    } catch (failure) {
      create.reset();
      const reason = createError(failure);
      if (reason === 'totpRequired' || reason === 'totpWrong') setAskCode(true);
      setError(reason);
    }
  };

  const copySecret = async () => {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.secret);
      toast.success(t('secret.copied'));
    } catch {
      toast.error(t('secret.copyError'));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && close()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {created ? t('secret.title', { name: created.name }) : t('create.title')}
          </DialogTitle>
          <DialogDescription>
            {created ? t('secret.description') : t('create.description')}
          </DialogDescription>
        </DialogHeader>
        {created ? (
          <>
            <DialogBody className="space-y-4">
              <Alert>
                <KeyRound className="size-4" />
                <AlertDescription>{t('secret.warning')}</AlertDescription>
              </Alert>
              <div className="space-y-2">
                <p className="text-sm font-medium">{t('secret.label')}</p>
                <div className="flex items-center gap-2">
                  <code
                    className="flex-1 rounded-md border bg-muted px-3 py-2 text-center font-mono text-lg tracking-wider break-all"
                    aria-live="polite"
                  >
                    {created.secret}
                  </code>
                  <Button
                    type="button"
                    isIcon
                    variant="outline"
                    aria-label={t('secret.copy')}
                    title={t('secret.copy')}
                    onClick={() => void copySecret()}
                  >
                    <Copy className="size-4" />
                  </Button>
                </div>
              </div>
            </DialogBody>
            <DialogFooter>
              <Button onClick={close}>{t('secret.done')}</Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="flex min-h-0 flex-col" noValidate>
            <DialogBody className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="mail-app-password-name">{t('create.name')}</Label>
                <Input
                  id="mail-app-password-name"
                  value={name}
                  maxLength={MAIL_APP_PASSWORD_NAME_MAX_LENGTH}
                  autoComplete="off"
                  placeholder={t('create.namePlaceholder')}
                  onChange={event => setName(event.target.value)}
                />
              </div>
              <fieldset className="space-y-2">
                <legend className="text-sm font-medium">{t('create.access')}</legend>
                {MAIL_CLIENT_SCOPES.map(scope => (
                  <label key={scope} className="flex cursor-pointer items-start gap-3 text-sm">
                    <Checkbox
                      checked={scopes.includes(scope)}
                      className="mt-0.5"
                      onCheckedChange={value =>
                        setScopes(current =>
                          value === true
                            ? [...current, scope]
                            : current.filter(item => item !== scope)
                        )
                      }
                    />
                    <span>{t(`create.${scope}`)}</span>
                  </label>
                ))}
              </fieldset>
              <div className="space-y-2">
                <Label htmlFor="mail-app-password-current">{t('create.currentPassword')}</Label>
                <Input
                  id="mail-app-password-current"
                  type="password"
                  value={currentPassword}
                  autoComplete="current-password"
                  onChange={event => setCurrentPassword(event.target.value)}
                />
                <p className="text-xs text-muted-foreground">{t('create.currentPasswordHint')}</p>
              </div>
              {showCode ? (
                <div className="space-y-2">
                  <Label htmlFor="mail-app-password-totp">{t('create.totp')}</Label>
                  <Input
                    id="mail-app-password-totp"
                    value={totpCode}
                    inputMode="text"
                    autoComplete="one-time-code"
                    onChange={event => setTotpCode(event.target.value)}
                  />
                  <p className="text-xs text-muted-foreground">{t('create.totpHint')}</p>
                </div>
              ) : null}
              {error ? (
                <p role="alert" className="text-sm text-error">
                  {t(`create.errors.${error}`)}
                </p>
              ) : null}
            </DialogBody>
            <DialogFooter className="mt-4">
              <Button type="button" variant="outline" onClick={close}>
                {t('create.cancel')}
              </Button>
              <Button type="submit" loading={create.isPending}>
                {t('create.submit')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** The message for a refused creation, from the contract's error codes and field errors. */
function createError(failure: unknown): CreateError {
  if (!(failure instanceof ApiError)) return 'failed';
  switch (failure.errorCode) {
    case ApiErrorCode.COMMON_VALIDATION_FAILED: {
      const fields = failure.fieldErrors ?? {};
      if (fields.current_password) return 'passwordWrong';
      if (fields.totp_code) {
        return fields.totp_code.some(message => message.includes('required'))
          ? 'totpRequired'
          : 'totpWrong';
      }
      if (fields.name) return 'nameRequired';
      if (fields.scopes) return 'scopeRequired';
      return 'failed';
    }
    case ApiErrorCode.COMMON_RATE_LIMITED:
      return 'rateLimited';
    case ApiErrorCode.MAILBOX_APP_PASSWORD_LIMIT_REACHED:
      return 'limitReached';
    case ApiErrorCode.MAILBOX_MAIL_CLIENT_ACCESS_DISABLED:
    case ApiErrorCode.ORGANIZATION_FEATURE_NOT_GRANTED:
      return 'disabled';
    default:
      return 'failed';
  }
}
