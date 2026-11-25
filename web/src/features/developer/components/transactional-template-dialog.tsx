'use client';

import { useEffect, useId, useMemo, useState, type FormEvent } from 'react';
import Link from 'next/link';
import { AlertCircle, Save } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { ROUTES } from '@/constants/routes';
import { mailOrganizationHref } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import { useSaveMailTemplate } from '@/features/mailbox/hooks/use-mail-templates';
import { MAIL_TEMPLATE_NAME_MAX_LENGTH } from '@/features/mailbox/schemas';
import type { MailboxDomain, MailTemplate, MailTemplateInput } from '@/features/mailbox/types';
import {
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { templateFallbacks } from '@/features/mailbox/utils/template-render';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** The key rule of `contracts/MAILBOXES.md`. */
const templateKeyPattern = /^[a-z][a-z0-9-]{0,63}$/u;

interface Definition {
  secret: boolean;
  required: boolean;
}

interface Draft {
  name: string;
  key: string;
  localPart: string;
  domain: string;
  fromName: string;
  subject: string;
  html: string;
  text: string;
  samples: Record<string, string>;
  definitions: Record<string, Definition>;
}

type Field = 'subject' | 'html' | 'text';
const fields: readonly Field[] = ['subject', 'html', 'text'];

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&');
}

function placeholderPattern(name: string): RegExp {
  // The name, then either the end of the placeholder or a fallback up to the first `}}`.
  return new RegExp(
    `\\{\\{\\s*${escapeRegExp(name)}\\s*(?:\\|(?:(?!\\}\\})[^\\r\\n])*)?\\}\\}`,
    'gu'
  );
}

/** Rewrites every placeholder of `name`: without a fallback when it is null, else with it. */
function withFallback(source: string, name: string, fallback: string | null): string {
  const clean = fallback?.replace(/[{}\r\n]/gu, '') ?? null;
  return source.replace(placeholderPattern(name), () =>
    clean === null ? `{{${name}}}` : `{{${name}|${clean}}}`
  );
}

/** True when any placeholder of `name` in the source carries a fallback. */
function anyFallback(source: string, name: string): boolean {
  return [...source.matchAll(placeholderPattern(name))].some(match => match[0].includes('|'));
}

/** Domains a transactional template may send from: verified ones, and the log instance's own. */
function senderDomains(
  domains: readonly MailboxDomain[] | undefined,
  current: string,
  offers: (domain: MailboxDomain) => boolean
): string[] {
  const names = (domains ?? [])
    .filter(domain => domain.kind === 'organization' || domain.provider === 'log')
    .filter(offers)
    .map(domain => domain.domain);
  // A sender whose domain lost verification stays selectable so the template can still be saved.
  if (current !== '' && !names.includes(current)) names.push(current);
  return names;
}

function draftOf(template: MailTemplate | null): Draft {
  if (!template) {
    return {
      name: '',
      key: '',
      localPart: '',
      domain: '',
      fromName: '',
      subject: '',
      html: '',
      text: '',
      samples: {},
      definitions: {},
    };
  }
  const address = template.from?.address ?? '';
  const at = address.lastIndexOf('@');
  return {
    name: template.name,
    key: template.key ?? '',
    localPart: at < 0 ? address : address.slice(0, at),
    domain: at < 0 ? '' : address.slice(at + 1),
    fromName: template.from?.name ?? '',
    subject: template.subject,
    html: template.html_body,
    text: template.text_body,
    samples: { ...template.sample_variables },
    definitions: Object.fromEntries(
      template.variable_definitions.map(({ name, secret, required }) => [
        name,
        { secret, required },
      ])
    ),
  };
}

const offersEveryDomain = () => true;

const DRAFT_STORAGE_PREFIX = 'zmail.transactionalTemplateDraft.';

function draftStorageKey(organizationId: number): string {
  return `${DRAFT_STORAGE_PREFIX}${organizationId}`;
}

function readString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isEmptyDraft(draft: Draft): boolean {
  return (
    draft.name === '' &&
    draft.key === '' &&
    draft.localPart === '' &&
    draft.domain === '' &&
    draft.fromName === '' &&
    draft.subject === '' &&
    draft.html === '' &&
    draft.text === '' &&
    Object.values(draft.samples).every(value => value === '') &&
    Object.keys(draft.definitions).length === 0
  );
}

