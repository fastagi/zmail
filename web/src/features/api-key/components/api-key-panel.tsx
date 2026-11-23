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
  API_KEY_EXPIRY_CHOICES,
  API_KEY_EXPIRY_PRESET_DAYS,
  API_KEY_PURPOSE_SCOPES,
  API_KEY_PURPOSES,
  API_KEY_SCOPE_MESSAGE_KEYS,
  API_KEY_SCOPES,
  DEFAULT_API_KEY_SCOPES,
  type ApiKeyExpiryChoice,
  type ApiKeyPurpose,
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
  const [purpose, setPurpose] = useState<ApiKeyPurpose>('send');
  const [scopes, setScopes] = useState<readonly ApiKeyScope[]>(DEFAULT_API_KEY_SCOPES);
  const [expiry, setExpiry] = useState<ApiKeyExpiryChoice>('never');
  const [expiresAt, setExpiresAt] = useState('');
  const [errors, setErrors] = useState<FormErrors>({});
  // The plaintext key is shown once: the dialog closes only after it is copied or saved.
  const [secured, setSecured] = useState(false);
  const [closeBlocked, setCloseBlocked] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<ApiKey | null>(null);
  const dateFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  );

  const closeCreateDialog = () => {
    setCreateOpen(false);
    setCreated(null);
    setName('');
    setPurpose('send');
    setScopes(DEFAULT_API_KEY_SCOPES);
    setExpiry('never');
    setExpiresAt('');
    setErrors({});
    setSecured(false);
    setCloseBlocked(false);
    createApiKey.reset();
  };

  const requestCloseCreateDialog = () => {
    if (created && !secured) {
      setCloseBlocked(true);
      return;
    }
    closeCreateDialog();
  };

  const markSecured = (value: boolean) => {
    setSecured(value);
    if (value) setCloseBlocked(false);
  };

  const submitCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const chosenScopes = purpose === 'custom' ? scopes : API_KEY_PURPOSE_SCOPES[purpose];
    const expiresAtValue = resolveExpiry(expiry, expiresAt);
    const nextErrors: FormErrors = {};
    if (!isValidName(name)) nextErrors.name = t('errors.nameRequired');
    if (chosenScopes.length === 0) nextErrors.scopes = t('errors.scopeRequired');
    if (expiresAtValue === 'missing') nextErrors.expiry = t('errors.expiryRequired');
    if (expiresAtValue === 'past') nextErrors.expiry = t('errors.expiryPast');
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    const candidate = {
      name,
      // In the order the dialog offers them.
      scopes: API_KEY_SCOPES.filter(scope => chosenScopes.includes(scope)),
      ...(expiresAtValue ? { expires_at: expiresAtValue } : {}),
    };
    const parsed = createApiKeySchema.safeParse(candidate);
    if (!parsed.success) {
      setErrors({ form: t('invalidForm') });
      return;
    }

    try {
      const result = await createApiKey.mutateAsync(parsed.data);
      setCreated(result);
      createApiKey.reset();
      toast.success(t('createdSuccess'));
    } catch {
      createApiKey.reset();
      setErrors({ form: t('createError') });
    }
  };

  const copySecret = async () => {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.plaintext_key);
      markSecured(true);
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
              <Button size="sm" icon={<Plus className="size-4" />} onClick={() => setCreateOpen(true)}>
                {t('create')}
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader className="hidden sm:table-header-group">
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
                    <TableRow
                      key={apiKey.id}
                      className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-2 py-2 sm:table-row sm:py-0"
                    >
                      <TableCell className="min-w-0 whitespace-normal sm:whitespace-nowrap">
                        <div className="flex items-center gap-2">
                          <span className="font-medium">{apiKey.name}</span>
                          <Badge variant={apiKey.mode === 'test' ? 'secondary' : 'outline'}>
                            {t(`mode.${apiKey.mode}`)}
                          </Badge>
                        </div>
                        <code className="text-xs text-muted-foreground">{apiKey.key_prefix}</code>
                      </TableCell>
                      <TableCell className="col-span-2 sm:col-span-1">
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
                      <TableCell className="col-span-2 sm:col-span-1">
                        <span className="mr-2 text-muted-foreground sm:hidden">
                          {t('columns.status')}
                        </span>
                        <Badge variant={status === 'revoked' ? 'destructive' : 'outline'}>
                          {t(`status.${status}`)}
                        </Badge>
                      </TableCell>
                      <TableCell className="col-span-2 text-muted-foreground sm:col-span-1">
                        <span className="mr-2 sm:hidden">{t('columns.lastUsed')}</span>
                        {apiKey.last_used_at
                          ? dateFormatter.format(new Date(apiKey.last_used_at))
                          : t('neverUsed')}
                      </TableCell>
                      <TableCell className="col-start-2 row-start-1 text-right">
                        <Button
                          isIcon
                          size="sm"
                          variant="ghost"
                          aria-label={t('revokeLabel', { name: apiKey.name })}
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
        onOpenChange={open => (open ? setCreateOpen(true) : requestCloseCreateDialog())}
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
              <DialogFooter className="flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="space-y-1">
                  <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <Checkbox
                      checked={secured}
                      onCheckedChange={value => markSecured(value === true)}
                    />
                    {t('secretSaved')}
                  </label>
                  {closeBlocked ? (
                    <p role="alert" className="text-sm text-error">
                      {t('secretCloseBlocked')}
                    </p>
                  ) : null}
                </div>
                <Button onClick={requestCloseCreateDialog}>{t('done')}</Button>
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
                    aria-invalid={errors.name ? true : undefined}
                    aria-describedby={errors.name ? 'api-key-name-error' : undefined}
                    onChange={event => setName(event.target.value)}
                  />
                  <FieldError id="api-key-name-error" message={errors.name} />
                </div>
                <fieldset className="space-y-2">
                  <legend className="text-sm font-medium">{t('purpose')}</legend>
                  <div className="grid gap-2">
                    {API_KEY_PURPOSES.map(option => (
                      <label
                        key={option}
                        className="flex cursor-pointer items-start gap-3 rounded-md border p-3 transition-colors hover:bg-muted/50 has-[:checked]:border-primary/50 has-[:checked]:bg-primary/5"
                      >
                        <input
                          type="radio"
                          name="api-key-purpose"
                          value={option}
                          className="mt-1"
                          checked={purpose === option}
                          aria-labelledby={`api-key-purpose-${option}-label`}
                          aria-describedby={`api-key-purpose-${option}-hint`}
                          onChange={() => setPurpose(option)}
                        />
                        <span className="min-w-0 space-y-0.5">
                          <span
                            id={`api-key-purpose-${option}-label`}
                            className="flex flex-wrap items-center gap-x-2 text-sm font-medium"
                          >
                            {t(`purposes.${option}.label`)}{' '}
                            {option === 'custom' ? null : (
                              <code className="text-xs font-normal text-muted-foreground">
                                ({API_KEY_PURPOSE_SCOPES[option].join(' + ')})
                              </code>
                            )}
                          </span>
                          <span
                            id={`api-key-purpose-${option}-hint`}
                            className="block text-sm text-muted-foreground"
                          >
                            {t(`purposes.${option}.description`)}
                          </span>
                        </span>
                      </label>
                    ))}
                  </div>
                </fieldset>
                {purpose === 'custom' ? (
                  <fieldset
                    className="space-y-2"
                    aria-describedby={errors.scopes ? 'api-key-scopes-error' : undefined}
                  >
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
                    <FieldError id="api-key-scopes-error" message={errors.scopes} />
                  </fieldset>
                ) : null}
                <fieldset className="space-y-2">
                  <legend className="text-sm font-medium">{t('expiresAt')}</legend>
                  <div className="flex flex-wrap gap-2">
                    {API_KEY_EXPIRY_CHOICES.map(option => (
                      <label
                        key={option}
                        className="flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors hover:bg-muted/50 has-[:checked]:border-primary/50 has-[:checked]:bg-primary/5"
                      >
                        <input
                          type="radio"
                          name="api-key-expiry-choice"
                          value={option}
                          checked={expiry === option}
                          onChange={() => setExpiry(option)}
                        />
                        {t(`expiry.${option}`)}
                      </label>
                    ))}
                  </div>
                  {expiry === 'custom' ? (
                    <div className="space-y-2">
                      <Label htmlFor="api-key-expiry">{t('expiry.date')}</Label>
                      <Input
                        id="api-key-expiry"
                        type="datetime-local"
                        value={expiresAt}
                        aria-invalid={errors.expiry ? true : undefined}
                        aria-describedby={errors.expiry ? 'api-key-expiry-error' : undefined}
                        onChange={event => setExpiresAt(event.target.value)}
                      />
                    </div>
                  ) : null}
                  <FieldError id="api-key-expiry-error" message={errors.expiry} />
                </fieldset>
                {errors.form ? (
                  <p role="alert" className="text-sm text-error">
                    {errors.form}
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
            <DialogDescription>
              {revokeTarget
                ? t('revokeDescription', {
                    name: revokeTarget.name,
                    prefix: revokeTarget.key_prefix,
                  })
                : null}
            </DialogDescription>
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

interface FormErrors {
  name?: string;
  scopes?: string;
  expiry?: string;
  form?: string;
}

function FieldError({ id, message }: { id: string; message?: string }) {
  if (!message) return null;
  return (
    <p id={id} className="text-sm text-error">
      {message}
    </p>
  );
}

function isValidName(value: string): boolean {
  const length = Array.from(value.trim()).length;
  return length >= 1 && length <= 100;
}

const DAY_MS = 24 * 60 * 60 * 1000;

/** The ISO expiry a choice resolves to, `null` for none, or why a custom date is unusable. */
function resolveExpiry(
  choice: ApiKeyExpiryChoice,
  customValue: string
): string | null | 'missing' | 'past' {
  if (choice === 'never') return null;
  if (choice !== 'custom') {
    return new Date(Date.now() + API_KEY_EXPIRY_PRESET_DAYS[choice] * DAY_MS).toISOString();
  }
  if (!customValue) return 'missing';
  const date = new Date(customValue);
  if (Number.isNaN(date.getTime())) return 'missing';
  if (date.getTime() <= Date.now()) return 'past';
  return date.toISOString();
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
