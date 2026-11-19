const messages = {
  nav: {
    home: 'Home',
    console: 'Console',
  },
  footer: {
    tagline: 'Team mailboxes and safe broadcasts on your own domain',
    copyright: '© {year} Zmail. All rights reserved.',
  },
  hero: {
    eyebrow: 'Team email on your own domain',
    titlePrefix: 'Send, receive, and broadcast',
    titleAccent: 'from domains you own',
    description:
      'Zmail brings shared team mailboxes, visual email templates, contact lists, and staged broadcasts with automatic bounce and spam protection into one workspace.',
    primaryAction: 'Open console',
    secondaryAction: 'Sign in',
  },
  features: {
    title: 'Built for daily mail and product updates',
    description:
      'Everything an organization needs to run team addresses and send announcements safely.',
    authentication: {
      title: 'Your own mail domains',
      description:
        'Connect domains on Cloudflare with automated routing, sending, and DMARC checks.',
    },
    console: {
      title: 'Shared team mailboxes',
      description:
        'Create addresses for people and teams, read incoming mail, reply with attachments, and track delivery.',
    },
    i18n: {
      title: 'Visual templates & variables',
      description:
        'Design responsive emails with blocks or HTML, contact fields, fallbacks, and campaign-wide values.',
    },
    ui: {
      title: 'Staged, protected broadcasts',
      description:
        'Send to a random test wave first and pause automatically when bounces or spam reports spike.',
    },
  },
  techStack: {
    title: 'Ready for teams and backend services',
    description:
      'Operate from the web console or trigger idempotent transactional mail over HTTP.',
    pills: {
      domains: 'Multi-domain DNS checks',
      templates: 'Visual block editor',
      staged: 'Test-wave broadcasts',
      unsubscribe: 'One-click unsubscribe',
      organizations: 'Multi-organization roles',
      api: 'Idempotent HTTP API',
    },
  },
  cta: {
    title: 'Ready to manage your team mail?',
    description:
      'Sign in to your organization workspace to set up domains, mailboxes, templates, and broadcasts.',
    primaryAction: 'Enter console',
    githubAction: 'View on GitHub',
  },
} as const;

export default messages;
export type SiteMessages = typeof messages;
