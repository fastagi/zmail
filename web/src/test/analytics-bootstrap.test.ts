import { afterEach, describe, expect, it } from 'vitest';

import { analyticsBootstrap } from '@/components/analytics';

type Gtag = { dataLayer?: unknown[] };

/** Runs the inline bootstrap as the browser would and returns the `config` call's options. */
function pageView(url: string): Record<string, unknown> {
  window.history.replaceState(null, '', url);
  delete (window as Gtag).dataLayer;
  new Function(analyticsBootstrap('G-TEST'))();
  const calls = ((window as Gtag).dataLayer ?? []) as IArguments[];
  const config = calls.map(call => Array.from(call)).find(call => call[0] === 'config');
  expect(config?.[1]).toBe('G-TEST');
  return config?.[2] as Record<string, unknown>;
}

afterEach(() => {
  window.history.replaceState(null, '', '/');
  delete (window as Gtag).dataLayer;
});

describe('analytics bootstrap', () => {
  it('never reports the query of the external mailbox callback', () => {
    expect(
      pageView('/console/mailboxes/connected/callback?state=secret-state&code=secret-code')
        .page_location
    ).toBe(`${window.location.origin}/console/mailboxes/connected/callback`);
  });

  it('reports other pages as they are', () => {
    expect(pageView('/console/mailboxes?org=1').page_location).toBe(
      `${window.location.origin}/console/mailboxes?org=1`
    );
  });
});
