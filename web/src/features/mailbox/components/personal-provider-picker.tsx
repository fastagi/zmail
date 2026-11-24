'use client';

import * as React from 'react';
import {
  ArrowRight,
  ExternalLink,
  Globe,
  KeyRound,
  Mail,
  Server,
  ShieldCheck,
  Sparkles,
} from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  EXTERNAL_MAIL_APP_PASSWORD_PAGES,
  EXTERNAL_MAIL_DEFAULT_SMTP_PORT,
  EXTERNAL_MAIL_IMAP_PORT,
  EXTERNAL_MAIL_SERVER_PRESETS,
  type ExternalMailServerPresetId,
  type ExternalMailSmtpPort,
} from '@/features/mailbox/external-mail-schemas';
import { cn } from '@/utils';

export type PersonalProviderAuthMethod = 'oauth' | 'app_password';

export type PersonalProviderId =
  | 'gmail'
  | 'outlook'
  | 'icloud'
  | 'qq'
  | 'netease163'
  | 'netease126'
  | 'fastmail'
  | 'yahoo'
  | 'custom';

export interface PersonalProviderPreset {
  id: PersonalProviderId;
  label: string;
  shortLabel: string;
  authMethod: PersonalProviderAuthMethod;
  authBadge: string;
  imapHost: string;
  imapPort: number;
  smtpHost: string;
  smtpPort: ExternalMailSmtpPort;
  guidance: string;
  appPasswordUrl?: string;
  domains: readonly string[];
  accentClass: string;
  /** Populated when the user submits from the email auto-detect field. */
  email?: string;
}

function serverPreset(id: ExternalMailServerPresetId) {
  const found = EXTERNAL_MAIL_SERVER_PRESETS.find(preset => preset.id === id);
  if (!found) {
    throw new Error(`Missing server preset: ${id}`);
  }
  return found;
}

const GMAIL_SERVER = serverPreset('gmail');
const ICLOUD_SERVER = serverPreset('icloud');
const QQ_SERVER = serverPreset('qq');
const NETEASE_163_SERVER = serverPreset('netease163');
const NETEASE_126_SERVER = serverPreset('netease126');
const FASTMAIL_SERVER = serverPreset('fastmail');
const YAHOO_SERVER = serverPreset('yahoo');

