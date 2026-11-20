import Link from 'next/link';
import {
  Activity,
  ArrowUpRight,
  Code2,
  FileText,
  Inbox,
  KeyRound,
  Mail,
  Megaphone,
  Rocket,
  Users,
} from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { isDevtoolsEnabled } from '@/config/devtools';
import { ROUTES } from '@/constants/routes';
import { getSessionUser } from '@/features/auth/server/session';
import { getT } from '@/i18n/server';

import { GreetingClock } from './_components/greeting-clock';

/**
 * Console home (RSC): the Zmail areas a member works in, and the order a new
 * organization sets them up in.
 *
 * Pattern: page is a Server Component; the only client island is
 * `GreetingClock` which re-renders the wall-clock every minute.
 */
export default async function ConsoleHomePage() {
  const [user, t] = await Promise.all([getSessionUser(), getT('console')]);

  const quickLinks = [
    {
      title: t('home.quickLinks.emails.title'),
      description: t('home.quickLinks.emails.description'),
      href: ROUTES.CONSOLE.EMAILS,
      icon: Inbox,
    },
    {
      title: t('home.quickLinks.broadcasts.title'),
      description: t('home.quickLinks.broadcasts.description'),
      href: ROUTES.CONSOLE.MAIL_BROADCASTS,
      icon: Megaphone,
    },
    {
      title: t('home.quickLinks.contacts.title'),
      description: t('home.quickLinks.contacts.description'),
      href: ROUTES.CONSOLE.MAIL_CONTACTS,
      icon: Users,
    },
    {
      title: t('home.quickLinks.templates.title'),
      description: t('home.quickLinks.templates.description'),
      href: ROUTES.CONSOLE.MAIL_TEMPLATES,
      icon: FileText,
    },
    {
      title: t('home.quickLinks.mailboxes.title'),
      description: t('home.quickLinks.mailboxes.description'),
      href: ROUTES.CONSOLE.MAILBOXES,
      icon: Mail,
    },
    {
      title: t('home.quickLinks.apiAccess.title'),
      description: t('home.quickLinks.apiAccess.description'),
      href: ROUTES.CONSOLE.SETTINGS,
      icon: KeyRound,
    },
    ...(isDevtoolsEnabled()
      ? [
          {
            title: t('home.quickLinks.styleguide.title'),
            description: t('home.quickLinks.styleguide.description'),
            href: ROUTES.DEVTOOLS.STYLEGUIDE,
            icon: Code2,
          },
          {
            title: t('home.quickLinks.i18nTest.title'),
            description: t('home.quickLinks.i18nTest.description'),
            href: ROUTES.DEVTOOLS.I18N_TEST,
            icon: Activity,
          },
        ]
      : []),
  ];

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-8 md:px-8 md:py-12">
      <header className="mb-8 flex flex-wrap items-end justify-between gap-4">
        <div>
          <GreetingClock name={user?.name ?? user?.email ?? t('home.defaultUser')} />
          <p className="mt-1 text-sm text-text-muted">{t('home.welcomeDescription')}</p>
        </div>
        <Button asChild>
          <Link href={ROUTES.SITE.HOME}>
            {t('returnToSite')}
            <ArrowUpRight className="ml-1.5 h-4 w-4" />
          </Link>
        </Button>
      </header>

      <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {quickLinks.map(({ title, description, href, icon: Icon }, index) => (
          <Card
            key={href}
            className="animate-enter group transition-all duration-200 hover:-translate-y-0.5 hover:border-primary/40 hover:shadow-md"
            style={{ animationDelay: `${index * 40}ms` }}
          >
            <CardHeader className="flex flex-row items-start justify-between gap-3 space-y-0">
              <div>
                <CardTitle className="text-base">{title}</CardTitle>
                <CardDescription className="mt-1">{description}</CardDescription>
              </div>
              <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary transition-colors duration-200 group-hover:bg-primary group-hover:text-primary-foreground">
                <Icon className="h-4.5 w-4.5 transition-transform duration-200 group-hover:scale-105" />
              </div>
            </CardHeader>
            <CardContent>
              <Button asChild variant="ghost" size="sm" className="-ml-2">
                <Link href={href} aria-label={`${t('home.open')} ${title}`}>
                  {t('home.open')}
                  <ArrowUpRight className="ml-1 h-3.5 w-3.5 transition-transform duration-200 group-hover:translate-x-0.5 group-hover:-translate-y-0.5" />
                </Link>
              </Button>
            </CardContent>
          </Card>
        ))}
      </section>

      <section
        className="animate-enter mt-10 rounded-xl border bg-bg-surface p-6"
        style={{ animationDelay: `${quickLinks.length * 40}ms` }}
      >
        <div className="flex items-start gap-4">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <Rocket className="h-5 w-5" />
          </div>
          <div>
            <h2 className="text-base font-semibold">{t('home.gettingStartedTitle')}</h2>
            <ol className="mt-2 list-decimal space-y-1.5 pl-4 text-sm text-text-muted">
              {(
                [
                  ['domain', ROUTES.CONSOLE.MAIL_DOMAIN],
                  ['mailbox', ROUTES.CONSOLE.MAILBOXES],
                  ['contacts', ROUTES.CONSOLE.MAIL_CONTACTS],
                  ['template', ROUTES.CONSOLE.MAIL_TEMPLATES],
                  ['broadcast', ROUTES.CONSOLE.MAIL_BROADCASTS],
                ] as const
              ).map(([step, href]) => (
                <li key={step}>
                  <Link
                    href={href}
                    className="inline-flex items-center gap-1 transition-colors hover:text-foreground hover:underline"
                  >
                    <span>{t(`home.gettingStarted.${step}`)}</span>
                    <ArrowUpRight className="h-3.5 w-3.5 shrink-0 opacity-60" aria-hidden="true" />
                  </Link>
                </li>
              ))}
            </ol>
          </div>
        </div>
      </section>

      {isDevtoolsEnabled() ? (
        <section className="mt-10 rounded-xl border bg-bg-surface p-6">
          <div className="flex items-start gap-4">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <Rocket className="h-5 w-5" />
            </div>
            <div>
              <h2 className="text-base font-semibold">{t('home.developerStepsTitle')}</h2>
              <ul className="mt-2 space-y-1 text-sm text-text-muted">
                <li>
                  {t('home.developerSteps.apiBefore')}{' '}
                  <code className="font-mono text-xs">NEXT_PUBLIC_API_URL</code>{' '}
                  {t('home.developerSteps.apiAfter')}
                </li>
                <li>
                  {t('home.developerSteps.replaceBefore')}
                  <code className="font-mono text-xs">
                    src/app/(protected)/(console)/console/page.tsx
                  </code>
                  {t('home.developerSteps.replaceAfter')}
                </li>
                <li>
                  {t('home.developerSteps.featuresBefore')}{' '}
                  <code className="font-mono text-xs">src/features/</code>{' '}
                  {t('home.developerSteps.featuresBetween')}{' '}
                  <code className="font-mono text-xs">(protected)/(console)/</code>
                  {t('home.developerSteps.featuresAfter')}
                </li>
              </ul>
            </div>
          </div>
        </section>
      ) : null}
    </div>
  );
}
