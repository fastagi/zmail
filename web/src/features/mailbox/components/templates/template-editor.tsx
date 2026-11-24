'use client';

import { useId } from 'react';
import {
  AlertCircle,
  ArrowLeft,
  Code2,
  LayoutTemplate,
  Save,
  Send,
  Trash2,
} from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { TemplateVisualEditor } from '@/features/mailbox/components/template-visual-editor';
import {
  DeleteTemplateDialog,
  DiscardChangesDialog,
  SwitchModeDialog,
  TemplateTestDialog,
} from '@/features/mailbox/components/templates/template-dialogs';
import { starterKey } from '@/features/mailbox/components/templates/template-draft';
import { TemplatePreview } from '@/features/mailbox/components/templates/template-preview';
import { useTemplateEditor } from '@/features/mailbox/components/templates/use-template-editor';
import { useMailTemplate } from '@/features/mailbox/hooks/use-mail-templates';
import {
  MAIL_SUBJECT_MAX_LENGTH,
  MAIL_TEMPLATE_NAME_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import type { MailTemplate } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { templateStarters } from '@/features/mailbox/utils/template-starters';
import { useT } from '@/i18n';

/** Placeholder forms shown as examples; they are template syntax, not interface copy. */
const valueExample = '{{first_name}}';
const fallbackExample = '{{first_name|there}}';

export function LoadedTemplateEditor({
  templateId,
  onBack,
}: {
  templateId: number;
  onBack: () => void;
}) {
  const t = useT('mailbox');
  const template = useMailTemplate(useMailOrganizationId(), templateId);
  if (template.isPending) return <Skeleton className="h-96 w-full" aria-hidden="true" />;
  if (template.error || !template.data) {
    return (
      <div className="space-y-3">
        <BackButton onBack={onBack} />
        <ErrorAlert
          message={t(resolveMailboxErrorKey(template.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void template.refetch()}
        />
      </div>
    );
  }
  return <TemplateEditor template={template.data} onBack={onBack} />;
}

function BackButton({ onBack }: { onBack: () => void }) {
  const t = useT('mailbox');
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      icon={<ArrowLeft className="size-4" />}
      onClick={onBack}
    >
      {t('templates.back')}
    </Button>
  );
}

export function TemplateEditor({
  template,
  onBack,
  onSaved,
}: {
  template: MailTemplate | null;
  onBack: () => void;
  onSaved?: (templateId: number) => void;
}) {
  const t = useT('mailbox');
  const editor = useTemplateEditor({ template, onBack, onSaved });
  const ids = { name: useId(), subject: useId(), html: useId(), text: useId(), textHint: useId() };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <BackButton onBack={editor.requestBack} />
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={editor.saving}
            icon={<Send className="size-4" />}
            onClick={() => void editor.openTest()}
          >
            {t('templates.test')}
          </Button>
          {editor.savedId !== null ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={editor.saving}
              icon={<Trash2 className="size-4" />}
              onClick={() => editor.setDeleting(true)}
            >
              {t('templates.delete')}
            </Button>
          ) : null}
        </div>
      </div>

      {editor.savedId === null ? (
        <div
          role="group"
          aria-label={t('templates.startFrom')}
          className="flex flex-wrap items-center gap-2"
        >
          <span className="text-sm text-muted-foreground">{t('templates.startFrom')}</span>
          {templateStarters.map(starter => (
            <Button
              key={starter}
              type="button"
              size="xs"
              variant="outline"
              onClick={() => editor.applyStarter(starter)}
            >
              {t(starterKey[starter])}
            </Button>
          ))}
        </div>
      ) : null}

      <div className="grid gap-6 lg:grid-cols-2">
        <form onSubmit={editor.submit} className="min-w-0 space-y-4">
          <div className="space-y-2">
            <Label htmlFor={ids.name}>{t('templates.name')}</Label>
            <Input
              id={ids.name}
              value={editor.draft.name}
              maxLength={MAIL_TEMPLATE_NAME_MAX_LENGTH}
              placeholder={t('templates.namePlaceholder')}
              disabled={editor.saving}
              onChange={event => editor.update({ name: event.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={ids.subject}>{t('templates.subject')}</Label>
            <Input
              id={ids.subject}
              value={editor.draft.subject}
              maxLength={MAIL_SUBJECT_MAX_LENGTH}
              spellCheck={false}
              disabled={editor.saving}
              onChange={event => editor.update({ subject: event.target.value })}
            />
          </div>
          <div
            role="group"
            aria-label={t('templates.visual.mode')}
            className="inline-flex rounded-md border p-0.5"
          >
            <Button
              type="button"
              size="xs"
              variant={editor.visual ? 'secondary' : 'ghost'}
              aria-pressed={editor.visual}
              icon={<LayoutTemplate className="size-3.5" />}
              disabled={editor.saving}
              onClick={() => editor.requestMode('visual')}
            >
              {t('templates.visual.modeVisual')}
            </Button>
            <Button
              type="button"
              size="xs"
              variant={editor.visual ? 'ghost' : 'secondary'}
              aria-pressed={!editor.visual}
              icon={<Code2 className="size-3.5" />}
              disabled={editor.saving}
              onClick={() => editor.requestMode('html')}
            >
              {t('templates.visual.modeHtml')}
            </Button>
          </div>
          {editor.draft.design ? (
            <>
              <TemplateVisualEditor
                design={editor.draft.design}
                variables={editor.variables ?? []}
                disabled={editor.saving}
                onChange={editor.updateDesign}
              />
              <p className="text-xs text-muted-foreground">{t('templates.visual.textGenerated')}</p>
            </>
          ) : (
            <>
              <div className="space-y-2">
                <Label htmlFor={ids.html}>{t('templates.html')}</Label>
                <Textarea
                  id={ids.html}
                  value={editor.draft.html}
                  rows={14}
                  spellCheck={false}
                  className="font-mono text-xs"
                  disabled={editor.saving}
                  onChange={event => editor.update({ html: event.target.value })}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor={ids.text}>{t('templates.text')}</Label>
                <Textarea
                  id={ids.text}
                  value={editor.draft.text}
                  rows={5}
                  aria-describedby={ids.textHint}
                  disabled={editor.saving}
                  onChange={event => editor.update({ text: event.target.value })}
                />
                <p id={ids.textHint} className="text-xs text-muted-foreground">
                  {t('templates.textHint')}
                </p>
              </div>
            </>
          )}

          <section className="space-y-2 rounded-md border bg-muted/30 p-3 text-sm">
            <h3 className="font-medium">{t('templates.syntaxTitle')}</h3>
            <p>
              <code className="font-mono text-xs">{valueExample}</code> {t('templates.syntaxValue')}
            </p>
            <p>
              <code className="font-mono text-xs">{fallbackExample}</code>{' '}
              {t('templates.syntaxFallback')}
            </p>
            <p className="text-muted-foreground">{t('templates.syntaxEscape')}</p>
          </section>

          <SampleValues
            variables={editor.variables}
            samples={editor.draft.samples}
            disabled={editor.saving}
            onChange={samples => editor.update({ samples })}
          />

          {editor.errorKey ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertDescription>{t(editor.errorKey)}</AlertDescription>
            </Alert>
          ) : null}
          <div className="flex justify-end">
            <Button
              type="submit"
              loading={editor.saving}
              disabled={!editor.dirty && editor.savedId !== null}
              icon={<Save className="size-4" />}
            >
              {t('templates.save')}
            </Button>
          </div>
        </form>

        <TemplatePreview
          rendered={editor.rendered}
          tab={editor.tab}
          onTabChange={editor.setTab}
        />
      </div>

      {editor.testing ? (
        <TemplateTestDialog
          templateId={editor.savedId}
          unsaved={editor.dirty && editor.savedId !== null}
          onClose={() => editor.setTesting(false)}
        />
      ) : null}
      {editor.discarding ? (
        <DiscardChangesDialog
          onConfirm={() => {
            editor.setDiscarding(false);
            onBack();
          }}
          onClose={() => editor.setDiscarding(false)}
        />
      ) : null}
      {editor.switching ? (
        <SwitchModeDialog
          mode={editor.switching}
          onConfirm={() => {
            if (editor.switching) editor.switchTo(editor.switching);
          }}
          onClose={() => editor.setSwitching(null)}
        />
      ) : null}
      {editor.deleting && editor.savedId !== null ? (
        <DeleteTemplateDialog
          templateId={editor.savedId}
          name={editor.saved.name}
          onClose={() => editor.setDeleting(false)}
          onDeleted={onBack}
        />
      ) : null}
    </div>
  );
}

function SampleValues({
  variables,
  samples,
  disabled,
  onChange,
}: {
  variables: string[] | null;
  samples: Record<string, string>;
  disabled: boolean;
  onChange: (samples: Record<string, string>) => void;
}) {
  const t = useT('mailbox');
  const hintId = useId();
  const baseId = useId();
  if (!variables) return null;
  return (
    <fieldset className="space-y-2" aria-describedby={hintId}>
      <legend className="text-sm font-medium">{t('templates.samples')}</legend>
      <p id={hintId} className="text-xs text-muted-foreground">
        {t('templates.samplesHint')}
      </p>
      {variables.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('templates.noVariables')}</p>
      ) : (
        <div className="grid gap-2 sm:grid-cols-[minmax(0,10rem)_minmax(0,1fr)] sm:items-center">
          {variables.map(name => (
            <div key={name} className="contents">
              <Label htmlFor={`${baseId}-${name}`} className="font-mono text-xs">
                {name}
              </Label>
              <Input
                id={`${baseId}-${name}`}
                value={samples[name] ?? ''}
                disabled={disabled}
                onChange={event => onChange({ ...samples, [name]: event.target.value })}
              />
            </div>
          ))}
        </div>
      )}
    </fieldset>
  );
}