export const PERSONAL_PROVIDER_PRESETS: readonly PersonalProviderPreset[] = [
  {
    id: 'gmail',
    label: 'Gmail',
    shortLabel: 'G',
    authMethod: 'oauth',
    authBadge: 'OAuth 2.0',
    imapHost: GMAIL_SERVER.imap,
    imapPort: EXTERNAL_MAIL_IMAP_PORT,
    smtpHost: GMAIL_SERVER.smtp,
    smtpPort: GMAIL_SERVER.smtpPort,
    guidance: 'Sign in with Google OAuth in one click — no manual server setup required.',
    appPasswordUrl: EXTERNAL_MAIL_APP_PASSWORD_PAGES.gmail,
    domains: ['gmail.com', 'googlemail.com'],
    accentClass: 'bg-red-500/10 text-red-600 dark:bg-red-500/20 dark:text-red-400',
  },
  {
    id: 'outlook',
    label: 'Outlook',
    shortLabel: 'O',
    authMethod: 'oauth',
    authBadge: 'OAuth 2.0',
    imapHost: 'outlook.office365.com',
    imapPort: EXTERNAL_MAIL_IMAP_PORT,
    smtpHost: 'smtp.office365.com',
    smtpPort: 587,
    guidance: 'Connect Outlook, Hotmail, or Live accounts securely with Microsoft OAuth.',
    domains: ['outlook.com', 'hotmail.com', 'live.com', 'msn.com', 'outlook.cn'],
    accentClass: 'bg-blue-500/10 text-blue-600 dark:bg-blue-500/20 dark:text-blue-400',
  },
  {
    id: 'icloud',
    label: 'iCloud',
    shortLabel: 'iC',
    authMethod: 'app_password',
    authBadge: 'App Password',
    imapHost: ICLOUD_SERVER.imap,
    imapPort: EXTERNAL_MAIL_IMAP_PORT,
    smtpHost: ICLOUD_SERVER.smtp,
    smtpPort: ICLOUD_SERVER.smtpPort,
    guidance: 'Generate an Apple app-specific password; IMAP & SMTP servers are pre-filled.',
    appPasswordUrl: EXTERNAL_MAIL_APP_PASSWORD_PAGES.icloud,
    domains: ['icloud.com', 'me.com', 'mac.com'],
    accentClass: 'bg-sky-500/10 text-sky-600 dark:bg-sky-500/20 dark:text-sky-400',
  },
  {
    id: 'qq',
    label: 'QQ Mail',
    shortLabel: 'QQ',
    authMethod: 'app_password',
    authBadge: 'App Password',
    imapHost: QQ_SERVER.imap,
    imapPort: EXTERNAL_MAIL_IMAP_PORT,
    smtpHost: QQ_SERVER.smtp,
    smtpPort: QQ_SERVER.smtpPort,
    guidance: 'Enable IMAP/SMTP in QQ Mail settings and paste your 16-character authorization code.',
    appPasswordUrl: 'https://mail.qq.com',
    domains: ['qq.com', 'foxmail.com', 'vip.qq.com'],
    accentClass: 'bg-amber-500/10 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400',
  },
  {
    id: 'netease163',
    label: '163 / 126',
    shortLabel: '163',
    authMethod: 'app_password',
    authBadge: 'App Password',
    imapHost: NETEASE_163_SERVER.imap,
    imapPort: EXTERNAL_MAIL_IMAP_PORT,
    smtpHost: NETEASE_163_SERVER.smtp,
    smtpPort: NETEASE_163_SERVER.smtpPort,
    guidance: 'Enable IMAP/SMTP in NetEase Mail settings and sign in with your authorization code.',
    appPasswordUrl: 'https://mail.163.com',
    domains: ['163.com', 'vip.163.com'],
    accentClass: 'bg-emerald-500/10 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-400',
  },
  {
    id: 'fastmail',
    label: 'Fastmail',
    shortLabel: 'FM',
    authMethod: 'app_password',
    authBadge: 'App Password',
    imapHost: FASTMAIL_SERVER.imap,
    imapPort: EXTERNAL_MAIL_IMAP_PORT,
    smtpHost: FASTMAIL_SERVER.smtp,
    smtpPort: FASTMAIL_SERVER.smtpPort,
    guidance: 'Create a Fastmail app password with IMAP & SMTP access; servers are pre-filled.',
    appPasswordUrl: 'https://app.fastmail.com/settings/security/apps',
    domains: ['fastmail.com', 'fastmail.fm'],
    accentClass: 'bg-indigo-500/10 text-indigo-600 dark:bg-indigo-500/20 dark:text-indigo-400',
  },
  {
    id: 'yahoo',
    label: 'Yahoo',
    shortLabel: 'Y!',
    authMethod: 'app_password',
    authBadge: 'App Password',
    imapHost: YAHOO_SERVER.imap,
    imapPort: EXTERNAL_MAIL_IMAP_PORT,
    smtpHost: YAHOO_SERVER.smtp,
    smtpPort: YAHOO_SERVER.smtpPort,
    guidance: 'Generate a Yahoo app password from Account Security; IMAP & SMTP are pre-filled.',
    appPasswordUrl: 'https://login.yahoo.com/myaccount/security/',
    domains: ['yahoo.com', 'ymail.com', 'yahoo.co.jp', 'yahoo.co.uk'],
    accentClass: 'bg-purple-500/10 text-purple-600 dark:bg-purple-500/20 dark:text-purple-400',
  },
  {
    id: 'custom',
    label: 'Custom IMAP',
    shortLabel: 'IMAP',
    authMethod: 'app_password',
    authBadge: 'IMAP / SMTP',
    imapHost: '',
    imapPort: EXTERNAL_MAIL_IMAP_PORT,
    smtpHost: '',
    smtpPort: EXTERNAL_MAIL_DEFAULT_SMTP_PORT,
    guidance: 'Connect any standard mailbox using its IMAP (TLS 993) and SMTP server credentials.',
    domains: [],
    accentClass: 'bg-muted text-foreground dark:bg-muted/80',
  },
] as const;

