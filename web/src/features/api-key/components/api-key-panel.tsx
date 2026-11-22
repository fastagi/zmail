'use client';

import { useMemo, useState, type FormEvent } from 'react';
import { AlertCircle, Ban, Copy, KeyRound, Plus, RotateCw } from 'lucide-react';
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Checkbox } from '@/components/ui/checkbox';
import {
  useApiKeys,
  useCreateApiKey,
  useRevokeApiKey,
} from '@/features/api-key/hooks/use-api-keys';
import { API_KEY_PLACEHOLDER, SendQuickStart } from '@/features/api-key/components/send-quick-start';
import { useSendReadiness } from '@/features/api-key/hooks/use-send-readiness';
import { createApiKeySchema } from '@/features/api-key/schemas';
import {
  API_KEY_SCOPE_MESSAGE_KEYS,
  API_KEY_SCOPES,
  DEFAULT_API_KEY_SCOPES,
  type ApiKeyScope,
} from '@/features/api-key/scopes';
import type { ApiKey, CreateApiKeyResult } from '@/features/api-key/types';
import { useT } from '@/i18n';

export function ApiKeyPanel() {
  const t = useT('settings.api');
  const locale = useLocale();
  const apiKeys = useApiKeys();
  const createApiKey = useCreateApiKey();
  const revokeApiKey = useRevokeApiKey();
  const readiness = useSendReadiness();
  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<CreateApiKeyResult | null>(null);
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<readonly ApiKeyScope[]>(DEFAULT_API_KEY_SCOPES);
  const [expiresAt, setExpiresAt] = useState('');
  const [formError, setFormError] = useState<string | null>(null);
  const [revokeTarget, setRevokeTarget] = useState<ApiKey | null>(null);
  const dateFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  );

  const closeCreateDialog = () => {
    setCreateOpen(false);
    setCreated(null);
    setName('');
    setScopes(DEFAULT_API_KEY_SCOPES);
    setExpiresAt('');
    setFormError(null);
    createApiKey.reset();
  };

  const submitCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(null);
    const expiry = expiresAt ? new Date(expiresAt) : null;
    if (expiry && (Number.isNaN(expiry.getTime()) || expiry <= new Date())) {
      setFormError(t('invalidForm'));
      return;
    }
    if (scopes.length === 0) {
      setFormError(t('scopeRequired'));
      return;
    }
    const candidate = {
      name,
      // In the order the dialog offers them.
      scopes: API_KEY_SCOPES.filter(scope => scopes.includes(scope)),
      ...(expiry ? { expires_at: expiry.toISOString() } : {}),
    };
    const parsed = createApiKeySchema.safeParse(candidate);
    if (!parsed.success) {
      setFormError(t('invalidForm'));
      return;
    }

    try {
      const result = await createApiKey.mutateAsync(parsed.data);
      setCreated(result);
      createApiKey.reset();
      toast.success(t('createdSuccess'));
    } catch {
      createApiKey.reset();
      setFormError(t('createError'));
    }
  };

  const copySecret = async () => {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.plaintext_key);
      toast.success(t('copied'));
    } catch {
      toast.error(t('copyError'));
    }
  };

  const confirmRevoke = async () => {
    if (!revokeTarget) return;
    try {
      await revokeApiKey.mutateAsync(revokeTarget.id);
      toast.success(t('revokedSuccess'));
      setRevokeTarget(null);
    } catch {
      toast.error(t('revokeError'));
    }
  };

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>{t('title')}</CardTitle>
          <CardDescription>{t('description')}</CardDescription>
          <CardAction>
            <Button size="sm" icon={<Plus className="size-4" />} onClick={() => setCreateOpen(true)}>
              {t('create')}
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent>
          {apiKeys.isPending ? (
            <div className="flex min-h-40 items-center justify-center text-sm text-muted-foreground">
              {t('loading')}
            </div>
          ) : apiKeys.isError ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" />
              <AlertTitle>{t('loadErrorTitle')}</AlertTitle>
              <AlertDescription className="flex items-center justify-between gap-4">
                <span>{t('loadError')}</span>
                <Button
                  size="xs"
                  variant="outline"
                  icon={<RotateCw className="size-3" />}
                  onClick={() => apiKeys.refetch()}
                >
                  {t('retry')}
                </Button>
              </AlertDescription>
            </Alert>
          ) : apiKeys.data.items.length === 0 ? (
            <div className="flex min-h-40 flex-col items-center justify-center gap-3 border border-dashed p-6 text-center">
              <KeyRound className="size-7 text-muted-foreground" />
              <div>
                <p className="text-sm font-medium">{t('emptyTitle')}</p>
                <p className="mt-1 text-sm text-muted-foreground">{t('emptyDescription')}</p>
              </div>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('columns.name')}</TableHead>
                  <TableHead>{t('columns.scopes')}</TableHead>
                  <TableHead>{t('columns.status')}</TableHead>
                  <TableHead>{t('columns.lastUsed')}</TableHead>
                  <TableHead className="text-right">{t('columns.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {apiKeys.data.items.map(apiKey => {
                  const status = apiKeyStatus(apiKey);
                  return (
                    <TableRow key={apiKey.id}>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <span className="font-medium">{apiKey.name}</span>
                          <Badge variant={apiKey.mode === 'test' ? 'secondary' : 'outline'}>
                            {t(`mode.${apiKey.mode}`)}
                          </Badge>
                        </div>
                        <code className="text-xs text-muted-foreground">{apiKey.key_prefix}</code>
                      </TableCell>
                      <TableCell>
                        <div className="flex max-w-72 flex-wrap gap-1">
                          {apiKey.scopes.length ? (
                            apiKey.scopes.map(scope => (
                              <Badge key={scope} variant="outline">
                                {scope}
                              </Badge>
                            ))
                          ) : (
                            <span className="text-muted-foreground">{t('noScopes')}</span>
                          )}
                        </div>
                      </TableCell>
                      <TableCell>
                        <Badge variant={status === 'revoked' ? 'destructive' : 'outline'}>
                          {t(`status.${status}`)}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {apiKey.last_used_at
                          ? dateFormatter.format(new Date(apiKey.last_used_at))
                          : t('neverUsed')}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          isIcon
                          size="sm"
                          variant="ghost"
                          aria-label={t('revoke')}
                          title={t('revoke')}
                          disabled={status === 'revoked'}
                          onClick={() => setRevokeTarget(apiKey)}
                        >
                          <Ban className="size-4" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {readiness.status === 'unavailable' || readiness.status === 'error' ? null : (
        <Card className="mt-5">
          <CardHeader>
            <CardTitle>{t('quickStart.title')}</CardTitle>
            <CardDescription>
              {t('quickStart.description', { placeholder: API_KEY_PLACEHOLDER })}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <SendQuickStart readiness={readiness} idPrefix="quick-start" />
          </CardContent>
        </Card>
      )}

      <Dialog
        open={createOpen}
        onOpenChange={open => (open ? setCreateOpen(true) : closeCreateDialog())}
      >
        <DialogContent size={created ? 'lg' : 'default'}>
          <DialogHeader>
            <DialogTitle>{created ? t('secretTitle') : t('createTitle')}</DialogTitle>
            <DialogDescription>
              {created ? t('secretDescription') : t('createDescription')}
            </DialogDescription>
          </DialogHeader>
          {created ? (
            <>
              <DialogBody className="space-y-4">
                <Alert>
                  <KeyRound className="size-4" />
                  <AlertTitle>{t('secretWarningTitle')}</AlertTitle>
                  <AlertDescription>{t('secretWarning')}</AlertDescription>
                </Alert>
                <div className="space-y-2">
                  <Label htmlFor="created-api-key">{t('plaintextKey')}</Label>
                  <div className="flex gap-2">
                    <Input id="created-api-key" value={created.plaintext_key} readOnly />
                    <Button
                      type="button"
                      isIcon
                      variant="outline"
                      aria-label={t('copy')}
                      title={t('copy')}
                      onClick={copySecret}
                    >
                      <Copy className="size-4" />
                    </Button>
                  </div>
                </div>
                {canSend(created.api_key.scopes) && readiness.status !== 'unavailable' ? (
                  <section className="space-y-3 border-t pt-4">
                    <div className="space-y-1">
                      <h3 className="text-sm font-semibold">{t('quickStart.title')}</h3>
                      <p className="text-sm text-muted-foreground">
                        {t('quickStart.secretDescription')}
                      </p>
                    </div>
                    <SendQuickStart
                      readiness={readiness}
                      apiKey={created.plaintext_key}
                      idPrefix="created-key"
                    />
                  </section>
                ) : null}
              </DialogBody>
              <DialogFooter>
                <Button onClick={closeCreateDialog}>{t('done')}</Button>
              </DialogFooter>
            </>
          ) : (
            <form onSubmit={submitCreate} className="flex min-h-0 flex-col">
              <DialogBody className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="api-key-name">{t('name')}</Label>
                  <Input
                    id="api-key-name"
                    value={name}
                    maxLength={100}
                    autoComplete="off"
                    placeholder={t('namePlaceholder')}
                    onChange={event => setName(event.target.value)}
                  />
                </div>
                <fieldset className="space-y-2">
                  <legend className="text-sm font-medium">{t('scopes')}</legend>
                  <p className="text-sm text-muted-foreground">{t('scopesHint')}</p>
                  <div className="grid gap-2">
                    {API_KEY_SCOPES.map(scope => {
                      const key = API_KEY_SCOPE_MESSAGE_KEYS[scope];
                      const checked = scopes.includes(scope);
                      return (
                        <label
                          key={scope}
                          className="flex cursor-pointer items-start gap-3 rounded-md border p-3 transition-colors hover:bg-muted/50 has-[[data-state=checked]]:border-primary/50 has-[[data-state=checked]]:bg-primary/5"
                        >
                          <Checkbox
                            checked={checked}
                            className="mt-0.5"
                            onCheckedChange={value =>
                              setScopes(current =>
                                value === true
                                  ? [...current, scope]
                                  : current.filter(item => item !== scope)
                              )
                            }
                          />
                          <span className="min-w-0 space-y-0.5">
                            <span className="flex flex-wrap items-center gap-x-2 text-sm font-medium">
                              {t(`scopeOptions.${key}.label`)}
                              <code className="text-xs font-normal text-muted-foreground">
                                {scope}
                              </code>
                            </span>
                            <span className="block text-sm text-muted-foreground">
                              {t(`scopeOptions.${key}.description`)}
                            </span>
                          </span>
                        </label>
                      );
                    })}
                  </div>
                </fieldset>
                <div className="space-y-2">
                  <Label htmlFor="api-key-expiry">{t('expiresAt')}</Label>
                  <Input
                    id="api-key-expiry"
                    type="datetime-local"
                    value={expiresAt}
                    onChange={event => setExpiresAt(event.target.value)}
                  />
                </div>
                {formError ? (
                  <p role="alert" className="text-sm text-error">
                    {formError}
                  </p>
                ) : null}
              </DialogBody>
              <DialogFooter className="mt-4">
                <Button type="button" variant="outline" onClick={closeCreateDialog}>
                  {t('cancel')}
                </Button>
                <Button type="submit" loading={createApiKey.isPending}>
                  {t('create')}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={revokeTarget !== null} onOpenChange={open => !open && setRevokeTarget(null)}>
        <DialogContent size="sm">
          <DialogHeader>
            <DialogTitle>{t('revokeTitle')}</DialogTitle>
            <DialogDescription>{t('revokeDescription')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRevokeTarget(null)}>
              {t('cancel')}
            </Button>
            <Button variant="destructive" loading={revokeApiKey.isPending} onClick={confirmRevoke}>
              {t('revoke')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

/** Whether a key can call `POST /v1/outbound-mail-messages`. */
function canSend(scopes: readonly string[]): boolean {
  return scopes.includes('mail:send') || scopes.includes('*');
}

function apiKeyStatus(apiKey: ApiKey): 'active' | 'expired' | 'revoked' {
  if (apiKey.revoked_at) return 'revoked';
  if (apiKey.expires_at && new Date(apiKey.expires_at) <= new Date()) return 'expired';
  return 'active';
}
