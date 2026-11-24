/**
 * Privacy Policy and Terms of Service for the public site.
 *
 * Each section body is plain text: blank lines separate paragraphs and lines that start with
 * "- " render as a bulleted list.
 */
const messages = {
  footer: {
    privacy: 'Privacy',
    terms: 'Terms',
  },
  updated: 'Last updated: {date}',
  contact: 'Questions about this document? Email {email}.',
  privacy: {
    title: 'Privacy Policy',
    description: 'How Zmail collects, uses, and protects your information.',
    intro:
      'This policy explains what information Zmail (“we”) collects when you use the zmail.im website, console, HTTP API, and IMAP and SMTP services, how we use it, and the choices you have.',
    sections: {
      collect: {
        heading: 'Information we collect',
        body: '- Account information: your email address, your name, and your password, which we store only as a one-way hash. If you turn on two-step verification, its secret is stored encrypted.\n- Google sign-in and Gmail connection: if you choose “Continue with Google”, Google shares your Google account identifier, email address, whether that address is verified, and your name using only the openid, email, and profile scopes. If you explicitly connect a Gmail mailbox, we additionally request only the Gmail scopes needed to sync and send mail on your behalf.\n- Organization and mail data: organizations, members and their roles, connected domains, mailboxes, the messages and attachments you send and receive, contacts, templates, and broadcast history.\n- Connected mail accounts: if you connect an outside mailbox over IMAP or OAuth, the server address and credentials or tokens you authorize, stored encrypted.\n- Technical information: IP address, browser type, and request times, which we use for security, rate limiting, and troubleshooting. API keys and device passwords are stored only as hashes.\n- Notification subscriptions: if you turn on browser notifications, the push subscription your browser provides.',
      },
      use: {
        heading: 'How we use information',
        body: 'We use this information to provide the service: to receive, store, display, and send mail for you and your organization, to keep accounts secure, to send service messages such as verification codes and welcome mail, to answer support requests, and to keep the service running and improve it.\n\nWe do not sell personal information. We do not use the content of your mail for advertising, and we do not use it to train AI models.',
      },
      google: {
        heading: 'Google API Services User Data Policy (Limited Use)',
        body: 'Zmail’s use and transfer of information received from Google APIs to any other app will adhere to the Google API Services User Data Policy, including the Limited Use requirements.\n\nInformation received from Google APIs — including Google sign-in profile data and, when you connect a Gmail mailbox, your Gmail messages and metadata — is used solely to provide user-facing features you request (such as account sign-in, syncing, reading, organizing, and sending mail). We do not use Gmail data for advertising, we do not share it with third parties except as necessary to provide the service or comply with law, and Gmail data is never used to develop, improve, or train generalized AI/ML models.',
      },
      sharing: {
        heading: 'How information is shared',
        body: '- Within your organization: owners and admins can see organization information according to the roles they assign, and members see the mailboxes shared with them.\n- Service providers: Cloudflare routes and delivers email and manages DNS for domains you connect, and Google provides sign-in when you choose it. They process data only to provide those services.\n- Legal reasons: we disclose information when the law requires it, or when it is needed to protect people from harm or to stop abuse of the service.\n- Business transfers: if Zmail is part of a merger or acquisition, information may transfer under this policy, and we will tell you before it becomes subject to a different policy.',
      },
      cookies: {
        heading: 'Cookies',
        body: 'We use only the cookies the service needs: a session cookie that keeps you signed in, and a short-lived cookie that protects the Google sign-in flow. Your language and theme preferences are kept in your browser. We do not use advertising or third-party analytics cookies.',
      },
      retention: {
        heading: 'Storage and retention',
        body: 'We keep your information while your account is active. When you delete your account, we delete your personal information or make it anonymous, except where we must keep it to meet legal obligations or resolve disputes. Mail that belongs to an organization stays with that organization until its owners delete it. Backups are rotated on a fixed schedule, so deleted data leaves them over time.',
      },
      security: {
        heading: 'Security',
        body: 'Connections are encrypted with TLS. Passwords and keys are hashed, sensitive secrets are encrypted at rest, and access inside the service is checked against your role. No system is perfectly secure, so please use a strong password and turn on two-step verification.',
      },
      rights: {
        heading: 'Your choices and rights',
        body: 'You can see and correct your profile in Settings, and delete your account from Settings at any time. To request a copy of your information, or for any other request about your data, email us at the address below. Depending on where you live, you may have further rights under local law, and we will respond to such requests as that law requires.',
      },
      children: {
        heading: 'Children',
        body: 'Zmail is not directed to children under 16, and we do not knowingly collect information from them.',
      },
      changes: {
        heading: 'Changes to this policy',
        body: 'We will post any change on this page and update the date above. If a change materially affects how we use your information, we will tell you in the product or by email before it takes effect.',
      },
    },
  },
  terms: {
    title: 'Terms of Service',
    description: 'The rules for using Zmail.',
    intro:
      'These terms govern your use of Zmail, including the zmail.im website, console, HTTP API, and IMAP and SMTP services. By creating an account or using the service, you agree to them. If you use Zmail for an organization, you accept them on its behalf.',
    sections: {
      account: {
        heading: 'Your account',
        body: 'Give accurate information when you sign up, keep your password, API keys, and device passwords safe, and tell us promptly if you think your account has been misused. You are responsible for activity under your account. The person who creates an organization becomes its owner and decides who may join it and what each member may do.',
      },
      use: {
        heading: 'Acceptable use',
        body: 'You may not use Zmail to:\n- send spam or bulk mail to people who have not agreed to receive it;\n- send phishing, malware, or deceptive messages, or impersonate another person or organization;\n- send content that is illegal or infringes the rights of others;\n- get around sending limits, probe or attack the service, or interfere with other users.\n\nBroadcasts must go only to recipients who agreed to receive them, and every broadcast must let recipients unsubscribe. Zmail automatically pauses sending when bounce or complaint rates are too high, and we may limit sending, suspend an organization, or close an account that breaks these rules.',
      },
      content: {
        heading: 'Your content',
        body: 'You keep ownership of the mail, contacts, templates, and other content you put into Zmail. You give us permission to store, process, and transmit that content only as needed to provide the service to you. You are responsible for having the right to send the mail you send and to use the contacts you import.',
      },
      domains: {
        heading: 'Domains and sending',
        body: 'Connect only domains you own or are authorized to manage. Sending limits apply to each organization and may change to protect deliverability. Delivery depends on recipient mail servers, so we cannot guarantee that every message will arrive.',
      },
      availability: {
        heading: 'Changes to the service',
        body: 'We are constantly improving Zmail and may add, change, or remove features. We aim to keep the service available, but it may be interrupted for maintenance or for reasons outside our control.',
      },
      fees: {
        heading: 'Fees',
        body: 'Zmail is currently free to use. If we introduce paid plans, we will publish the prices first, and we will not charge you without your agreement.',
      },
      termination: {
        heading: 'Ending your use',
        body: 'You can stop using Zmail and delete your account at any time. We may suspend or end access if you break these terms or if we need to in order to comply with the law, and when we can, we will tell you first and give you a chance to export your data.',
      },
      disclaimer: {
        heading: 'Disclaimer and limitation of liability',
        body: 'Zmail is provided “as is” and “as available”, without warranties of any kind, to the extent the law allows. To the extent the law allows, we are not liable for indirect, incidental, or consequential damages, or for lost data, profits, or revenue, arising from your use of the service.',
      },
      changes: {
        heading: 'Changes to these terms',
        body: 'We will post any change on this page and update the date above. If a change is material, we will tell you in the product or by email before it takes effect. Continuing to use Zmail after a change takes effect means you accept the new terms.',
      },
    },
  },
} as const;

export default messages;
export type LegalMessages = typeof messages;