const NETEASE_126_PRESET: PersonalProviderPreset = {
  id: 'netease126',
  label: '126 Mail',
  shortLabel: '126',
  authMethod: 'app_password',
  authBadge: 'App Password',
  imapHost: NETEASE_126_SERVER.imap,
  imapPort: EXTERNAL_MAIL_IMAP_PORT,
  smtpHost: NETEASE_126_SERVER.smtp,
  smtpPort: NETEASE_126_SERVER.smtpPort,
  guidance: 'Enable IMAP/SMTP in 126 Mail settings and sign in with your authorization code.',
  appPasswordUrl: 'https://mail.126.com',
  domains: ['126.com'],
  accentClass: 'bg-emerald-500/10 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-400',
};

const ALL_DETECTABLE_PRESETS: readonly PersonalProviderPreset[] = [
  ...PERSONAL_PROVIDER_PRESETS,
  NETEASE_126_PRESET,
];

const CUSTOM_IMAP_PRESET = PERSONAL_PROVIDER_PRESETS.find(
  preset => preset.id === 'custom'
) as PersonalProviderPreset;

const EMAIL_SHAPE_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/u;

/**
 * Auto-detects the personal mail provider from an email address.
 * Returns `null` until the input resembles a complete email address, then falls back to Custom IMAP.
 */
export function detectPersonalProvider(address: string): PersonalProviderPreset | null {
  const trimmed = address.trim();
  if (!EMAIL_SHAPE_PATTERN.test(trimmed)) {
    return null;
  }

  const domain = trimmed.slice(trimmed.lastIndexOf('@') + 1).toLowerCase();
  const matched = ALL_DETECTABLE_PRESETS.find(preset => preset.domains.includes(domain));
  return matched ?? CUSTOM_IMAP_PRESET;
}

export interface PersonalProviderPickerProps {
  onSelectProvider: (preset: PersonalProviderPreset) => void;
  initialEmail?: string;
  title?: string;
  description?: string;
  className?: string;
}

