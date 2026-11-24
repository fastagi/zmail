'use client';

import { AlertCircle } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { MailHtmlFrame } from '@/features/mailbox/components/mail-shared';
import type { RenderedTemplate } from '@/features/mailbox/utils/template-render';
import { useT } from '@/i18n';

export function TemplatePreview({
  rendered,
  tab,
  onTabChange,
}: {
  rendered: RenderedTemplate | null;
  tab: 'html' | 'text';
  onTabChange: (tab: 'html' | 'text') => void;
}) {
  const t = useT('mailbox');

  return (
    <section
      aria-label={t('templates.preview')}
      className="min-w-0 space-y-3 lg:sticky lg:top-4 lg:self-start"
    >
      <h3 className="text-sm font-medium">{t('templates.preview')}</h3>
      {rendered ? (
        <>
          <p className="rounded-md border px-3 py-2 text-sm font-medium break-words">
            {rendered.subject || '\u00a0'}
          </p>
          {rendered.missing.length > 0 ? (
            <Alert>
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertDescription>
                {t('templates.missing', { names: rendered.missing.join(', ') })}
              </AlertDescription>
            </Alert>
          ) : null}
          <Tabs value={tab} onValueChange={value => onTabChange(value as 'html' | 'text')}>
            <TabsList>
              <TabsTrigger value="html">{t('templates.previewHtml')}</TabsTrigger>
              <TabsTrigger value="text">{t('templates.previewText')}</TabsTrigger>
            </TabsList>
            <TabsContent value="html" className="pt-2">
              <MailHtmlFrame html={rendered.html} title={t('templates.previewFrame')} />
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
        </>
      ) : (
        <Alert variant="destructive">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>{t('templates.syntaxError')}</AlertDescription>
        </Alert>
      )}
    </section>
  );
}
