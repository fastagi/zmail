import type { Metadata } from 'next';
import Link from 'next/link';
import {
  ArrowRight,
  BookOpen,
  CheckCircle2,
  KeyRound,
  Mail,
  RefreshCw,
  ShieldCheck,
  Sparkles,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { DocsCodeTabs } from '@/components/features/site/docs-code-tabs';
import { ROUTES } from '@/constants/routes';
import { getT } from '@/i18n/server';
import {
  DIRECT_SEND_CURL,
  DIRECT_SEND_GO,
  DIRECT_SEND_NODE,
  DIRECT_SEND_PYTHON,
  DIRECT_SEND_RESPONSE,
  IDEMPOTENCY_CURL,
  STATUS_QUERY_CURL,
  STATUS_QUERY_RESPONSE,
  TEMPLATE_SEND_CURL,
  TEMPLATE_SEND_GO,
  TEMPLATE_SEND_NODE,
  TEMPLATE_SEND_PYTHON,
} from './snippets';

export async function generateMetadata(): Promise<Metadata> {
  const t = await getT('docs');
  const title = t('meta.title');
  const description = t('meta.description');
  return {
    title,
    description,
    openGraph: { title: `Zmail · ${title}`, description },
  };
}

const STATUS_ITEMS = [
  { key: 'queued', badgeClass: 'bg-muted text-text-subtle border-border' },
  { key: 'sent', badgeClass: 'bg-sky-500/10 text-sky-700 dark:text-sky-300 border-sky-500/20' },
  {
    key: 'accepted',
    badgeClass: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 border-emerald-500/20',
  },
  {
    key: 'bounced',
    badgeClass: 'bg-amber-500/10 text-amber-700 dark:text-amber-300 border-amber-500/20',
  },
  { key: 'failed', badgeClass: 'bg-rose-500/10 text-rose-700 dark:text-rose-300 border-rose-500/20' },
] as const;

export default async function DocsPage() {
  const t = await getT('docs');

  const tocItems = [
    { id: 'quickstart', name: t('toc.quickstart') },
    { id: 'templates', name: t('toc.templates') },
    { id: 'status-idempotency', name: t('toc.statusIdempotency') },
    { id: 'auth-permissions', name: t('toc.authPermissions') },
    { id: 'enterprise-mail', name: t('toc.enterpriseMail') },
  ];

  const copyLabel = t('code.copy');
  const copiedLabel = t('code.copied');

  const directTabs = [
    { id: 'node', label: t('code.tabs.node'), code: DIRECT_SEND_NODE },
    { id: 'curl', label: t('code.tabs.curl'), code: DIRECT_SEND_CURL },
    { id: 'python', label: t('code.tabs.python'), code: DIRECT_SEND_PYTHON },
    { id: 'go', label: t('code.tabs.go'), code: DIRECT_SEND_GO },
  ];

  const templateTabs = [
    { id: 'node', label: t('code.tabs.node'), code: TEMPLATE_SEND_NODE },
    { id: 'curl', label: t('code.tabs.curl'), code: TEMPLATE_SEND_CURL },
    { id: 'python', label: t('code.tabs.python'), code: TEMPLATE_SEND_PYTHON },
    { id: 'go', label: t('code.tabs.go'), code: TEMPLATE_SEND_GO },
  ];

  const statusTabs = [
    { id: 'curl', label: t('code.tabs.curl'), code: STATUS_QUERY_CURL },
    { id: 'json', label: t('code.tabs.statusJson'), code: STATUS_QUERY_RESPONSE },
  ];

  const enterpriseSteps = [
    {
      num: t('enterprise.steps.step1Num'),
      name: t('enterprise.steps.step1Title'),
      body: t('enterprise.steps.step1Desc'),
    },
    {
      num: t('enterprise.steps.step2Num'),
      name: t('enterprise.steps.step2Title'),
      body: t('enterprise.steps.step2Desc'),
    },
    {
      num: t('enterprise.steps.step3Num'),
      name: t('enterprise.steps.step3Title'),
      body: t('enterprise.steps.step3Desc'),
    },
  ];

  return (
    <div className="container max-w-6xl py-10 md:py-16">
      <div className="lg:grid lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-12">
        {/* Sticky desktop TOC */}
        <aside className="hidden lg:block">
          <nav aria-label={t('toc.heading')} className="sticky top-24 space-y-6">
            <div>
              <p className="mb-3 text-xs font-semibold uppercase tracking-wider text-text-muted">
                {t('toc.heading')}
              </p>
              <ul className="space-y-1 border-l border-border/70">
                {tocItems.map(item => (
                  <li key={item.id}>
                    <a
                      href={`#${item.id}`}
                      className="-ml-px block border-l border-transparent py-1.5 pl-4 text-sm text-text-muted transition-colors hover:border-primary hover:text-foreground"
                    >
                      {item.name}
                    </a>
                  </li>
                ))}
              </ul>
            </div>

            <div className="rounded-xl border border-border/70 bg-bg-surface/70 p-4">
              <p className="text-xs font-medium text-foreground">{t('cta.heading')}</p>
              <div className="mt-3 flex flex-col gap-2">
                <Button asChild size="sm" className="w-full justify-between">
                  <Link href={ROUTES.CONSOLE.DEVELOPERS}>
                    <span>{t('hero.primaryCta')}</span>
                    <ArrowRight className="size-3.5" aria-hidden="true" />
                  </Link>
                </Button>
              </div>
            </div>
          </nav>
        </aside>

        {/* Main documentation column */}
        <article className="min-w-0 max-w-3xl">
          {/* Hero */}
          <header className="border-b border-border/60 pb-10">
            <div className="inline-flex items-center gap-2 rounded-full border border-border/70 bg-bg-surface px-3 py-1 text-xs font-medium text-text-subtle">
              <BookOpen className="size-3.5 text-primary" aria-hidden="true" />
              <span>{t('hero.eyebrow')}</span>
            </div>
            <h1 className="mt-4 font-serif text-4xl tracking-tight sm:text-5xl">
              {t('hero.title')}
            </h1>
            <p className="mt-4 text-pretty text-base leading-relaxed text-text-muted sm:text-lg">
              {t('hero.description')}
            </p>
            <div className="mt-6 flex flex-wrap items-center gap-3">
              <Button asChild>
                <Link href={ROUTES.CONSOLE.DEVELOPERS}>
                  {t('hero.primaryCta')}
                  <ArrowRight className="size-4" aria-hidden="true" />
                </Link>
              </Button>
              <Button asChild variant="outline">
                <Link href={ROUTES.CONSOLE.MAILBOXES}>{t('hero.secondaryCta')}</Link>
              </Button>
            </div>

            {/* Mobile TOC */}
            <nav aria-label={t('toc.heading')} className="mt-8 flex flex-wrap gap-2 lg:hidden">
              {tocItems.map(item => (
                <a
                  key={item.id}
                  href={`#${item.id}`}
                  className="rounded-full border border-border/70 bg-bg-surface px-3 py-1 text-xs font-medium text-text-subtle transition-colors hover:border-primary/50 hover:text-primary"
                >
                  {item.name}
                </a>
              ))}
            </nav>
          </header>

          {/* Section 1: 30-Second Quickstart */}
          <section id="quickstart" className="scroll-mt-24 border-b border-border/60 py-12">
            <p className="font-mono text-xs font-medium text-primary">{t('quickstart.badge')}</p>
            <h2 className="mt-2 text-2xl font-semibold tracking-tight sm:text-3xl">
              {t('quickstart.heading')}
            </h2>
            <p className="mt-3 leading-relaxed text-text-muted">{t('quickstart.summary')}</p>

            {/* Endpoint pill */}
            <div className="mt-6 inline-flex max-w-full flex-wrap items-center gap-2 rounded-lg border border-border/80 bg-bg-surface px-3 py-2 font-mono text-xs sm:text-sm">
              <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 font-semibold text-emerald-700 dark:text-emerald-300">
                <code>POST</code>
              </span>
              <code className="break-all text-foreground">https://zmail.im/v1/emails</code>
            </div>

            {/* Key highlights */}
            <div className="mt-6 grid gap-3 sm:grid-cols-3">
              <div className="rounded-xl border border-border/70 bg-bg-surface/60 p-4">
                <p className="text-xs font-semibold text-foreground">
                  {t('quickstart.highlights.endpointLabel')}
                </p>
                <p className="mt-1.5 text-xs leading-relaxed text-text-muted">
                  {t('quickstart.highlights.endpointDesc')}
                </p>
              </div>
              <div className="rounded-xl border border-border/70 bg-bg-surface/60 p-4">
                <p className="text-xs font-semibold text-foreground">
                  {t('quickstart.highlights.authLabel')}
                </p>
                <p className="mt-1.5 text-xs leading-relaxed text-text-muted">
                  {t('quickstart.highlights.authDesc')}
                </p>
              </div>
              <div className="rounded-xl border border-border/70 bg-bg-surface/60 p-4">
                <p className="text-xs font-semibold text-foreground">
                  {t('quickstart.highlights.domainLabel')}
                </p>
                <p className="mt-1.5 text-xs leading-relaxed text-text-muted">
                  {t('quickstart.highlights.domainDesc')}
                </p>
              </div>
            </div>

            <div className="mt-6 space-y-4">
              <DocsCodeTabs
                tabs={directTabs}
                copyLabel={copyLabel}
                copiedLabel={copiedLabel}
                ariaLabel={t('code.aria.directSend')}
              />

              <div className="pt-2">
                <h3 className="text-sm font-semibold text-foreground">
                  {t('quickstart.responseHeading')}
                </h3>
                <p className="mt-1 mb-3 text-sm text-text-muted">
                  {t('quickstart.responseNote', { id: '{id}' })}
                </p>
                <DocsCodeTabs
                  tabs={[
                    { id: 'response', label: t('code.tabs.json'), code: DIRECT_SEND_RESPONSE },
                  ]}
                  copyLabel={copyLabel}
                  copiedLabel={copiedLabel}
                  ariaLabel={t('code.aria.directResponse')}
                />
              </div>
            </div>
          </section>

          {/* Section 2: Template Sending */}
          <section id="templates" className="scroll-mt-24 border-b border-border/60 py-12">
            <p className="font-mono text-xs font-medium text-primary">{t('templates.badge')}</p>
            <h2 className="mt-2 text-2xl font-semibold tracking-tight sm:text-3xl">
              {t('templates.heading')}
            </h2>
            <p className="mt-3 leading-relaxed text-text-muted">{t('templates.summary')}</p>

            <div className="mt-6 grid gap-4 sm:grid-cols-2">
              <div className="rounded-xl border border-border/70 bg-bg-surface/60 p-5">
                <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                  <Sparkles className="size-4 text-primary" aria-hidden="true" />
                  <span>{t('templates.reasons.nonEngineerTitle')}</span>
                </div>
                <p className="mt-2 text-sm leading-relaxed text-text-muted">
                  {t('templates.reasons.nonEngineerDesc')}
                </p>
              </div>
              <div className="rounded-xl border border-border/70 bg-bg-surface/60 p-5">
                <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                  <ShieldCheck className="size-4 text-primary" aria-hidden="true" />
                  <span>{t('templates.reasons.maskingTitle')}</span>
                </div>
                <p className="mt-2 text-sm leading-relaxed text-text-muted">
                  {t('templates.reasons.maskingDesc')}
                </p>
              </div>
            </div>

            <div className="mt-6">
              <DocsCodeTabs
                tabs={templateTabs}
                copyLabel={copyLabel}
                copiedLabel={copiedLabel}
                ariaLabel={t('code.aria.templateSend')}
              />
            </div>
          </section>

          {/* Section 3: Status & Idempotency */}
          <section id="status-idempotency" className="scroll-mt-24 border-b border-border/60 py-12">
            <p className="font-mono text-xs font-medium text-primary">{t('status.badge')}</p>
            <h2 className="mt-2 text-2xl font-semibold tracking-tight sm:text-3xl">
              {t('status.heading')}
            </h2>
            <p className="mt-3 leading-relaxed text-text-muted">{t('status.summary')}</p>

            <div className="mt-6 inline-flex max-w-full flex-wrap items-center gap-2 rounded-lg border border-border/80 bg-bg-surface px-3 py-2 font-mono text-xs sm:text-sm">
              <span className="rounded bg-sky-500/15 px-1.5 py-0.5 font-semibold text-sky-700 dark:text-sky-300">
                <code>GET</code>
              </span>
              <code className="break-all text-foreground">https://zmail.im/v1/emails/{'{id}'}</code>
            </div>

            <div className="mt-5">
              <p className="mb-3 text-sm text-text-muted">
                {t('status.queryDesc', { id: '{id}' })}
              </p>
              <DocsCodeTabs
                tabs={statusTabs}
                copyLabel={copyLabel}
                copiedLabel={copiedLabel}
                ariaLabel={t('code.aria.statusQuery')}
              />
            </div>

            {/* Status table */}
            <div className="mt-8">
              <h3 className="text-base font-semibold text-foreground">
                {t('status.lifecycleHeading')}
              </h3>
              <div className="mt-3 divide-y divide-border/60 overflow-hidden rounded-xl border border-border/70 bg-bg-surface/50">
                {STATUS_ITEMS.map(({ key, badgeClass }) => (
                  <div
                    key={key}
                    className="flex flex-col gap-1.5 px-4 py-3 sm:flex-row sm:items-center sm:gap-4"
                  >
                    <span
                      className={`inline-flex w-fit shrink-0 items-center rounded-md border px-2 py-0.5 font-mono text-xs font-medium ${badgeClass}`}
                    >
                      {t(`status.statuses.${key}.name`)}
                    </span>
                    <p className="text-sm text-text-muted">{t(`status.statuses.${key}.desc`)}</p>
                  </div>
                ))}
              </div>
            </div>

            {/* Idempotency-Key */}
            <div className="mt-8 space-y-4">
              <div className="flex items-center gap-2">
                <RefreshCw className="size-4 text-primary" aria-hidden="true" />
                <h3 className="text-base font-semibold text-foreground">
                  {t('status.idempotencyHeading')}
                </h3>
              </div>
              <ul className="space-y-2.5 text-sm leading-relaxed text-text-muted">
                <li className="flex items-start gap-2.5">
                  <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />
                  <span>{t('status.idempotencyPoints.omit')}</span>
                </li>
                <li className="flex items-start gap-2.5">
                  <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />
                  <span>{t('status.idempotencyPoints.reuse')}</span>
                </li>
                <li className="flex items-start gap-2.5">
                  <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />
                  <span>{t('status.idempotencyPoints.conflict')}</span>
                </li>
              </ul>
              <DocsCodeTabs
                tabs={[{ id: 'idempotency', label: t('code.tabs.curl'), code: IDEMPOTENCY_CURL }]}
                copyLabel={copyLabel}
                copiedLabel={copiedLabel}
                ariaLabel={t('status.idempotencyHeading')}
              />
            </div>
          </section>

          {/* Section 4: Auth & Permissions */}
          <section id="auth-permissions" className="scroll-mt-24 border-b border-border/60 py-12">
            <p className="font-mono text-xs font-medium text-primary">{t('auth.badge')}</p>
            <h2 className="mt-2 text-2xl font-semibold tracking-tight sm:text-3xl">
              {t('auth.heading')}
            </h2>
            <p className="mt-3 leading-relaxed text-text-muted">{t('auth.summary')}</p>

            <div className="mt-6 space-y-3">
              <h3 className="text-sm font-semibold text-foreground">{t('auth.headersHeading')}</h3>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-xl border border-border/70 bg-bg-surface/60 p-4">
                  <div className="flex flex-wrap items-center gap-2">
                    <KeyRound className="size-4 text-primary" aria-hidden="true" />
                    <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-primary">
                      {t('auth.bearerBadge')}
                    </span>
                  </div>
                  <p className="mt-2 break-all font-mono text-xs text-foreground">
                    {t('auth.bearerTitle')}
                  </p>
                  <p className="mt-2 text-xs leading-relaxed text-text-muted">
                    {t('auth.bearerDesc')}
                  </p>
                </div>

                <div className="rounded-xl border border-border/70 bg-bg-surface/60 p-4">
                  <KeyRound className="size-4 text-text-muted" aria-hidden="true" />
                  <p className="mt-2 break-all font-mono text-xs text-foreground">
                    {t('auth.xApiKeyTitle')}
                  </p>
                  <p className="mt-2 text-xs leading-relaxed text-text-muted">
                    {t('auth.xApiKeyDesc')}
                  </p>
                </div>
              </div>
            </div>

            <div className="mt-6 space-y-2 rounded-xl border border-border/70 bg-bg-surface/50 p-5">
              <h3 className="text-sm font-semibold text-foreground">{t('auth.orgScopeHeading')}</h3>
              <ul className="list-disc space-y-1.5 pl-5 text-sm leading-relaxed text-text-muted marker:text-text-muted">
                <li>{t('auth.orgBound')}</li>
                <li>{t('auth.legacyMultiOrg')}</li>
              </ul>
            </div>

            <div className="mt-6 rounded-xl border border-border/70 bg-bg-surface/50 p-5">
              <h3 className="text-sm font-semibold text-foreground">{t('auth.grantHeading')}</h3>
              <p className="mt-2 text-sm leading-relaxed text-text-muted">{t('auth.grantDesc')}</p>
              <Button asChild variant="link" className="mt-2 h-auto px-0 text-sm">
                <Link href={ROUTES.CONSOLE.ORGANIZATIONS}>
                  {t('auth.grantCta')}
                  <ArrowRight className="size-3.5" aria-hidden="true" />
                </Link>
              </Button>
            </div>
          </section>

          {/* Section 5: Enterprise Mailboxes */}
          <section id="enterprise-mail" className="scroll-mt-24 border-b border-border/60 py-12">
            <p className="font-mono text-xs font-medium text-primary">{t('enterprise.badge')}</p>
            <h2 className="mt-2 text-2xl font-semibold tracking-tight sm:text-3xl">
              {t('enterprise.heading')}
            </h2>
            <p className="mt-3 leading-relaxed text-text-muted">{t('enterprise.summary')}</p>

            <ol className="mt-6 space-y-4">
              {enterpriseSteps.map(step => (
                <li
                  key={step.num}
                  className="flex gap-4 rounded-xl border border-border/70 bg-bg-surface/60 p-5"
                >
                  <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 font-mono text-xs font-semibold text-primary">
                    {step.num}
                  </span>
                  <div className="min-w-0">
                    <h3 className="text-base font-semibold text-foreground">{step.name}</h3>
                    <p className="mt-1.5 text-sm leading-relaxed text-text-muted">{step.body}</p>
                  </div>
                </li>
              ))}
            </ol>
          </section>

          {/* Bottom CTA */}
          <section className="pt-12">
            <div className="rounded-2xl border border-border/80 bg-bg-surface p-6 sm:p-8">
              <div className="flex items-center gap-2 text-primary">
                <Mail className="size-5" aria-hidden="true" />
                <span className="text-sm font-semibold">Zmail</span>
              </div>
              <h2 className="mt-3 text-xl font-semibold tracking-tight sm:text-2xl">
                {t('cta.heading')}
              </h2>
              <p className="mt-2 max-w-xl text-sm leading-relaxed text-text-muted">
                {t('cta.description')}
              </p>
              <div className="mt-6 flex flex-wrap items-center gap-3">
                <Button asChild>
                  <Link href={ROUTES.CONSOLE.DEVELOPERS}>
                    {t('cta.developers')}
                    <ArrowRight className="size-4" aria-hidden="true" />
                  </Link>
                </Button>
                <Button asChild variant="outline">
                  <Link href={ROUTES.CONSOLE.MAILBOXES}>{t('cta.mailboxes')}</Link>
                </Button>
              </div>
            </div>
          </section>
        </article>
      </div>
    </div>
  );
}
