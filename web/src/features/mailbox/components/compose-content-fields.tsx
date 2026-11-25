'use client';

import { useId } from 'react';

import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { MAIL_SUBJECT_MAX_LENGTH } from '@/features/mailbox/schemas';
import type { MailTemplate } from '@/features/mailbox/types';
import { useT } from '@/i18n';

const NO_TEMPLATE = '__none__';

type ComposeTemplateOption = Pick<MailTemplate, 'id' | 'name' | 'variables'>;

export function ComposeContentFields({
  isReplyOrForward,
  templates,
  templateId,
  template,
  values,
  subject,
  body,
  disabled,
  onSelectTemplate,
  onChangeValue,
  onChangeSubject,
  onChangeBody,
}: {
  isReplyOrForward: boolean;
  templates: readonly ComposeTemplateOption[] | undefined;
  templateId: number | null;
  template: ComposeTemplateOption | null;
  values: Record<string, string>;
  subject: string;
  body: string;
  disabled: boolean;
  onSelectTemplate: (templateId: number | null) => void;
  onChangeValue: (name: string, value: string) => void;
  onChangeSubject: (value: string) => void;
  onChangeBody: (value: string) => void;
}) {
  const t = useT('mailbox');
  const templateHintId = useId();

  return (
    <>
      {!isReplyOrForward && templates && templates.length > 0 ? (
        <div className="space-y-2">
          <Label htmlFor="mail-compose-template">{t('compose.template')}</Label>
          <Select
            value={templateId === null ? NO_TEMPLATE : String(templateId)}
            disabled={disabled}
            onValueChange={value =>
              onSelectTemplate(value === NO_TEMPLATE ? null : Number(value))
            }
          >
            <SelectTrigger id="mail-compose-template" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NO_TEMPLATE}>{t('compose.templateNone')}</SelectItem>
              {templates.map(option => (
                <SelectItem key={option.id} value={String(option.id)}>
                  {option.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      ) : null}
      {template ? (
        template.variables.length > 0 ? (
          <fieldset className="space-y-2" aria-describedby={templateHintId}>
            <legend className="text-sm font-medium">{t('templates.variables')}</legend>
            <p id={templateHintId} className="text-xs text-muted-foreground">
              {t('compose.templateHint')}
            </p>
            {template.variables.map(name => (
              <div key={name} className="space-y-1">
                <Label htmlFor={`mail-compose-variable-${name}`} className="font-mono text-xs">
                  {name}
                </Label>
                <Input
                  id={`mail-compose-variable-${name}`}
                  value={values[name] ?? ''}
                  disabled={disabled}
                  onChange={event => onChangeValue(name, event.target.value)}
                />
              </div>
            ))}
          </fieldset>
        ) : null
      ) : (
        <>
          <div className="space-y-2">
            <Label htmlFor="mail-compose-subject">{t('compose.subject')}</Label>
            <Input
              id="mail-compose-subject"
              value={subject}
              maxLength={MAIL_SUBJECT_MAX_LENGTH}
              disabled={disabled}
              onChange={event => onChangeSubject(event.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="mail-compose-body">{t('compose.body')}</Label>
            <Textarea
              id="mail-compose-body"
              value={body}
              rows={8}
              disabled={disabled}
              onChange={event => onChangeBody(event.target.value)}
            />
          </div>
        </>
      )}
    </>
  );
}
