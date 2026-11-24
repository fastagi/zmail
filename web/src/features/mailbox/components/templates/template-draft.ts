'use client';

import {
  MAIL_SUBJECT_MAX_LENGTH,
  MAIL_TEMPLATE_NAME_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import type { MailTemplate, MailTemplateInput } from '@/features/mailbox/types';
import {
  emptyDesign,
  readDesign,
  renderDesignHtml,
  renderDesignText,
  type TemplateDesign,
} from '@/features/mailbox/utils/template-design';
import type { StarterPhrases, TemplateStarter } from '@/features/mailbox/utils/template-starters';
import { useT } from '@/i18n';

export interface Draft {
  name: string;
  subject: string;
  html: string;
  text: string;
  samples: Record<string, string>;
  /** The visual design the bodies are rendered from; null in the HTML editor. */
  design: TemplateDesign | null;
}

/** A new template starts in the visual editor; a stored one opens the way it was written. */
export function draftOf(template: MailTemplate | null): Draft {
  if (!template)
    return withDesign(
      { name: '', subject: '', html: '', text: '', samples: {}, design: null },
      emptyDesign()
    );
  return {
    name: template.name,
    subject: template.subject,
    html: template.html_body,
    text: template.text_body,
    samples: { ...template.sample_variables },
    design: readDesign(template.design),
  };
}

/** The draft with the design and the bodies rendered from it. */
export function withDesign(draft: Draft, design: TemplateDesign): Draft {
  return { ...draft, design, html: renderDesignHtml(design), text: renderDesignText(design) };
}

/** The input the API receives: sample values only for variables the template uses. */
export function inputOf(draft: Draft, variables: readonly string[]): MailTemplateInput {
  const samples = Object.fromEntries(
    variables
      .map(name => [name, draft.samples[name] ?? ''] as const)
      .filter(([, value]) => value !== '')
  );
  return {
    name: draft.name,
    subject: draft.subject,
    html: draft.html,
    text: draft.text,
    sample_variables: samples,
    design: draft.design,
  };
}

export function useStarterPhrases(): StarterPhrases {
  const t = useT('mailbox');
  return {
    welcomeSubject: t('templates.starters.welcomeSubject'),
    welcomeHeading: t('templates.starters.welcomeHeading'),
    welcomeBody: t('templates.starters.welcomeBody'),
    welcomeButton: t('templates.starters.welcomeButton'),
    welcomeFooter: t('templates.starters.welcomeFooter'),
    greeting: t('templates.starters.greeting'),
    notificationButton: t('templates.starters.notificationButton'),
    newsletterKicker: t('templates.starters.newsletterKicker'),
    newsletterButton: t('templates.starters.newsletterButton'),
    newsletterFooter: t('templates.starters.newsletterFooter'),
    sampleFirstName: t('templates.starters.sampleFirstName'),
    sampleCompany: t('templates.starters.sampleCompany'),
    sampleTitle: t('templates.starters.sampleTitle'),
    sampleMessage: t('templates.starters.sampleMessage'),
    sampleIssueTitle: t('templates.starters.sampleIssueTitle'),
    sampleIntro: t('templates.starters.sampleIntro'),
  };
}

export const starterKey = {
  blank: 'templates.starterBlank',
  welcome: 'templates.starterWelcome',
  notification: 'templates.starterNotification',
  newsletter: 'templates.starterNewsletter',
} as const satisfies Record<TemplateStarter, string>;

function sameSamples(
  left: Readonly<Record<string, string>>,
  right: Readonly<Record<string, string>>
): boolean {
  const keys = new Set([...Object.keys(left), ...Object.keys(right)]);
  for (const key of keys) {
    if ((left[key] ?? '') !== (right[key] ?? '')) return false;
  }
  return true;
}

export function sameDraft(left: Draft, right: Draft): boolean {
  return (
    left.name === right.name &&
    left.subject === right.subject &&
    left.html === right.html &&
    left.text === right.text &&
    sameSamples(left.samples, right.samples) &&
    JSON.stringify(left.design) === JSON.stringify(right.design)
  );
}

export function validDraft(
  draft: Draft,
  variables: readonly string[] | null
): variables is readonly string[] {
  if (!variables) return false;
  const name = draft.name.trim();
  const subject = draft.subject.trim();
  if (name === '' || Array.from(name).length > MAIL_TEMPLATE_NAME_MAX_LENGTH) return false;
  if (
    subject === '' ||
    Array.from(subject).length > MAIL_SUBJECT_MAX_LENGTH ||
    subject.includes('\r') ||
    subject.includes('\n') ||
    subject.includes('\0')
  ) {
    return false;
  }
  return draft.html.trim() !== '' || draft.text.trim() !== '';
}
