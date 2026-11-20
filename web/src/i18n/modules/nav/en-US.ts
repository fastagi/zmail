// Nav translations - English (US)
const messages = {
  home: 'Home',
  console: 'Console',
  organizations: 'Organizations',
  assets: 'Assets',
  usage: 'Usage',
  settings: 'Settings',
  profile: 'Profile',
  analytics: 'Analytics',
  styleguide: 'Styleguide',
  mailboxes: 'Mailboxes',
  emails: 'Emails',
  mailBroadcasts: 'Broadcasts',
  mailContacts: 'Contacts',
  mailTemplates: 'Templates',
  admin: 'Admin console',
  adminDomains: 'Domains',
  adminSharedMailboxes: 'Shared mailboxes',
  adminPlatformAccounts: 'Platform accounts',
  adminAiTriage: 'AI triage',
  developers: 'Developers',
  // fastbuild:slot web.i18n.nav.en-us
} as const;

export default messages;
export type NavMessages = typeof messages;