function readDraft(organizationId: number): Draft | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.sessionStorage.getItem(draftStorageKey(organizationId));
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    if (!isPlainRecord(parsed)) return null;

    const samples: Record<string, string> = {};
    if (isPlainRecord(parsed.samples)) {
      for (const [k, v] of Object.entries(parsed.samples)) {
        if (typeof v === 'string') samples[k] = v;
      }
    }

    const definitions: Record<string, Definition> = {};
    if (isPlainRecord(parsed.definitions)) {
      for (const [k, v] of Object.entries(parsed.definitions)) {
        if (
          isPlainRecord(v) &&
          typeof v.secret === 'boolean' &&
          typeof v.required === 'boolean'
        ) {
          definitions[k] = { secret: v.secret, required: v.required };
        }
      }
    }

    return {
      name: readString(parsed.name),
      key: readString(parsed.key),
      localPart: readString(parsed.localPart),
      domain: readString(parsed.domain),
      fromName: readString(parsed.fromName),
      subject: readString(parsed.subject),
      html: readString(parsed.html),
      text: readString(parsed.text),
      samples,
      definitions,
    };
  } catch {
    return null;
  }
}

function writeDraft(organizationId: number, draft: Draft): void {
  if (typeof window === 'undefined') return;
  try {
    window.sessionStorage.setItem(draftStorageKey(organizationId), JSON.stringify(draft));
  } catch {
    // Storage may be unavailable in restricted contexts; the dialog still works without persistence.
  }
}

function clearDraft(organizationId: number): void {
  if (typeof window === 'undefined') return;
  try {
    window.sessionStorage.removeItem(draftStorageKey(organizationId));
  } catch {
    // Storage may be unavailable in restricted contexts.
  }
}

/**
 * Creates or edits a transactional template: name, key, sender, content, and the declared
 * variables. A required variable has no fallback and every use of an optional one has one, so
 * the required and secret switches rewrite the placeholders; a secret is always required.
 */
