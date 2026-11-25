/**
 * Application route constants
 * Central place to manage all application routes
 */

export const ROUTES = {
  // Public site routes
  SITE: {
    HOME: '/',
    DOCS: '/docs',
  },

  // Auth routes
  AUTH: {
    LOGIN: '/login',
    REGISTER: '/register',
    FORGOT_PASSWORD: '/forgot-password',
  },

  // Console (Admin) routes
  CONSOLE: {
    HOME: '/console',
    ORGANIZATIONS: '/console/organizations',
    ORGANIZATION: '/console/organizations/:organizationId',
    ASSETS: '/console/assets',
    USAGE: '/console/usage',
    SETTINGS: '/console/settings',
    MAILBOXES: '/console/mailboxes',
    EMAILS: '/console/emails',
    // The organization admin area: owners and admins only.
    ADMIN: '/console/admin',
    ADMIN_MAILBOXES: '/console/admin/mailboxes',
    ADMIN_PLATFORM_ACCOUNTS: '/console/admin/platform-accounts',
    ADMIN_AI_TRIAGE: '/console/admin/ai-triage',
    ADMIN_EXTERNAL_CONNECTIONS: '/console/admin/external-connections',
    MAIL_DOMAIN: '/console/admin/domains',
    MAIL_ZONE_DNS: '/console/admin/domains/:zoneId/dns',
    MAIL_TEMPLATES: '/console/mail-templates',
    MAIL_CONTACTS: '/console/mail-contacts',
    MAIL_BROADCASTS: '/console/mail-broadcasts',
    DEVELOPERS: '/console/developers',
    // fastbuild:slot web.routes.console
  },

  // Internal devtools and demos
  DEVTOOLS: {
    STYLEGUIDE: '/styleguide',
    I18N_TEST: '/i18n-test',
  },
} as const;

// Type-safe route utility functions
type SiteRoutes = keyof typeof ROUTES.SITE;
type AuthRoutes = keyof typeof ROUTES.AUTH;
type ConsoleRoutes = keyof typeof ROUTES.CONSOLE;
type DevtoolsRoutes = keyof typeof ROUTES.DEVTOOLS;

// Type helpers for route segments
export type SiteRoute = (typeof ROUTES.SITE)[SiteRoutes];
export type AuthRoute = (typeof ROUTES.AUTH)[AuthRoutes];
export type ConsoleRoute = (typeof ROUTES.CONSOLE)[ConsoleRoutes];
export type DevtoolsRoute = (typeof ROUTES.DEVTOOLS)[DevtoolsRoutes];

// Dynamic route builder with type checking
export function buildRoute(basePath: string, params?: Record<string, string | number>): string {
  let route = basePath;

  if (params) {
    Object.entries(params).forEach(([key, value]) => {
      route = route.replace(`:${key}`, String(value));
    });
  }

  return route;
}

// Strongly typed navigation helpers
export function getSiteRoute(route: SiteRoutes): string {
  return ROUTES.SITE[route];
}

export function getAuthRoute(route: AuthRoutes): string {
  return ROUTES.AUTH[route];
}

export function getConsoleRoute(route: ConsoleRoutes): string {
  return ROUTES.CONSOLE[route];
}

export function getOrganizationRoute(organizationId: number | string): string {
  return buildRoute(ROUTES.CONSOLE.ORGANIZATION, { organizationId });
}

export function getMailZoneDnsRoute(zoneId: number | string): string {
  return buildRoute(ROUTES.CONSOLE.MAIL_ZONE_DNS, { zoneId });
}

export function getDevtoolsRoute(route: DevtoolsRoutes): string {
  return ROUTES.DEVTOOLS[route];
}
