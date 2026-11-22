const messages = {
  notifications: 'Notifications',
  profile: 'Profile',
  returnToSite: 'Return to site',
  navigation: 'Console navigation',
  openNavigation: 'Open navigation menu',
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
    },
    setup: {
      title: 'Set up your team mail',
      titleNoTeam: 'Set up Zmail for your team',
      description:
        'From a new account to every colleague with a team mailbox. Each step ticks as it happens.',
      progress: '{done} of {total} done',
      state: {
        done: 'Done',
        skipped: 'Skipped',
        waiting: 'In progress',
      },
      team: {
        title: 'Create your team',
        short: 'Mailboxes, members and domains belong to a team.',
        created: 'Team {name} created. Carry on with the steps below.',
      },
      domain: {
        title: 'Connect your company domain',
        short: 'Mail from @your-company.com, set up by Zmail.',
        hint: 'Mail from @your-company.com earns more trust than a shared address. Zmail sets up the mail records; you change the name servers at your registrar once, which usually takes effect within a few hours.',
        action: 'Connect domain',
        view: 'See progress',
        skip: 'Just for me — use @{shared}',
        skipped: 'Using @{shared} for now.',
        undoSkip: 'Connect a domain instead',
        pendingZone:
          'Waiting for the name servers of {zone} to change at your registrar. Zmail checks every 10 minutes and finishes the rest by itself.',
        settingUp: 'Setting up the mail records for {domain}. This takes about a minute.',
        pendingRecords: 'Some DNS records for {domain} are not ready yet.',
        verified: '{domain} is verified.',
      },
      mailbox: {
        title: 'Create your mailbox',
        short: 'Your own address, or a shared one like hello@.',
        hint: 'Start with your own address, or a shared one such as hello@ that several people answer.',
        action: 'Create a mailbox',
        actionDomain: 'Create a mailbox on @{domain}',
        done: '{count, plural, one {# mailbox} other {# mailboxes}} so far.',
      },
      invite: {
        title: 'Invite colleagues and give them mailboxes',
        short: 'They join by email; then each gets a mailbox.',
        hint: 'Each colleague gets an email with a join link. Once they join, give each of them a mailbox of their own.',
        action: 'Invite colleagues',
        manage: 'See who joined',
        status: '{joined} joined · {pending} not yet',
        statusJoined: '{joined} joined',
        noMailbox:
          '{count, plural, one {# colleague has} other {# colleagues have}} no mailbox yet',
        giveMailbox: 'Create their mailboxes',
      },
    },
  },
} as const;

export default messages;
export type ConsoleMessages = typeof messages;
