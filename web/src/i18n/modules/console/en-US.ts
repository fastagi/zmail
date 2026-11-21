const messages = {
  notifications: 'Notifications',
  profile: 'Profile',
  returnToSite: 'Return to site',
  greeting: {
    hello: 'Hello, {name}',
    late: 'Up late, {name}',
    morning: 'Good morning, {name}',
    afternoon: 'Good afternoon, {name}',
    evening: 'Good evening, {name}',
  },
  boundary: {
    errorTitle: 'This console page could not be loaded',
    errorDescription:
      'Your session and data were not changed. Try again, or return to the console home.',
    requestId: 'Request ID: {requestId}',
    errorDigest: 'Error reference: {digest}',
    notFoundDescription: 'The console page you requested does not exist or has moved.',
    backToConsole: 'Back to console',
  },
  home: {
    defaultUser: 'there',
    welcomeDescription:
      "Send and receive your organization's mail, manage contacts and templates, and broadcast product updates.",
    open: 'Open',
    gettingStartedTitle: 'Getting started',
    quickLinks: {
      emails: {
        title: 'Emails',
        description: 'Search every sent and received message, delivery events, and 7-day volume.',
      },
      broadcasts: {
        title: 'Broadcasts',
        description:
          'Send product updates in stages and follow progress, bounces, and unsubscribes.',
      },
      contacts: {
        title: 'Contacts',
        description: 'Import contacts into lists; unsubscribes apply on their own.',
      },
      templates: {
        title: 'Templates',
        description: 'Keep the mail you send often and pick it for a broadcast.',
      },
      mailboxes: {
        title: 'Mailboxes',
        description: 'Create team addresses, read the inbox, reply, and send mail.',
      },
      apiAccess: {
        title: 'API access',
        description: 'Manage user-owned API keys and account settings.',
      },
      styleguide: {
        title: 'Styleguide',
        description: 'Browse the design system and component gallery.',
      },
      i18nTest: {
        title: 'Internationalization test',
        description: 'Inspect the next-intl translation tree.',
      },
    },
    developerStepsTitle: 'Next steps',
    developerSteps: {
      apiBefore: 'Point',
      apiAfter: 'at a contract-compatible production endpoint or adapter.',
      replaceBefore: 'Replace this page (',
      replaceAfter: ') with your real business workspace.',
      featuresBefore: 'Add new features to',
      featuresBetween: 'and expose them through routes under',
      featuresAfter: '.',
    },
    onboarding: {
      team: {
        title: 'Create your team',
        hint: 'Done.',
      },
      mailbox: {
        title: 'Create your first mailbox',
        hint: 'An address such as hello@ that your team sends and receives from.',
      },
      invite: {
        title: 'Invite your colleagues',
        hint: 'They get an email with a link to join; it ticks once someone accepts.',
      },
      domain: {
        title: 'Use your own domain (optional)',
        hint: "Send from @your-company.com instead of the shared domain. You'll change your domain's name servers once.",
      },
      more: 'Ready for more?',
      moreLink: 'Send a product update to a contact list',
    },
  },
} as const;

export default messages;
export type ConsoleMessages = typeof messages;
