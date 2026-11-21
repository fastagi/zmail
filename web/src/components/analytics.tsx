import Script from 'next/script';
import { env } from '@/config/env';

const GA_MEASUREMENT_ID = env.NEXT_PUBLIC_GA_MEASUREMENT_ID;

/**
 * Routes whose query carries one-time secrets, such as the `state` and `code` a mail provider sends
 * back to the external mailbox callback. Analytics is told the page without its query there.
 */
export const ANALYTICS_PATH_ONLY_ROUTES: readonly string[] = [
  '/console/mailboxes/connected/callback',
];

/** The inline gtag bootstrap; the page view never carries the query of a path-only route. */
export function analyticsBootstrap(measurementId: string): string {
  return `
          window.dataLayer = window.dataLayer || [];
          function gtag(){dataLayer.push(arguments);}
          gtag('js', new Date());
          var pathOnly = ${JSON.stringify(ANALYTICS_PATH_ONLY_ROUTES)};
          var path = window.location.pathname;
          var hidden = pathOnly.some(function (route) {
            return path === route || path.indexOf(route + '/') === 0;
          });
          gtag('config', ${JSON.stringify(measurementId)}, {
            page_location: hidden ? window.location.origin + path : window.location.href
          });
        `;
}

export function Analytics() {
  if (!GA_MEASUREMENT_ID) return null;

  return (
    <>
      <Script
        src={`https://www.googletagmanager.com/gtag/js?id=${GA_MEASUREMENT_ID}`}
        strategy="lazyOnload"
      />
      <Script id="google-analytics" strategy="lazyOnload">
        {analyticsBootstrap(GA_MEASUREMENT_ID)}
      </Script>
    </>
  );
}
