'use client';

import { lazy, Suspense } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import {
  LucideIcon,
  Settings,
  Home,
  Bell,
  LogOut,
  ArrowUpLeft,
  Palette,
  Building2,
  FolderOpen,
  Gauge,
  Mail,
  Send,
  FileText,
  Users,
  Megaphone,
  Code2,
  // fastbuild:slot web.console.nav-icons
} from 'lucide-react';

import { cn } from '@/utils';
import { Logo } from '@/components/ui/icons';
import { ROUTES } from '@/constants/routes';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Button } from '@/components/ui/button';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { ThemeToggle } from '@/components/theme-toggle';
import { LanguageSwitcher } from '@/components/common';
import { useT, type AllTranslationKeys } from '@/i18n';
import { useLogout } from '@/features/auth/hooks/use-auth';
import { useAuthStore } from '@/features/auth/store/auth-store';
import { isWebFeatureEnabled } from '@/config/features';
import { isDevtoolsEnabled } from '@/config/devtools';

const OrganizationSwitcher = lazy(async () => {
  const organizationFeature =
    await import('@/features/organization/components/organization-switcher');
  return { default: organizationFeature.OrganizationSwitcher };
});

const AdminEntryLink = lazy(async () => {
  const adminFeature = await import('@/features/mailbox/components/admin/admin-entry-link');
  return { default: adminFeature.AdminEntryLink };
});

const NotificationCenter = lazy(async () => {
  const notificationFeature =
    await import('@/features/notification/components/notification-center');
  return { default: notificationFeature.NotificationCenter };
});

