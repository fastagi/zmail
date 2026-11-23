import { PropsWithChildren } from 'react';
import Link from 'next/link';
import { SiteHeaderNav } from '@/components/features/site/site-header-nav';
import { LanguageSwitcher } from '@/components/common/locale-switcher';
import { ThemeToggle } from '@/components/theme-toggle';
import { Logo } from '@/components/ui/icons';
import { getT } from '@/i18n/server';

/**
 * Site layout for public pages
 * Unified design language with Auth and Console
 */
export default async function SiteLayout({ children }: PropsWithChildren) {
  const t = await getT('site');

  return (
    <div className="flex min-h-screen flex-col bg-bg-canvas">
      {/* Navigation bar - unified with Console header style */}
      <header className="sticky top-0 z-50 border-b border-border/50 bg-bg-surface/80 backdrop-blur-md">
        <div className="container flex h-16 items-center justify-between">
          <div className="flex items-center gap-8">
            {/* Logo */}
            <Link href="/" className="flex items-center gap-2.5 group">
              <Logo className="h-8 w-8 text-primary transition-transform group-hover:scale-105" />
              <span className="text-xl font-bold tracking-tight">Zmail</span>
            </Link>

            {/* Navigation links - only valid ones */}
            <nav className="hidden md:flex items-center gap-1">
              <Link 
                href="/" 
                className="px-3 py-2 text-sm font-medium text-text-subtle rounded-lg transition-colors hover:text-primary hover:bg-muted/50"
              >
                {t('nav.home')}
              </Link>
              <Link 
                href="/console" 
                className="px-3 py-2 text-sm font-medium text-text-subtle rounded-lg transition-colors hover:text-primary hover:bg-muted/50"
              >
                {t('nav.console')}
              </Link>
            </nav>
          </div>

          {/* Right side actions */}
          <div className="flex items-center gap-2">
            <LanguageSwitcher />
            <ThemeToggle />
            <SiteHeaderNav />
          </div>
        </div>
      </header>

      {/* Main content area */}
      <main className="flex-1">
        {children}
      </main>

      {/* Simplified Footer */}
      <footer className="border-t border-border/50 bg-bg-surface/50">
        <div className="container py-8">
          <div className="flex flex-col items-center justify-between gap-4 md:flex-row">
            {/* Brand */}
            <div className="flex items-center gap-2">
              <Logo className="h-6 w-6 text-primary" />
              <span className="text-sm font-medium">Zmail</span>
              <span className="text-sm text-text-muted">
                {t('footer.tagline')}
              </span>
            </div>

            {/* Links & Copyright */}
            <div className="flex items-center gap-6 text-sm text-text-muted">
              <a 
                href="https://github.com/fastagi/zmail"
                target="_blank"
                rel="noopener noreferrer"
                className="hover:text-primary transition-colors"
              >
                GitHub
              </a>
              <span>{t('footer.copyright', { year: new Date().getFullYear() })}</span>
            </div>
          </div>
        </div>
      </footer>
    </div>
  );
}
