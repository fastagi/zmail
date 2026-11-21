'use client';
import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useT } from '@/i18n';
import request from '@/http/request';

type Checks = { dns: boolean; receiving: boolean; sending: boolean };
type Connection = { id: string; zone_name: string; worker: string };
type Zone = { id: string; name: string; account_name: string };
type Installation = { signing_secret: string; callback_path: string };
const path = '/mail-cloudflare-connection';

export function CloudflareConnectionForm({ organizationId }: { organizationId: number }) {
  const t = useT('mailbox');
  const cache = useQueryClient();
  const options = { headers: { 'Organization-Id': String(organizationId) } };
  const queryKey = ['mail-cloudflare-connection', organizationId];
  const status = useQuery({
    queryKey,
    queryFn: () => request.get<{ connection: Connection | null }>(path, options),
    retry: false,
  });
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
  async function run(action: () => Promise<void>) {
    setBusy(true);
    setMessage('');
    try {
      await action();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t('domain.cfFailed'));
    } finally {
      setBusy(false);
    }
  }
  const row = status.data?.connection;
  return (
    <section className="space-y-3 rounded-md border p-4" aria-label="Cloudflare">
      <h3 className="font-semibold">Cloudflare</h3>
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
              })
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
              })
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
                  })
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
