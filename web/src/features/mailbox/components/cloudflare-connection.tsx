'use client';

import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  AlertCircle,
  Check,
  CheckCircle2,
  Cloud,
  Copy,
  ExternalLink,
  Globe,
  KeyRound,
  RefreshCw,
  ShieldCheck,
  Unlink,
} from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

type Checks = { dns: boolean; receiving: boolean; sending: boolean };
type Connection = { id: string; zone_name: string; worker: string };
type Zone = { id: string; name: string; account_name: string };
type Installation = { signing_secret: string; callback_path: string };
const path = '/mail-cloudflare-connection';
const cloudflareTokensUrl = 'https://dash.cloudflare.com/profile/api-tokens';

type CloudflareStep = 'list' | 'bind' | 'domain' | 'other';

/** The organization's own-Cloudflare-account connection; `null` when it has none. */
export function useCloudflareConnection(organizationId: number) {
  return useQuery({
    queryKey: ['mail-cloudflare-connection', organizationId],
    queryFn: () =>
      request.get<{ connection: Connection | null }>(path, {
        headers: { 'Organization-Id': String(organizationId) },
      }),
    retry: false,
  });
}

/**
 * Maps a failed own-Cloudflare-token step to an actionable translation key.
 */
export function cloudflareErrorKey(failure: unknown, step: CloudflareStep) {
  if (failure instanceof ApiError) {
    const code = failure.errorCode;
    if (step === 'list' && failure.status === 422) return 'domain.cfTokenRefused' as const;
    if (code === ApiErrorCode.MAILBOX_DOMAIN_PROVIDER_FAILED) {
      if (step === 'list' || step === 'bind') return 'domain.cfTokenRefused' as const;
      if (step === 'domain') return 'domain.cfTokenMissingPermission' as const;
    }
    if (code === ApiErrorCode.MAILBOX_DOMAIN_ZONE_NOT_FOUND) {
      if (step === 'bind') return 'domain.cfZoneNotVisible' as const;
      if (step === 'domain') return 'domain.cfDomainOutsideZone' as const;
    }
    if (code === ApiErrorCode.MAILBOX_DOMAIN_TAKEN && step === 'bind') {
      return 'domain.cfAlreadyConnected' as const;
    }
  }
  return resolveMailboxErrorKey(failure);
}

function CopyableValue({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      // Clipboard may be unavailable in restricted contexts.
    }
  }

  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border bg-background px-3 py-2 text-xs">
      <div className="min-w-0 flex-1 space-y-0.5">
        <span className="block font-medium text-muted-foreground">{label}</span>
        <code className="block break-all font-mono text-foreground">{value}</code>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-7 shrink-0 px-2"
        aria-label={`Copy ${label}`}
        onClick={() => void handleCopy()}
      >
        {copied ? (
          <Check className="size-3.5 text-emerald-500" />
        ) : (
          <Copy className="size-3.5 text-muted-foreground" />
        )}
      </Button>
    </div>
  );
}