export function TransactionalTemplateDialog({
  organizationId,
  template,
  canManage = false,
  onClose,
  onSaved,
  offersDomain = offersEveryDomain,
}: {
  organizationId: number;
  template: MailTemplate | null;
  canManage?: boolean;
  onClose: () => void;
  onSaved: (template: MailTemplate) => void;
  /** Narrows the sender domains, as a developer's scope does. */
  offersDomain?: (domain: MailboxDomain) => boolean;
}) {
  const t = useT('developer');
  const tm = useT('mailbox');
  const domains = useMailboxDomains(organizationId);
  const save = useSaveMailTemplate(organizationId);
  const [draft, setDraft] = useState<Draft>(() =>
    template ? draftOf(template) : (readDraft(organizationId) ?? draftOf(null))
  );
  const [keyError, setKeyError] = useState<'invalid' | 'taken' | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const ids = {
    name: useId(),
    key: useId(),
    keyHint: useId(),
    localPart: useId(),
    domain: useId(),
    fromName: useId(),
    subject: useId(),
    text: useId(),
    html: useId(),
  };

  const offered = senderDomains(domains.data, '', offersDomain);
  const staleDraftDomain =
    !template && domains.isSuccess && draft.domain !== '' && !offered.includes(draft.domain);

  useEffect(() => {
    if (staleDraftDomain) {
      setDraft(current => (current.domain === '' ? current : { ...current, domain: '' }));
    }
  }, [staleDraftDomain]);

  useEffect(() => {
    if (template) return;
    if (isEmptyDraft(draft)) clearDraft(organizationId);
    else writeDraft(organizationId, draft);
  }, [draft, organizationId, template]);

  const options = template ? senderDomains(domains.data, draft.domain, offersDomain) : offered;
  const selectedDomain = staleDraftDomain ? '' : draft.domain;
  const domain = selectedDomain !== '' ? selectedDomain : (options[0] ?? '');
  const source = { subject: draft.subject, html: draft.html, text: draft.text };
  const rows = useMemo(
    () => templateFallbacks({ subject: draft.subject, html: draft.html, text: draft.text }),
    [draft.subject, draft.html, draft.text]
  );
  const variableRows = (rows ?? []).map(row => ({
    ...row,
    definition: draft.definitions[row.name] ?? { secret: false, required: row.fallback === null },
  }));

  const update = (change: Partial<Draft>) => {
    setFormError(null);
    setDraft(current => ({ ...current, ...change }));
  };

  /** Applies a fallback to every field, keeping the rest of the draft. */
  const rewrite = (name: string, fallback: string | null, definition: Definition) => {
    setFormError(null);
    setDraft(current => ({
      ...current,
      ...Object.fromEntries(
        fields.map(field => [field, withFallback(current[field], name, fallback)])
      ),
      definitions: { ...current.definitions, [name]: definition },
    }));
  };

  const setRequired = (name: string, definition: Definition, required: boolean) =>
    rewrite(name, required ? null : '', { ...definition, required });

  const setSecret = (name: string, definition: Definition, secret: boolean) => {
    if (secret) rewrite(name, null, { secret: true, required: true });
    else
      update({ definitions: { ...draft.definitions, [name]: { ...definition, secret: false } } });
  };

  const input = (): MailTemplateInput | null => {
    const name = draft.name.trim();
    if (!templateKeyPattern.test(draft.key)) {
      setKeyError('invalid');
      return null;
    }
    if (
      name === '' ||
      Array.from(name).length > MAIL_TEMPLATE_NAME_MAX_LENGTH ||
      draft.subject.trim() === '' ||
      draft.localPart.trim() === '' ||
      domain === '' ||
      (draft.html.trim() === '' && draft.text.trim() === '') ||
      rows === null
    ) {
      setFormError(tm('errors.templateInvalid'));
      return null;
    }
    const mismatch = variableRows.some(row =>
      row.definition.required
        ? fields.some(field => anyFallback(source[field], row.name))
        : row.fallback === null
    );
    if (mismatch) {
      setFormError(t('dialog.fallbackMismatch'));
      return null;
    }
    const samples = Object.fromEntries(
      variableRows
        .map(row => [row.name, draft.samples[row.name] ?? ''] as const)
        .filter(([, value]) => value !== '')
    );
    return {
      name,
      subject: draft.subject,
      html: draft.html,
      text: draft.text,
      sample_variables: samples,
      design: null,
      kind: 'transactional',
      key: draft.key,
      from_address: `${draft.localPart.trim()}@${domain}`,
      from_name: draft.fromName.trim(),
      variable_definitions: variableRows.map(row => ({ name: row.name, ...row.definition })),
    };
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setKeyError(null);
    setFormError(null);
    const value = input();
    if (!value) return;
    try {
      const stored = await save.mutateAsync({
        input: value,
        ...(template ? { templateId: template.id } : {}),
      });
      if (!template) clearDraft(organizationId);
      toast.success(t(template ? 'templates.saved' : 'templates.created'));
      onSaved(stored);
      onClose();
    } catch (failure) {
      if (
        failure instanceof ApiError &&
        failure.errorCode === ApiErrorCode.MAILBOX_TEMPLATE_KEY_TAKEN
      ) {
        setKeyError('taken');
        return;
      }
      const key: MailboxErrorKey = resolveMailboxErrorKey(failure);
      setFormError(tm(key));
    }
  };

  const cancel = () => {
    if (!template) clearDraft(organizationId);
    onClose();
  };

  const noDomain = domains.isSuccess && options.length === 0;

  return (
    <Dialog open onOpenChange={open => (open ? undefined : onClose())}>
      <DialogContent size="xl">
        <form onSubmit={event => void submit(event)} className="flex min-h-0 flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t(template ? 'dialog.editTitle' : 'dialog.createTitle')}</DialogTitle>
            <DialogDescription>{t('dialog.description')}</DialogDescription>
          </DialogHeader>
          <DialogBody className="space-y-4">
            {noDomain ? (
              <Alert className="border-warning/40 bg-warning-subtle text-warning">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
                  <div className="space-y-1">
                    <p>{t('dialog.noDomain')}</p>
                    {canManage ? null : <p>{t('dialog.noDomainAdminHint')}</p>}
                  </div>
                  {canManage ? (
                    <Button asChild size="sm" variant="outline" className="shrink-0 text-foreground">
                      <Link
                        href={mailOrganizationHref(ROUTES.CONSOLE.MAIL_DOMAIN, organizationId)}
                        onClick={onClose}
                      >
                        {t('dialog.connectDomain')}
                      </Link>
                    </Button>
                  ) : null}
                </AlertDescription>
              </Alert>
            ) : null}
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor={ids.name}>{t('dialog.name')}</Label>
                <Input
                  id={ids.name}
                  value={draft.name}
                  maxLength={MAIL_TEMPLATE_NAME_MAX_LENGTH}
                  onChange={event => update({ name: event.target.value })}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor={ids.key}>{t('dialog.key')}</Label>
                <Input
                  id={ids.key}
                  value={draft.key}
                  className="font-mono"
                  maxLength={64}
                  aria-invalid={keyError !== null}
                  aria-describedby={ids.keyHint}
                  onChange={event => {
                    setKeyError(null);
                    update({ key: event.target.value });
                  }}
                />
                <p
                  id={ids.keyHint}
                  className={cn(
                    'text-xs',
                    keyError === null ? 'text-muted-foreground' : 'text-error'
                  )}
                >
                  {keyError === 'taken' ? tm('errors.templateKeyTaken') : t('dialog.keyHint')}
                </p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor={ids.localPart}>{t('dialog.localPart')}</Label>
                <div className="flex min-w-0 items-center gap-1.5">
                  <Input
                    id={ids.localPart}
                    value={draft.localPart}
                    className="min-w-0 flex-1"
                    onChange={event => update({ localPart: event.target.value })}
                  />
                  <span className="text-muted-foreground" aria-hidden="true">
                    @
                  </span>
                  <Select
                    value={domain}
                    disabled={options.length === 0}
                    onValueChange={value => update({ domain: value })}
                  >
                    <SelectTrigger
                      id={ids.domain}
                      aria-label={t('dialog.domain')}
                      className="min-w-0 flex-1"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {options.map(name => (
                        <SelectItem key={name} value={name}>
                          {name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor={ids.fromName}>{t('dialog.fromName')}</Label>
                <Input
                  id={ids.fromName}
                  value={draft.fromName}
                  maxLength={100}
                  onChange={event => update({ fromName: event.target.value })}
                />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor={ids.subject}>{t('dialog.subject')}</Label>
              <Input
                id={ids.subject}
                value={draft.subject}
                onChange={event => update({ subject: event.target.value })}
              />
            </div>
            <div className="grid gap-4 lg:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor={ids.text}>{t('dialog.text')}</Label>
                <Textarea
                  id={ids.text}
                  rows={6}
                  value={draft.text}
                  onChange={event => update({ text: event.target.value })}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor={ids.html}>{t('dialog.html')}</Label>
                <Textarea
                  id={ids.html}
                  rows={6}
                  className="font-mono text-xs"
                  value={draft.html}
                  onChange={event => update({ html: event.target.value })}
                />
              </div>
            </div>
            <fieldset className="space-y-2">
              <legend className="text-sm font-medium">{t('dialog.variables')}</legend>
              {variableRows.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t('dialog.variablesEmpty')}</p>
              ) : (
                <div className="overflow-x-auto rounded-md border">
                  <table className="w-full text-sm">
                    <thead className="text-left text-xs text-muted-foreground">
                      <tr>
                        <th className="px-3 py-2 font-medium">{t('dialog.variable')}</th>
                        <th className="px-3 py-2 font-medium">{t('dialog.required')}</th>
                        <th className="px-3 py-2 font-medium">{t('dialog.secret')}</th>
                        <th className="px-3 py-2 font-medium">{t('dialog.fallback')}</th>
                        <th className="px-3 py-2 font-medium">{t('dialog.sample')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {variableRows.map(row => {
                        const { definition } = row;
                        return (
                          <tr key={row.name} className="border-t">
                            <td className="px-3 py-2 font-mono text-xs">{row.name}</td>
                            <td className="px-3 py-2">
                              <Checkbox
                                aria-label={t('dialog.requiredFor', { name: row.name })}
                                checked={definition.required}
                                disabled={definition.secret}
                                onCheckedChange={checked =>
                                  setRequired(row.name, definition, checked === true)
                                }
                              />
                            </td>
                            <td className="px-3 py-2">
                              <Checkbox
                                aria-label={t('dialog.secretFor', { name: row.name })}
                                checked={definition.secret}
                                onCheckedChange={checked =>
                                  setSecret(row.name, definition, checked === true)
                                }
                              />
                            </td>
                            <td className="px-3 py-2">
                              <Input
                                aria-label={t('dialog.fallbackFor', { name: row.name })}
                                className="h-8 min-w-28"
                                value={row.fallback ?? ''}
                                disabled={definition.required || definition.secret}
                                onChange={event =>
                                  rewrite(row.name, event.target.value, definition)
                                }
                              />
                            </td>
                            <td className="px-3 py-2">
                              <Input
                                aria-label={t('dialog.sampleFor', { name: row.name })}
                                className="h-8 min-w-28"
                                value={draft.samples[row.name] ?? ''}
                                onChange={event =>
                                  update({
                                    samples: { ...draft.samples, [row.name]: event.target.value },
                                  })
                                }
                              />
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
              <p className="text-xs text-muted-foreground">{t('dialog.secretHint')}</p>
              <p className="text-xs text-muted-foreground">{t('dialog.sampleHint')}</p>
            </fieldset>
            {formError ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{formError}</AlertDescription>
              </Alert>
            ) : null}
          </DialogBody>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={cancel}>
              {t('dialog.cancel')}
            </Button>
            <Button
              type="submit"
              loading={save.isPending}
              disabled={save.isPending || noDomain}
              icon={<Save className="size-4" />}
            >
              {t('dialog.save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
