'use client';

import { useId, useMemo, useState } from 'react';

import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { MailHtmlFrame } from '@/features/mailbox/components/mail-shared';
import {
  MAIL_BROADCAST_CONTACT_VARIABLES,
  MAIL_TEMPLATE_VALUE_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import type { MailTemplate } from '@/features/mailbox/types';
import { renderTemplate, templateFallbacks } from '@/features/mailbox/utils/template-render';
import { useT } from '@/i18n';

/** Live preview of the selected template with sample contact values and shared broadcast values. */
export function BroadcastPreview({
  template,
  values,
}: {
  template: MailTemplate;
  values: Record<string, string>;
}) {
  const t = useT('mailbox');
  const [chosenTab, setChosenTab] = useState<'html' | 'text' | null>(null);
  const rendered = useMemo(() => {
    const merged: Record<string, string> = {
      ...template.sample_variables,
      unsubscribe_url: template.sample_variables.unsubscribe_url || '#unsubscribe',
      ...Object.fromEntries(Object.entries(values).filter(([, value]) => value !== '')),
    };
    return renderTemplate(
      { subject: template.subject, html: template.html_body, text: template.text_body },
      merged
    );
  }, [template, values]);

  if (!rendered) return null;
  const hasHtml = template.html_body !== '';
  const tab = chosenTab ?? (hasHtml ? 'html' : 'text');

  return (
    <section
      aria-label={t('broadcasts.previewTitle')}
      className="min-w-0 space-y-3 lg:sticky lg:top-4 lg:self-start"
    >
      <div className="space-y-1">
        <h3 className="text-sm font-medium">{t('broadcasts.previewTitle')}</h3>
        <p className="text-xs text-muted-foreground">{t('broadcasts.previewHint')}</p>
      </div>
      <p className="rounded-md border px-3 py-2 text-sm font-medium break-words">
        {rendered.subject || '\u00A0'}
      </p>
      <Tabs value={tab} onValueChange={value => setChosenTab(value as 'html' | 'text')}>
        <TabsList>
          <TabsTrigger value="html">{t('templates.previewHtml')}</TabsTrigger>
          <TabsTrigger value="text">{t('templates.previewText')}</TabsTrigger>
        </TabsList>
        <TabsContent value="html" className="pt-2">
          <MailHtmlFrame html={rendered.html} title={t('broadcasts.previewTitle')} />
        </TabsContent>
        <TabsContent value="text" className="pt-2">
          {rendered.text ? (
            <pre className="max-h-96 overflow-auto rounded-md border p-3 font-sans text-sm break-words whitespace-pre-wrap">
              {rendered.text}
            </pre>
          ) : (
            <p className="text-sm text-muted-foreground">{t('templates.noText')}</p>
          )}
        </TabsContent>
      </Tabs>
    </section>
  );
}

const contactVariables: readonly string[] = MAIL_BROADCAST_CONTACT_VARIABLES;

/** The template's variables: contact fields are filled per message, the rest can be shared. */
export function SharedValues({
  template,
  values,
  disabled,
  onChange,
}: {
  template: MailTemplate;
  values: Record<string, string>;
  disabled: boolean;
  onChange: (name: string, value: string) => void;
}) {
  const t = useT('mailbox');
  const baseId = useId();
  const fields =
    templateFallbacks({
      subject: template.subject,
      html: template.html_body,
      text: template.text_body,
    }) ?? [];
  const fromContact = fields.filter(field => contactVariables.includes(field.name));
  const shared = fields.filter(field => !contactVariables.includes(field.name));
  return (
    <fieldset className="space-y-3 rounded-md border p-3">
      <legend className="px-1 text-sm font-medium">{t('broadcasts.values.title')}</legend>
      <p className="text-xs text-muted-foreground">
        {fromContact.length > 0
          ? t('broadcasts.values.fromContact', {
              names: fromContact.map(field => field.name).join(', '),
            })
          : t('broadcasts.values.fromContactNone')}
      </p>
      {shared.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('broadcasts.values.none')}</p>
      ) : (
        <>
          <p className="text-xs text-muted-foreground">{t('broadcasts.values.hint')}</p>
          {shared.map(field => {
            const id = `${baseId}-${field.name}`;
            const hintId = `${id}-hint`;
            const value = values[field.name] ?? '';
            const atRisk = value === '' && field.fallback === null;
            return (
              <div key={field.name} className="space-y-1">
                <Label htmlFor={id} className="font-mono text-xs">
                  {field.name}
                </Label>
                <Input
                  id={id}
                  value={value}
                  maxLength={MAIL_TEMPLATE_VALUE_MAX_LENGTH}
                  placeholder={
                    field.fallback === null
                      ? t('broadcasts.values.placeholder')
                      : t('broadcasts.values.fallback', { value: field.fallback })
                  }
                  aria-describedby={atRisk ? hintId : undefined}
                  disabled={disabled}
                  onChange={event => onChange(field.name, event.target.value)}
                />
                {atRisk ? (
                  <p id={hintId} className="text-xs text-amber-700 dark:text-amber-400">
                    {t('broadcasts.values.required', { name: field.name })}
                  </p>
                ) : null}
              </div>
            );
          })}
        </>
      )}
    </fieldset>
  );
}