export function CloudflareConnectionForm({ organizationId }: { organizationId: number }) {
  const t = useT('mailbox');
  const cache = useQueryClient();
  const options = { headers: { 'Organization-Id': String(organizationId) } };
  const status = useCloudflareConnection(organizationId);
  const [token, setToken] = useState('');
  const [zones, setZones] = useState<Zone[]>([]);
  const [zone, setZone] = useState('');
  const [name, setName] = useState('');
  const [capability, setCapability] = useState('send_only');
  const [install, setInstall] = useState<Installation | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [checks, setChecks] = useState<Checks | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [messageKind, setMessageKind] = useState<'error' | 'success'>('success');

  async function run(action: () => Promise<void>, step: CloudflareStep = 'other') {
    setBusy(true);
    setMessage('');
    try {
      await action();
    } catch (error) {
      setMessageKind('error');
      setMessage(t(cloudflareErrorKey(error, step), { zone: row?.zone_name ?? '' }));
    } finally {
      setBusy(false);
    }
  }

  const row = status.data?.connection;
  const activeStep = row ? 3 : zones.length > 0 ? 2 : 1;

  return (
    <section
      className="space-y-4 rounded-lg border bg-card/60 p-5 shadow-xs"
      aria-labelledby="mail-cloudflare-title"
    >
      {/* Header & Stepper */}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            <Cloud className="size-4 text-primary" aria-hidden="true" />
            <h3 id="mail-cloudflare-title" className="text-base font-semibold">
              {t('domain.cfTitle')}
            </h3>
            {row ? (
              <Badge variant="secondary" className="gap-1 text-xs">
                <CheckCircle2 className="size-3 text-emerald-500" />
                {row.zone_name}
              </Badge>
            ) : null}
          </div>
          <p className="text-xs text-muted-foreground">{t('domain.cfWhen')}</p>
        </div>

        <a
          href={cloudflareTokensUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2.5 py-1.5 text-xs font-medium text-foreground transition-colors hover:bg-muted"
        >
          <KeyRound className="size-3.5 text-muted-foreground" />
          Cloudflare API Tokens
          <ExternalLink className="size-3 text-muted-foreground" />
        </a>
      </div>

      {/* 3-Step Visual Progress */}
      <ol className="grid grid-cols-3 gap-2 text-xs" aria-label="Cloudflare setup progress">
        {[
          { step: 1, label: t('domain.cfToken') },
          { step: 2, label: t('domain.cfZone') },
          { step: 3, label: t('domain.cfDomain') },
        ].map(item => {
          const done = activeStep > item.step;
          const current = activeStep === item.step;
          return (
            <li
              key={item.step}
              className={cn(
                'flex items-center gap-2 rounded-md border px-2.5 py-1.5 transition-colors',
                done && 'border-emerald-500/30 bg-emerald-500/5 text-foreground',
                current && 'border-primary/50 bg-primary/5 font-medium text-foreground',
                !done && !current && 'border-border/60 text-muted-foreground'
              )}
            >
              <span
                className={cn(
                  'flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold',
                  done && 'bg-emerald-500 text-white',
                  current && 'bg-primary text-primary-foreground',
                  !done && !current && 'bg-muted text-muted-foreground'
                )}
              >
                {done ? <Check className="size-3" /> : item.step}
              </span>
              <span className="truncate">{item.label}</span>
            </li>
          );
        })}
      </ol>

      {status.isPending ? (
        <p className="text-sm text-muted-foreground">{t('domain.cfLoading')}</p>
      ) : status.isError ? (
        <Alert variant="destructive">
          <AlertDescription>{t('domain.cfUnavailable')}</AlertDescription>
        </Alert>
      ) : row ? (
        <div className="space-y-4">
          {/* Connected Zone & Permission Checks */}
          <div className="space-y-3 rounded-md border bg-muted/20 p-3.5">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2 text-sm font-medium">
                <Globe className="size-4 text-muted-foreground" />
                <span>{row.zone_name}</span>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() =>
                  run(async () =>
                    setChecks(
                      await request.post<Checks>(path + '/' + row.id + '/checks', {}, options)
                    )
                  )
                }
              >
                <RefreshCw className={cn('mr-1.5 size-3.5', busy && 'animate-spin')} />
                {t('domain.cfCheck')}
              </Button>
            </div>

            {checks && (
              <div className="space-y-2 border-t pt-3 text-sm" role="status">
                <div className="flex flex-wrap gap-2">
                  {(['dns', 'sending', 'receiving'] as const).map(key => (
                    <span
                      key={key}
                      className={cn(
                        'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium',
                        checks[key]
                          ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                          : 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300'
                      )}
                    >
                      {checks[key] ? (
                        <CheckCircle2 className="size-3.5" />
                      ) : (
                        <AlertCircle className="size-3.5" />
                      )}
                      {t(`domain.cfCheck_${key}`)} ·{' '}
                      {t(checks[key] ? 'domain.cfReadable' : 'domain.cfUnverified')}
                    </span>
                  ))}
                </div>
                <p className="text-xs text-muted-foreground">{t('domain.cfCheckHint')}</p>
              </div>
            )}
          </div>

          {/* Worker Details with One-Click Copy */}
          <details
            open={install ? true : undefined}
            className="rounded-md border bg-muted/15 px-3.5 py-2.5"
          >
            <summary className="cursor-pointer text-sm font-medium select-none">
              {t('domain.cfWorker')}
            </summary>
            <div className="mt-3 space-y-2 text-sm">
              <p className="text-xs text-muted-foreground">{t('domain.cfDeploy')}</p>
              <CopyableValue label="Worker" value={row.worker} />
              <CopyableValue
                label="MAILBOX_INBOUND_URL"
                value={
                  install?.callback_path ??
                  '/v1/cloudflare-connections/' + row.id + '/inbound-mail-messages'
                }
              />
              {install && (
                <CopyableValue label="MAILBOX_INBOUND_SECRET" value={install.signing_secret} />
              )}
            </div>
          </details>

          {/* Connect Domain on Connected Zone */}
          <div className="space-y-3 rounded-md border bg-muted/20 p-3.5">
            <div className="space-y-1.5">
              <Label htmlFor="cf-domain-name">{t('domain.cfDomain')}</Label>
              <Input
                id="cf-domain-name"
                value={name}
                placeholder={'mail.' + row.zone_name}
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                onChange={e => setName(e.target.value)}
              />
            </div>

            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup" aria-label={t('domain.cfMode')}>
              {[
                { value: 'send_only', label: t('domain.cfSend') },
                { value: 'send_receive', label: t('domain.cfBoth') },
              ].map(option => (
                <label
                  key={option.value}
                  className={cn(
                    'flex cursor-pointer items-center gap-2 rounded-md border bg-background px-3 py-2 text-sm transition-colors',
                    capability === option.value
                      ? 'border-primary bg-primary/5 font-medium'
                      : 'hover:bg-muted/40'
                  )}
                >
                  <input
                    type="radio"
                    name="cf-domain-capability"
                    value={option.value}
                    checked={capability === option.value}
                    className="accent-primary"
                    onChange={() => setCapability(option.value)}
                  />
                  <span>{option.label}</span>
                </label>
              ))}
            </div>

            <Button
              type="button"
              disabled={busy || !name.trim()}
              onClick={() =>
                run(async () => {
                  await request.post(
                    path + '/' + row.id + '/domains',
                    { name: name.trim(), capability },
                    options
                  );
                  setName('');
                  setMessageKind('success');
                  setMessage(t('domain.cfStarted'));
                  await cache.invalidateQueries({ queryKey: ['mailbox'] });
                }, 'domain')
              }
            >
              {t('domain.cfSetup')}
            </Button>
          </div>

          {/* Disconnect Control */}
          <div className="border-t pt-3">
            {confirm ? (
              <div className="space-y-2 rounded-md border border-destructive/30 bg-destructive/5 p-3">
                <p className="text-xs text-destructive">{t('domain.cfDisconnectImpact')}</p>
                <div className="flex items-center gap-2">
                  <Button
                    type="button"
                    size="sm"
                    disabled={busy}
                    variant="destructive"
                    onClick={() =>
                      run(async () => {
                        await request.delete(path + '/' + row.id, options);
                        setInstall(null);
                        setChecks(null);
                        setConfirm(false);
                        await status.refetch();
                      })
                    }
                  >
                    {t('domain.cfConfirm')}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => setConfirm(false)}
                  >
                    {t('domain.cfCancel')}
                  </Button>
                </div>
              </div>
            ) : (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setConfirm(true)}
              >
                <Unlink className="mr-1.5 size-3.5" />
                {t('domain.cfDisconnect')}
              </Button>
            )}
          </div>
        </div>
      ) : (
        <div className="space-y-4">
          {/* Required Permissions Reference */}
          <div className="rounded-md border bg-muted/20 p-3 text-xs">
            <div className="mb-1.5 flex items-center gap-1.5 font-medium text-foreground">
              <ShieldCheck className="size-3.5 text-primary" />
              Required Cloudflare Token Permissions
            </div>
            <div className="flex flex-wrap gap-1.5">
              {[
                'Zone · Zone · Read',
                'Zone · DNS · Edit',
                'Zone · Email Routing Rules · Edit',
                'Account · Email Sending · Edit',
              ].map(perm => (
                <Badge key={perm} variant="outline" className="font-mono text-[11px] font-normal">
                  {perm}
                </Badge>
              ))}
            </div>
          </div>

          {/* Step 1: Paste Token & Preview Zones */}
          <div className="space-y-2">
            <Label htmlFor="cf-api-token-input">{t('domain.cfToken')}</Label>
            <div className="flex flex-wrap items-center gap-2">
              <Input
                id="cf-api-token-input"
                type="password"
                autoComplete="off"
                className="min-w-60 flex-1"
                value={token}
                disabled={busy}
                placeholder="Paste Cloudflare API Token"
                onChange={e => {
                  setToken(e.target.value);
                  setZones([]);
                  setZone('');
                }}
              />
              <Button
                type="button"
                variant="outline"
                disabled={busy || !token.trim()}
                onClick={() =>
                  run(async () => {
                    const list = await request.post<Zone[]>(
                      '/mail-cloudflare-previews',
                      { api_token: token.trim() },
                      options
                    );
                    setZones(list);
                    setZone(list[0]?.id ?? '');
                    if (!list.length) {
                      setMessageKind('error');
                      setMessage(t('domain.cfEmpty'));
                    }
                  }, 'list')
                }
              >
                {t('domain.cfList')}
              </Button>
            </div>
          </div>

          {/* Step 2: Select Zone & Bind */}
          {!!zones.length && (
            <div className="space-y-2 rounded-md border bg-muted/20 p-3">
              <Label htmlFor="cf-zone-select">{t('domain.cfZone')}</Label>
              <div className="flex flex-wrap items-center gap-2">
                <select
                  id="cf-zone-select"
                  aria-label={t('domain.cfZone')}
                  className="h-9 min-w-56 flex-1 rounded-md border border-input bg-background px-3 text-sm shadow-xs focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
                  value={zone}
                  onChange={e => setZone(e.target.value)}
                >
                  {zones.map(z => (
                    <option key={z.id} value={z.id}>
                      {z.name} · {z.account_name}
                    </option>
                  ))}
                </select>
                <Button
                  type="button"
                  disabled={busy || !zone}
                  onClick={() =>
                    run(async () => {
                      const result = await request.post<Installation>(
                        path,
                        { api_token: token.trim(), zone_id: zone },
                        options
                      );
                      setToken('');
                      setZones([]);
                      setInstall(result);
                      setMessageKind('success');
                      await status.refetch();
                    }, 'bind')
                  }
                >
                  {t('domain.cfBind')}
                </Button>
              </div>
            </div>
          )}
        </div>
      )}

      {message && (
        <Alert
          role="status"
          variant={messageKind === 'error' ? 'destructive' : 'default'}
          className={cn(
            messageKind === 'success' &&
              'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
          )}
        >
          <AlertDescription>{message}</AlertDescription>
        </Alert>
      )}
    </section>
  );
}