export function PersonalProviderPicker({
  onSelectProvider,
  initialEmail = '',
  title = 'Quick-connect your personal email',
  description = 'Enter your email address to auto-detect settings, or pick a provider directly.',
  className,
}: PersonalProviderPickerProps) {
  const [email, setEmail] = React.useState(initialEmail);
  const detected = React.useMemo(() => detectPersonalProvider(email), [email]);

  const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!detected) {
      return;
    }
    onSelectProvider({
      ...detected,
      email: email.trim(),
    });
  };

  const handleTileClick = (preset: PersonalProviderPreset) => {
    const trimmed = email.trim();
    onSelectProvider(trimmed ? { ...preset, email: trimmed } : preset);
  };

  return (
    <Card className={cn('gap-5 py-5', className)} data-slot="personal-provider-picker">
      <CardHeader className="gap-1.5 pb-0">
        <div className="flex items-center gap-2">
          <Sparkles className="size-4 text-primary" aria-hidden="true" />
          <CardTitle className="text-base">{title}</CardTitle>
        </div>
        <CardDescription>{description}</CardDescription>
      </CardHeader>

      <CardContent className="flex flex-col gap-5">
        <form onSubmit={handleSubmit} className="flex flex-col gap-3" noValidate>
          <div className="flex flex-col gap-2 sm:flex-row">
            <div className="flex-1">
              <Input
                type="email"
                inputMode="email"
                autoComplete="email"
                aria-label="Email address"
                placeholder="you@gmail.com, name@qq.com, or any IMAP address"
                leftIcon={<Mail className="size-4" aria-hidden="true" />}
                value={email}
                onChange={event => setEmail(event.target.value)}
              />
            </div>
            <Button
              type="submit"
              disabled={!detected}
              className="sm:w-auto"
              icon={<ArrowRight className="size-4" aria-hidden="true" />}
              iconPosition="right"
            >
              {detected ? `Connect ${detected.label}` : 'Continue'}
            </Button>
          </div>

          {detected ? (
            <div
              role="status"
              aria-live="polite"
              data-slot="detected-provider-preview"
              data-provider-id={detected.id}
              className="flex flex-col gap-2 rounded-lg border border-border/80 bg-muted/40 p-3 text-sm dark:bg-muted/25"
            >
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <span
                    className={cn(
                      'inline-flex size-6 items-center justify-center rounded-md text-xs font-semibold',
                      detected.accentClass
                    )}
                    aria-hidden="true"
                  >
                    {detected.shortLabel}
                  </span>
                  <span className="font-medium text-foreground">{detected.label}</span>
                </div>
                <Badge variant={detected.authMethod === 'oauth' ? 'default' : 'secondary'}>
                  {detected.authMethod === 'oauth' ? (
                    <ShieldCheck className="size-3" aria-hidden="true" />
                  ) : (
                    <KeyRound className="size-3" aria-hidden="true" />
                  )}
                  {detected.authBadge}
                </Badge>
              </div>

              <p className="text-xs text-muted-foreground">{detected.guidance}</p>

              <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                {detected.imapHost ? (
                  <span className="inline-flex items-center gap-1 font-mono">
                    <Server className="size-3 shrink-0" aria-hidden="true" />
                    IMAP: {detected.imapHost}:{detected.imapPort}
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1">
                    <Globe className="size-3 shrink-0" aria-hidden="true" />
                    Custom IMAP / SMTP server
                  </span>
                )}
                {detected.smtpHost ? (
                  <span className="font-mono">
                    SMTP: {detected.smtpHost}:{detected.smtpPort}
                  </span>
                ) : null}
                {detected.appPasswordUrl && detected.authMethod === 'app_password' ? (
                  <a
                    href={detected.appPasswordUrl}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 text-primary hover:underline"
                  >
                    App password guide
                    <ExternalLink className="size-3" aria-hidden="true" />
                  </a>
                ) : null}
              </div>
            </div>
          ) : null}
        </form>

        <div className="flex flex-col gap-2.5">
          <p className="text-xs font-medium text-muted-foreground">Or choose a provider</p>
          <ul
            className="grid grid-cols-2 gap-2 sm:grid-cols-4"
            aria-label="Email providers"
          >
            {PERSONAL_PROVIDER_PRESETS.map(preset => {
              const isHighlighted =
                detected?.id === preset.id ||
                (detected?.id === 'netease126' && preset.id === 'netease163');

              return (
                <li key={preset.id} className="flex">
                  <button
                    type="button"
                    data-provider-tile={preset.id}
                    onClick={() => handleTileClick(preset)}
                    className={cn(
                      'group flex w-full flex-col items-start gap-2 rounded-lg border p-3 text-left transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1',
                      isHighlighted
                        ? 'border-primary bg-primary/5 shadow-xs dark:bg-primary/10'
                        : 'border-border/80 bg-background hover:border-border-strong hover:bg-muted/40 dark:bg-card/60 dark:hover:bg-muted/30'
                    )}
                  >
                    <div className="flex w-full items-center justify-between gap-2">
                      <span
                        className={cn(
                          'inline-flex size-7 items-center justify-center rounded-md text-xs font-semibold',
                          preset.accentClass
                        )}
                        aria-hidden="true"
                      >
                        {preset.shortLabel}
                      </span>
                      <Badge
                        variant={preset.authMethod === 'oauth' ? 'default' : 'outline'}
                        className="px-1.5 py-0 text-[10px]"
                      >
                        {preset.authMethod === 'oauth' ? 'OAuth' : 'IMAP'}
                      </Badge>
                    </div>
                    <div className="flex flex-col gap-0.5">
                      <span className="text-sm font-medium text-foreground">{preset.label}</span>
                      <span className="text-[11px] text-muted-foreground">
                        {preset.imapHost ? preset.imapHost : 'Any mail server'}
                      </span>
                    </div>
                  </button>
                </li>
              );
            })}
          </ul>
        </div>
      </CardContent>
    </Card>
  );
}
