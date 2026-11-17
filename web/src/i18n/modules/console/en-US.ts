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
        description: 'Read the inbox, reply, and send mail.',
      },
      broadcasts: {
        title: 'Broadcasts',
        description:
          'Send product updates in stages and follow progress, bounces, and unsubscribes.',
      },
      contacts: {
        title: 'Contacts',
        description: 'Import contacts into groups; unsubscribes apply on their own.',
      },
      templates: {
        title: 'Templates',
        description: 'Keep the mail you send often and pick it for a broadcast.',
      },
      mailboxes: {
        title: 'Mailboxes',
        description: 'Create sending addresses for people and purposes.',
      },
      domain: {
        title: 'Domain',
        description: 'Connect your own domains; Zmail sets up their mail records.',
      },
      apiAccess: {
        title: 'API access',
        description: 'Manage user-owned API keys and starter preferences.',
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
    gettingStarted: {
      domain: 'Connect your domain under Domain, or use the shared domain.',
      mailbox: 'Create a sending address under Mailboxes, such as hello@your-domain.',
      contacts: 'Create a group under Contacts and import contacts.',
      template: 'Save a product update under Templates.',
      broadcast:
        'Under Broadcasts, pick the domain, mailbox, template, and group; send a test wave before everyone.',
    },
  },
} as const;

export default messages;
export type ConsoleMessages = typeof messages;
