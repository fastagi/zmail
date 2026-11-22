import type { ModuleName } from './module-names';

// Global namespaces are always serialized; route scopes add only what their client leaves use.
export const CLIENT_MESSAGE_NAMESPACES = {
  global: ['common', 'errors'],
  auth: ['auth'],
  // The first-run team setup card (organization copy) renders on the console home and inbox.
  console: ['auth', 'nav', 'console', 'organization'],
  settings: ['settings'],
  organization: ['organization'],
  permission: ['permission'],
  notification: ['notification'],
  asset: ['asset'],
  setting: ['setting'],
  usage: ['usage'],
  webhook: ['webhook'],
  i18nTest: ['test'],
  mailbox: ['mailbox'],
  developer: ['developer', 'mailbox'],
  // fastbuild:slot web.i18n.client-namespaces
} as const satisfies Record<string, readonly ModuleName[]>;

/**
 * The developer console's namespaces: its Webhooks tab renders the webhook feature's management
 * UI, so the webhook namespace comes along when that feature is on (and the tab shows).
 */
export function developerMessageNamespaces(webhookEnabled: boolean): readonly ModuleName[] {
  return [
    ...CLIENT_MESSAGE_NAMESPACES.developer,
    ...(webhookEnabled ? CLIENT_MESSAGE_NAMESPACES.webhook : []),
  ];
}
