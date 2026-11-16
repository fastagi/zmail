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
  mailContacts: 'Contacts',
  mailTemplates: 'Templates',
  mailDomain: 'Domain',
  // fastbuild:slot web.i18n.nav.en-us
} as const;

export default messages;
export type NavMessages = typeof messages;
