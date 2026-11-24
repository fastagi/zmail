'use client';

import type { RefObject } from 'react';
import Link from 'next/link';
import type { LucideIcon } from 'lucide-react';

import { cn } from '@/utils';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { WorkspaceModeToggle } from '@/features/mailbox/components/workspace-mode-toggle';
import type { WorkspaceMode } from '@/features/mailbox/stores/workspace-mode';
import { useT, type AllTranslationKeys } from '@/i18n';

export interface ConsoleNavItem {
  titleKey: Extract<AllTranslationKeys, `nav.${string}`>;
  href: string;
  icon: LucideIcon;
  active: boolean;
}

interface ConsoleMobileNavProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mainItems: ConsoleNavItem[];
  secondaryItems: ConsoleNavItem[];
  returnHref: string;
  /** The header menu button; it lives outside the lazy sheet, so focus is returned by hand. */
  triggerRef: RefObject<HTMLButtonElement | null>;
  onWorkspaceModeChange?: (mode: WorkspaceMode) => void;
}

/**
 * The console destinations for viewports below the desktop rail breakpoint.
 * Loaded lazily the first time the header menu button is pressed.
 */
export function ConsoleMobileNav({
  open,
  onOpenChange,
  mainItems,
  secondaryItems,
  returnHref,
  triggerRef,
  onWorkspaceModeChange,
}: ConsoleMobileNavProps) {
  const t = useT();
  const close = () => onOpenChange(false);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="left"
        id="console-mobile-nav"
        aria-describedby={undefined}
        className="w-72 gap-0 p-0"
        onCloseAutoFocus={event => {
          event.preventDefault();
          triggerRef.current?.focus();
        }}
      >
        <SheetHeader className="border-b">
          <SheetTitle>{t('console.navigation')}</SheetTitle>
        </SheetHeader>
        <nav
          aria-label={t('console.navigation')}
          className="flex flex-1 flex-col overflow-y-auto p-2 text-sm font-medium"
        >
          <div className="px-1 pb-2">
            <WorkspaceModeToggle
              size="sm"
              className="w-full"
              ariaLabel={t('common.workspaceMode.label')}
              labels={{
                personal: t('common.workspaceMode.personal'),
                work: t('common.workspaceMode.work'),
              }}
              onChange={onWorkspaceModeChange}
            />
          </div>
          <ul className="grid gap-0.5">
            {mainItems.map(item => (
              <MobileNavLink key={item.href} item={item} onNavigate={close} />
            ))}
          </ul>
          <ul className="mt-auto grid gap-0.5 border-t pt-2">
            {secondaryItems.map(item => (
              <MobileNavLink key={item.href} item={item} onNavigate={close} />
            ))}
            <li>
              <Link
                href={returnHref}
                onClick={close}
                className="flex items-center gap-3 rounded-lg px-3 py-2 text-muted-foreground hover:text-foreground"
              >
                {t('console.returnToSite')}
              </Link>
            </li>
          </ul>
        </nav>
      </SheetContent>
    </Sheet>
  );
}

function MobileNavLink({ item, onNavigate }: { item: ConsoleNavItem; onNavigate: () => void }) {
  const t = useT();
  const IconComponent = item.icon;
  return (
    <li>
      <Link
        href={item.href}
        onClick={onNavigate}
        aria-current={item.active ? 'page' : undefined}
        className={cn(
          'flex items-center gap-3 rounded-lg px-3 py-2 transition-colors hover:text-primary',
          item.active ? 'bg-muted font-semibold text-primary' : 'text-text-subtle hover:bg-muted/50'
        )}
      >
        <IconComponent className="h-4.5 w-4.5 shrink-0" aria-hidden="true" />
        <span>{t(item.titleKey)}</span>
      </Link>
    </li>
  );
}
