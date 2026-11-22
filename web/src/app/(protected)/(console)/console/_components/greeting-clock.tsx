'use client';

import { useEffect, useState } from 'react';
import { useLocale } from 'next-intl';

import { useCurrentUser } from '@/features/auth/store/auth-store';
import type { AuthUser } from '@/features/auth/types';
import { useT } from '@/i18n';

/**
 * Small client island for the console greeting + wall-clock. The name comes from the signed-in
 * user in the request's auth store, which every auth mode fills, so the page stays a pure Server
 * Component.
 */
export function GreetingClock() {
  const [now, setNow] = useState<Date | null>(null);
  const locale = useLocale();
  const t = useT('console');
  const name = greetingName(useCurrentUser()) ?? t('home.defaultUser');

  useEffect(() => {
    const update = () => setNow(new Date());
    const firstTick = window.setTimeout(update, 0);
    const interval = window.setInterval(update, 60_000);
    return () => {
      window.clearTimeout(firstTick);
      window.clearInterval(interval);
    };
  }, []);

  const greetingKey = pickGreeting(now);
  const time = now?.toLocaleTimeString(locale, {
    hour: '2-digit',
    minute: '2-digit',
  });

  return (
    <h1 className="text-2xl font-semibold tracking-tight md:text-3xl">
      {t(`greeting.${greetingKey}`, { name })}
      {time ? (
        <>
          {' '}
          <span className="ml-3 align-middle text-base font-normal text-text-muted">{time}</span>
        </>
      ) : null}
    </h1>
  );
}

type GreetingKey = 'hello' | 'late' | 'morning' | 'afternoon' | 'evening';

function pickGreeting(now: Date | null): GreetingKey {
  if (!now) return 'hello';
  const h = now.getHours();
  if (h < 5) return 'late';
  if (h < 12) return 'morning';
  if (h < 18) return 'afternoon';
  return 'evening';
}

/** The name the user gave at sign-up, else the part of their address before the @. */
export function greetingName(user: AuthUser | null | undefined): string | null {
  const name = user?.name?.trim();
  if (name) return name;
  const local = user?.email?.split('@')[0]?.trim();
  return local || null;
}