interface NavItem {
  titleKey: Extract<AllTranslationKeys, `nav.${string}`>;
  href: string;
  icon: LucideIcon;
}

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const compactNav = isNavigationItemActive(pathname, ROUTES.CONSOLE.MAILBOXES);
  const navLabelClass = cn(
    'whitespace-nowrap',
    compactNav && 'opacity-0 motion-safe:transition-opacity group-hover/rail:opacity-100 group-focus-within/rail:opacity-100'
  );
  const t = useT();
  const user = useAuthStore.use.user();
  const { mutate: logout, isPending: isLoggingOut } = useLogout();
  const organizationEnabled = isWebFeatureEnabled('organization');
  const notificationEnabled = isWebFeatureEnabled('notification');
  const assetEnabled = isWebFeatureEnabled('asset');
  const usageEnabled = isWebFeatureEnabled('usage');

  const mainNavItems: NavItem[] = [
    {
      titleKey: 'nav.console',
      href: ROUTES.CONSOLE.HOME,
      icon: Home,
    },
    ...(organizationEnabled
      ? [
          {
            titleKey: 'nav.organizations' as const,
            href: ROUTES.CONSOLE.ORGANIZATIONS,
            icon: Building2,
          },
        ]
      : []),
    ...(assetEnabled
      ? [
          {
            titleKey: 'nav.assets' as const,
            href: ROUTES.CONSOLE.ASSETS,
            icon: FolderOpen,
          },
        ]
      : []),
    ...(usageEnabled
      ? [
          {
            titleKey: 'nav.usage' as const,
            href: ROUTES.CONSOLE.USAGE,
            icon: Gauge,
          },
        ]
      : []),
    ...(isWebFeatureEnabled('mailbox')
      ? [
          {
            titleKey: 'nav.emails' as const,
            href: ROUTES.CONSOLE.EMAILS,
            icon: Send,
          },
          {
            titleKey: 'nav.mailboxes' as const,
            href: ROUTES.CONSOLE.MAILBOXES,
            icon: Mail,
          },
          {
            titleKey: 'nav.mailBroadcasts' as const,
            href: ROUTES.CONSOLE.MAIL_BROADCASTS,
            icon: Megaphone,
          },
          {
            titleKey: 'nav.mailContacts' as const,
            href: ROUTES.CONSOLE.MAIL_CONTACTS,
            icon: Users,
          },
          {
            titleKey: 'nav.mailTemplates' as const,
            href: ROUTES.CONSOLE.MAIL_TEMPLATES,
            icon: FileText,
          },
          {
            titleKey: 'nav.developers' as const,
            href: ROUTES.CONSOLE.DEVELOPERS,
            icon: Code2,
          },
        ]
      : []),
    // fastbuild:slot web.console.nav
  ];

  const secondaryNavItems: NavItem[] = [
    ...(isDevtoolsEnabled()
      ? [
          {
            titleKey: 'nav.styleguide' as const,
            href: ROUTES.DEVTOOLS.STYLEGUIDE,
            icon: Palette,
          },
        ]
      : []),
    {
      titleKey: 'nav.settings',
      href: ROUTES.CONSOLE.SETTINGS,
      icon: Settings,
    },
  ];

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-bg-canvas text-text-main">
      {/* Fixed Header */}
      <header className="flex h-16 shrink-0 items-center justify-between border-b bg-bg-surface px-4 md:px-6 shadow-sm z-50">
        <div className="flex min-w-0 items-center gap-2 sm:gap-4">
          <Link href={ROUTES.SITE.HOME} className="flex items-center gap-2.5 group">
            <Logo className="h-8 w-8 text-primary transition-transform group-hover:scale-105" />
            <span className="hidden text-xl font-bold sm:inline">Zmail</span>
          </Link>
          {organizationEnabled ? (
            <Suspense
              fallback={<div className="h-8 w-9 rounded-md border bg-muted" aria-hidden="true" />}
            >
              <OrganizationSwitcher />
            </Suspense>
          ) : null}
          {organizationEnabled && isWebFeatureEnabled('mailbox') ? (
            <Suspense fallback={null}>
              <AdminEntryLink />
            </Suspense>
          ) : null}
        </div>

        <div className="flex shrink-0 items-center gap-1 sm:gap-2">
          <div className="hidden sm:block">
            <LanguageSwitcher />
          </div>
          <ThemeToggle />

          {notificationEnabled ? (
            <Suspense
              fallback={
                <Button variant="ghost" isIcon className="h-9 w-9 rounded-full" disabled>
                  <Bell className="h-4 w-4 text-text-muted" />
                  <span className="sr-only">{t('console.notifications')}</span>
                </Button>
              }
            >
              <NotificationCenter />
            </Suspense>
          ) : null}

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                isIcon
                noScale
                className="h-9 w-9 rounded-full overflow-hidden border border-border/50 hover:border-primary/50 transition-colors"
              >
                <Avatar className="h-full w-full">
                  <AvatarFallback aria-hidden="true" className="bg-primary/10 text-primary">
                    {(user?.name || user?.email || 'ZM')
                      .split(/[\s@._-]+/)
                      .filter(Boolean)
                      .map(part => part[0])
                      .join('')
                      .slice(0, 2)
                      .toUpperCase()}
                  </AvatarFallback>
                </Avatar>
                <span className="sr-only">{t('console.profile')}</span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-56 rounded-xl p-1 shadow-premium">
              <div className="px-2 py-1.5 text-xs font-medium text-text-muted truncate">
                {user?.name || user?.email || t('nav.profile')}
              </div>
              <DropdownMenuItem asChild className="rounded-lg cursor-pointer">
                <Link href={ROUTES.CONSOLE.SETTINGS}>{t('nav.profile')}</Link>
              </DropdownMenuItem>
              <DropdownMenuItem asChild className="rounded-lg cursor-pointer">
                <Link href={ROUTES.CONSOLE.SETTINGS}>{t('nav.settings')}</Link>
              </DropdownMenuItem>
              <div className="h-px bg-border/50 my-1" />
              <DropdownMenuItem
                className="rounded-lg cursor-pointer text-error focus:bg-destructive/10"
                onSelect={event => {
                  event.preventDefault();
                  logout();
                }}
                disabled={isLoggingOut}
              >
                <div className="flex w-full items-center">
                  <LogOut className="mr-2 h-4 w-4" />
                  <span>{t('auth.logout')}</span>
                </div>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      {/* Body: Sidebar + Main Content */}
      <div className="flex h-0 grow overflow-hidden">
        {/* Fixed Sidebar */}
        <div className={cn('relative hidden shrink-0 md:block', compactNav ? 'w-14' : 'w-[220px]')}>
          <aside
            className={cn(
              'absolute inset-y-0 left-0 z-40 flex flex-col overflow-hidden border-r bg-background',
              compactNav
                ? 'group/rail w-14 motion-safe:transition-[width,box-shadow] motion-safe:duration-200 hover:w-[220px] hover:shadow-lg focus-within:w-[220px] focus-within:shadow-lg'
                : 'w-full'
            )}
          >
            <div className="flex flex-1 flex-col overflow-x-hidden overflow-y-auto py-2">
              <nav className="grid items-start px-2 text-sm font-medium">
                {mainNavItems.map(item => {
                  const IconComponent = item.icon;
                  const isActive = isNavigationItemActive(pathname, item.href);
                  return (
                    <Link
                      key={item.href}
                      href={item.href}
                      aria-current={isActive ? 'page' : undefined}
                      className={cn(
                        'group flex items-center gap-3 rounded-lg px-3 py-2 transition-all duration-150 hover:text-primary active:scale-[0.99]',
                        isActive
                          ? 'bg-muted font-semibold text-primary shadow-2xs'
                          : 'text-text-subtle hover:bg-muted/50'
                      )}
                    >
                      <IconComponent className="h-4.5 w-4.5 shrink-0 transition-transform duration-150 group-hover:scale-110" />
                      <span className={navLabelClass}>{t(item.titleKey)}</span>
                    </Link>
                  );
                })}
              </nav>
            </div>
            <div className={cn('mt-auto', compactNav ? 'p-2' : 'p-4')}>
              <nav className="grid items-start gap-1 text-sm font-medium">
                {secondaryNavItems.map(item => {
                  const IconComponent = item.icon;
                  const isActive = isNavigationItemActive(pathname, item.href);
                  return (
                    <Link
                      key={item.href}
                      href={item.href}
                      aria-current={isActive ? 'page' : undefined}
                      className={cn(
                        'flex items-center gap-3 rounded-lg px-3 py-2 transition-all hover:text-primary',
                        isActive ? 'bg-muted text-primary' : 'text-text-subtle hover:bg-muted/50'
                      )}
                    >
                      <IconComponent className="h-4.5 w-4.5 shrink-0" />
                      <span className={navLabelClass}>{t(item.titleKey)}</span>
                    </Link>
                  );
                })}
              </nav>
              <Link
                href={ROUTES.SITE.HOME}
                className="flex h-8 items-center gap-2 text-sm text-muted-foreground hover:text-foreground"
              >
                {compactNav && <ArrowUpLeft className="mx-3 size-4 shrink-0" aria-hidden="true" />}
                <span className={navLabelClass}>{t('console.returnToSite')}</span>
              </Link>
            </div>
          </aside>
        </div>

        {/* Scrollable Main Content */}
        <main key={pathname} className="animate-enter h-full min-w-0 w-full overflow-y-auto">
          {children}
        </main>
      </div>
    </div>
  );
}

function isNavigationItemActive(pathname: string, href: string): boolean {
  return pathname === href || (href !== ROUTES.CONSOLE.HOME && pathname.startsWith(`${href}/`));
}
