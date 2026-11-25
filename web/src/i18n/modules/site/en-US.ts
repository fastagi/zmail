const messages = {
  nav: {
    home: 'Overview',
    docs: 'Docs',
    console: 'Console',
  },
  footer: {
    tagline: 'Email on your own domain',
    copyright: '© {year} Zmail',
  },
  hero: {
    title: 'Team email on your own domain.',
    description:
      'Send a product update, reply to a customer, or deliver a receipt from your app. Give every message an address your customers recognize.',
    primary: 'Create an account',
    secondary: 'Explore the API',
  },
  preview: {
    update: 'Product update',
    releaseSubject: 'The autumn release',
    releaseBody:
      'Monthly reports are here. Choose a date range in Reports to see how your team did, then export the results to share.',
    address: 'support@atlas.example',
    label: 'Example conversation',
    inbox: 'Shared inbox',
    subject: 'Re: The autumn release',
    sender: 'Maya Chen',
    question: 'Love the new reports. Can I export last month’s numbers?',
    reply: 'Absolutely, Maya. Open Reports, choose last month, then select Export.',
    team: 'Atlas Support',
  },
  sample: {
    title: 'An order receipt over HTTP',
    description:
      'Use your own sender address and a key with the mail:send scope. Give each order a unique idempotency key.',
    action: 'Open developer console',
  },
  flows: {
    heading: 'How teams use Zmail',
    inbox: {
      title: 'Share a team address',
      description:
        'Give the right people access to support@. Read the conversation and reply from the same address.',
    },
    broadcast: {
      title: 'Send a product update',
      description:
        'Build an email from a template and send it to a test group first. Zmail can pause the broadcast if bounces or spam complaints rise above your thresholds.',
    },
    api: {
      title: 'Deliver mail from your app',
      description:
        'Send receipts over HTTP with a scoped API key. Reuse an idempotency key when you retry a request to avoid sending a second copy.',
    },
  },
} as const;

export default messages;
export type SiteMessages = typeof messages;
