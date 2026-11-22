import Link from 'next/link';
import { ArrowUpRight, FileText, Inbox, KeyRound, Mail, Megaphone, Users } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { ROUTES } from '@/constants/routes';
import { LazyOnboardingChecklist } from '@/features/mailbox/components/onboarding-checklist-lazy';
import { getT } from '@/i18n/server';

import { GreetingClock } from './_components/greeting-clock';

/**
 * Console home (RSC): the setup guide first, until the team is set up, then the Zmail areas a
 * member works in.
 *
 * Pattern: page is a Server Component; the client islands are `GreetingClock`, which greets the
 * signed-in user by name and re-renders the wall-clock every minute, and the lazy setup guide.
 */
export default async function ConsoleHomePage() {
  const t = await getT('console');

  const quickLinks = [
    {
      title: t('home.quickLinks.mailboxes.title'),
      description: t('home.quickLinks.mailboxes.description'),
      href: ROUTES.CONSOLE.MAILBOXES,
      icon: Mail,
    },
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
      title: t('home.quickLinks.apiAccess.title'),
      description: t('home.quickLinks.apiAccess.description'),
      href: ROUTES.CONSOLE.SETTINGS,
      icon: KeyRound,
    },
  ];

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-8 md:px-8 md:py-12">
      <header className="mb-8 flex flex-wrap items-end justify-between gap-4">
        <div>
          <GreetingClock />
          <p className="mt-1 text-sm text-text-muted">{t('home.welcomeDescription')}</p>
        </div>
        <Button asChild variant="ghost" size="sm">
          <Link href={ROUTES.SITE.HOME}>
            {t('returnToSite')}
            <ArrowUpRight className="ml-1.5 h-4 w-4" />
          </Link>
        </Button>
      </header>

      <LazyOnboardingChecklist />

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

    </div>
  );
}
