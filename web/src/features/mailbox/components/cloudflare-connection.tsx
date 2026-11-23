'use client';
import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useT } from '@/i18n';
import { ApiErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';

type Checks = { dns: boolean; receiving: boolean; sending: boolean };
type Connection = { id: string; zone_name: string; worker: string };
type Zone = { id: string; name: string; account_name: string };
type Installation = { signing_secret: string; callback_path: string };
const path = '/mail-cloudflare-connection';

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
 * The message for a failed step of the own-token flow. Every request here runs with the token the
 * user pasted (or stored), so the copy names it, and each step says what the failure means there:
 * listing zones fails when Cloudflare does not accept the token (`422` without a specific code),
 * binding re-reads the zones with it, and setting a domain up reads that domain's DNS records.
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
  async function run(action: () => Promise<void>, step: CloudflareStep = 'other') {
    setBusy(true);
    setMessage('');
    try {
      await action();
    } catch (error) {
      setMessage(t(cloudflareErrorKey(error, step), { zone: row?.zone_name ?? '' }));
    } finally {
      setBusy(false);
    }
  }
  const row = status.data?.connection;
  return (
    <section className="space-y-3 rounded-md border p-4" aria-labelledby="mail-cloudflare-title">
      <div className="space-y-1">
        <h3 id="mail-cloudflare-title" className="font-semibold">
          {t('domain.cfTitle')}
        </h3>
        <p className="text-xs text-muted-foreground">{t('domain.cfWhen')}</p>
      </div>
      {status.isPending ? (
        <p>{t('domain.cfLoading')}</p>
      ) : status.isError ? (
        <p>{t('domain.cfUnavailable')}</p>
      ) : row ? (
        <>
          <p>{row.zone_name}</p>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() =>
              run(async () =>
                setChecks(await request.post<Checks>(path + '/' + row.id + '/checks', {}, options))
              )
            }
          >
            {t('domain.cfCheck')}
          </Button>
          {checks && (
            <div className="space-y-1 text-sm" role="status">
              {(['dns', 'sending', 'receiving'] as const).map(key => (
                <p key={key}>
                  {t(`domain.cfCheck_${key}`)} ·{' '}
                  {t(checks[key] ? 'domain.cfReadable' : 'domain.cfUnverified')}
                </p>
              ))}
              <p className="text-muted-foreground">{t('domain.cfCheckHint')}</p>
            </div>
          )}

          <details open={install ? true : undefined}>
            <summary>{t('domain.cfWorker')}</summary>
            <div className="space-y-2 py-2 text-sm">
              <p>{t('domain.cfDeploy')}</p>
              <p>
                Worker <code>{row.worker}</code>
              </p>
              <p>
                MAILBOX_INBOUND_URL{' '}
                <code className="break-all">
                  {install?.callback_path ??
                    '/v1/cloudflare-connections/' + row.id + '/inbound-mail-messages'}
                </code>
              </p>
              {install && (
                <p>
                  MAILBOX_INBOUND_SECRET <code className="break-all">{install.signing_secret}</code>
                </p>
              )}
            </div>
          </details>
          <label className="block">
            {t('domain.cfDomain')}
            <Input
              value={name}
              placeholder={'mail.' + row.zone_name}
              onChange={e => setName(e.target.value)}
            />
          </label>
          <select
            aria-label={t('domain.cfMode')}
            value={capability}
            onChange={e => setCapability(e.target.value)}
          >
            <option value="send_only">{t('domain.cfSend')}</option>
            <option value="send_receive">{t('domain.cfBoth')}</option>
          </select>
          <Button
            disabled={busy || !name.trim()}
            onClick={() =>
              run(async () => {
                await request.post(path + '/' + row.id + '/domains', { name, capability }, options);
                setName('');
                setMessage(t('domain.cfStarted'));
                await cache.invalidateQueries({ queryKey: ['mailbox'] });
              }, 'domain')
            }
          >
            {t('domain.cfSetup')}
          </Button>
          <div>
            {confirm ? (
              <>
                <p>{t('domain.cfDisconnectImpact')}</p>
                <Button
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
                </Button>{' '}
                <Button variant="outline" onClick={() => setConfirm(false)}>
                  {t('domain.cfCancel')}
                </Button>
              </>
            ) : (
              <Button variant="outline" onClick={() => setConfirm(true)}>
                {t('domain.cfDisconnect')}
              </Button>
            )}
          </div>
        </>
      ) : (
        <>
          <label className="block">
            {t('domain.cfToken')}
            <Input
              type="password"
              autoComplete="off"
              value={token}
              disabled={busy}
              onChange={e => {
                setToken(e.target.value);
                setZones([]);
                setZone('');
              }}
            />
          </label>
          <Button
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
                if (!list.length) setMessage(t('domain.cfEmpty'));
              }, 'list')
            }
          >
            {t('domain.cfList')}
          </Button>
          {!!zones.length && (
            <>
              <select
                aria-label={t('domain.cfZone')}
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
                    await status.refetch();
                  }, 'bind')
                }
              >
                {t('domain.cfBind')}
              </Button>
            </>
          )}
        </>
      )}
      {message && (
        <p role="status" className="text-sm">
          {message}
        </p>
      )}
    </section>
  );
}
