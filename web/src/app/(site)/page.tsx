import type { Metadata } from 'next';
import Link from 'next/link';
import { ArrowRight, Braces, Inbox, Megaphone } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { getT } from '@/i18n/server';

const receiptRequest = `POST /v1/outbound-mail-messages
X-API-Key: <key>
Organization-Id: <organization-id>
Idempotency-Key: receipt-1042
Content-Type: application/json

{
  "from": "billing@atlas.example",
  "to": ["maya@example.com"],
  "subject": "Receipt for order 1042",
  "text": "Thanks, Maya. We received your payment."
}`;
const previewTabClass =
  'relative flex min-h-10 cursor-pointer items-center justify-center gap-2 rounded-md px-1 text-center text-sm font-medium has-focus-visible:ring-2 has-focus-visible:ring-primary';
const previewPanelClass =
  'col-start-1 row-start-1 motion-safe:transition-opacity motion-safe:duration-200';

const workflows = [
  { key: 'inbox', icon: Inbox },
  { key: 'broadcast', icon: Megaphone },
  { key: 'api', icon: Braces },
] as const;

export async function generateMetadata(): Promise<Metadata> {
  const t = await getT('site');
  const title = t('hero.title');
  const description = t('hero.description');
  return { title, description, openGraph: { title: `Zmail · ${title}`, description } };
}

export default async function HomePage() {
  const t = await getT('site');

  return (
    <div className="container">
      <section className="grid items-center gap-10 py-14 md:py-20 lg:grid-cols-2 lg:gap-16 lg:py-28">
        <div>
          <h1 className="max-w-xl text-balance font-serif text-5xl leading-[1.05] tracking-tight sm:text-6xl lg:text-7xl">
            {t('hero.title')}
          </h1>
          <p className="mt-6 max-w-lg text-pretty text-lg leading-relaxed text-text-muted">
            {t('hero.description')}
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Button asChild size="lg">
              <Link href="/register">
                {t('hero.primary')}
                <ArrowRight aria-hidden="true" />
              </Link>
            </Button>
            <Button asChild variant="outline" size="lg">
              <a href="#sending-api">{t('hero.secondary')}</a>
            </Button>
          </div>
        </div>

        <figure className="min-w-0 rounded-3xl bg-primary/10 p-4 sm:p-8">
          <figcaption className="mb-3 text-xs font-medium text-text-muted">
            {t('preview.label')}
          </figcaption>
          <fieldset className="group min-w-0">
            <legend className="sr-only">{t('preview.label')}</legend>
            <div className="relative mb-3 grid grid-cols-2 rounded-lg bg-muted p-1">
              <span
                aria-hidden="true"
                className="pointer-events-none absolute inset-y-1 left-1 w-[calc(50%-0.25rem)] rounded-md bg-background shadow-sm motion-safe:transition-transform motion-safe:duration-200 group-has-[#preview-update:checked]:translate-x-full"
              />
              <label className={previewTabClass}>
                <input type="radio" className="sr-only" name="mail-preview" defaultChecked />
                <Inbox aria-hidden="true" className="size-4" />
                {t('preview.inbox')}
              </label>
              <label className={previewTabClass}>
                <input type="radio" className="sr-only" name="mail-preview" id="preview-update" />
                <Megaphone aria-hidden="true" className="size-4" />
                {t('preview.update')}
              </label>
            </div>
            <div className="grid overflow-hidden rounded-xl border border-border bg-bg-surface shadow-sm">
              <div
                className={`${previewPanelClass} group-has-[#preview-update:checked]:invisible group-has-[#preview-update:checked]:opacity-0`}
              >
                <p className="border-b border-border px-5 py-3 text-sm text-text-muted">
                  {t('preview.address')}
                </p>
                <div className="space-y-5 p-5 sm:p-7">
                  <h2 className="text-xl font-semibold tracking-tight">{t('preview.subject')}</h2>
                  <div>
                    <p className="text-sm font-semibold">{t('preview.sender')}</p>
                    <p className="mt-1 text-sm leading-relaxed text-text-muted">
                      {t('preview.question')}
                    </p>
                  </div>
                  <div className="border-s-2 border-primary ps-4">
                    <p className="text-sm font-semibold">{t('preview.team')}</p>
                    <p className="mt-1 text-sm leading-relaxed">{t('preview.reply')}</p>
                  </div>
                </div>
              </div>
              <div
                className={`${previewPanelClass} invisible opacity-0 group-has-[#preview-update:checked]:visible group-has-[#preview-update:checked]:opacity-100`}
              >
                <p className="border-b border-border px-5 py-3 text-sm text-text-muted">
                  {t('preview.address')}
                </p>
                <div className="space-y-5 p-5 sm:p-7">
                  <h2 className="text-xl font-semibold tracking-tight">
                    {t('preview.releaseSubject')}
                  </h2>
                  <p className="text-sm leading-relaxed">{t('preview.releaseBody')}</p>
                  <p className="text-sm text-text-muted">{t('preview.team')}</p>
                </div>
              </div>
            </div>
          </fieldset>
        </figure>
      </section>

      <section className="border-t border-border py-12 md:py-16">
        <h2 className="font-serif text-3xl tracking-tight md:text-4xl">{t('flows.heading')}</h2>
        <div className="mt-8 grid gap-8 md:grid-cols-3">
          {workflows.map(({ key, icon: Icon }) => (
            <div key={key}>
              <Icon className="mb-4 size-5 text-primary" aria-hidden="true" />
              <h3 className="text-lg font-semibold">{t(`flows.${key}.title`)}</h3>
              <p className="mt-2 max-w-md leading-relaxed text-text-muted">
                {t(`flows.${key}.description`)}
              </p>
            </div>
          ))}
        </div>
      </section>
      <section
        id="sending-api"
        className="grid scroll-mt-20 gap-6 border-t border-border py-12 md:grid-cols-2 md:py-16"
      >
        <div>
          <h2 className="font-serif text-3xl tracking-tight md:text-4xl">{t('sample.title')}</h2>
          <p className="mt-4 max-w-md leading-relaxed text-text-muted">{t('sample.description')}</p>
          <Button asChild variant="link" className="mt-4 px-0">
            <Link href="/console/developers">
              {t('sample.action')}
              <ArrowRight aria-hidden="true" />
            </Link>
          </Button>
        </div>
        <pre
          tabIndex={0}
          aria-label={t('sample.title')}
          className="min-w-0 overflow-x-auto rounded-xl border border-border bg-bg-subtle p-5 text-xs leading-relaxed sm:text-sm"
        >
          <code>{receiptRequest}</code>
        </pre>
      </section>
    </div>
  );
}
