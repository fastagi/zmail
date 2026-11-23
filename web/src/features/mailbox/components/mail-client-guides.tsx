'use client';

import { Info } from 'lucide-react';

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useT } from '@/i18n';

const CLIENTS = ['ios', 'mac', 'outlook', 'thunderbird', 'android', 'foxmail'] as const;
const STEPS = ['step1', 'step2', 'step3', 'step4'] as const;

/** Step-by-step manual setup for the common mail apps, with what each one cannot do yet. */
export function MailClientGuides() {
  const t = useT('settings.mailClients.guides');

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('title')}</CardTitle>
        <CardDescription>{t('description')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <Tabs defaultValue="ios">
          <TabsList className="max-w-full justify-start overflow-x-auto">
            {CLIENTS.map(client => (
              <TabsTrigger key={client} value={client}>
                {t(`clients.${client}.name`)}
              </TabsTrigger>
            ))}
          </TabsList>
          {CLIENTS.map(client => (
            <TabsContent key={client} value={client} className="pt-2">
              <ol className="list-decimal space-y-2 pl-5 text-sm">
                {STEPS.map(step => (
                  <li key={step}>{t(`clients.${client}.${step}`)}</li>
                ))}
              </ol>
              {client === 'ios' || client === 'outlook' ? (
                <p className="mt-3 flex gap-2 text-sm text-muted-foreground">
                  <Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                  {t(`notes.${client}`)}
                </p>
              ) : null}
            </TabsContent>
          ))}
        </Tabs>
        <p className="flex gap-2 text-sm text-muted-foreground">
          <Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          {t('notes.shared')}
        </p>
      </CardContent>
    </Card>
  );
}
