'use client';

import { useId, useMemo, useState, type FormEvent } from 'react';
import {
  AlertCircle,
  ArrowLeft,
  Code2,
  FileText,
  LayoutTemplate,
  Plus,
  Save,
  Send,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  MailHtmlFrame,
  nativeSelectClassName,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import { TemplateVisualEditor } from '@/features/mailbox/components/template-visual-editor';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import {
  useDeleteMailTemplate,
  useMailTemplate,
  useMailTemplates,
  useSaveMailTemplate,
  useSendMailTemplateTest,
} from '@/features/mailbox/hooks/use-mail-templates';
import {
  MAIL_SUBJECT_MAX_LENGTH,
  MAIL_TEMPLATE_NAME_MAX_LENGTH,
  MAIL_TEMPLATE_TEST_MAX_RECIPIENTS,
  normalizeRecipient,
  splitRecipients,
} from '@/features/mailbox/schemas';
import type { MailTemplate, MailTemplateInput } from '@/features/mailbox/types';
import {
  isOrganizationLost,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import {
  emptyDesign,
  readDesign,
  renderDesignHtml,
  renderDesignText,
  type TemplateDesign,
} from '@/features/mailbox/utils/template-design';
import { renderTemplate, templateVariables } from '@/features/mailbox/utils/template-render';
import {
  starterContent,
  starterDesign,
  templateStarters,
  type StarterPhrases,
  type TemplateStarter,
} from '@/features/mailbox/utils/template-starters';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** Placeholder forms shown as examples; they are template syntax, not interface copy. */
const valueExample = '{{first_name}}';
const fallbackExample = '{{first_name|there}}';

/**
 * Templates console: a list of the organization's templates and an editor with starters, sample
 * values, a live preview, and test sends. The preview renders in the browser with the same rules
 * as the API; saving is checked again by the API.
 */
export function MailTemplates() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader
        title={t('templates.title')}
        description={t('templates.description')}
        scope={scope}
      />
      <MailOrganizationBoundary scope={scope}>
        {() => <TemplateWorkspace />}
      </MailOrganizationBoundary>
    </div>
  );
}

type Editing = { kind: 'list' } | { kind: 'new' } | { kind: 'edit'; templateId: number };

function TemplateWorkspace() {
  const [editing, setEditing] = useState<Editing>({ kind: 'list' });
  const back = () => setEditing({ kind: 'list' });
  if (editing.kind === 'new') {
    return (
      <TemplateEditor
        template={null}
        onBack={back}
        onSaved={templateId => setEditing({ kind: 'edit', templateId })}
      />
    );
  }
  if (editing.kind === 'edit') {
    return <LoadedTemplateEditor templateId={editing.templateId} onBack={back} />;
  }
  return (
    <TemplateList
      onCreate={() => setEditing({ kind: 'new' })}
      onOpen={templateId => setEditing({ kind: 'edit', templateId })}
    />
  );
}

function TemplateList({
  onCreate,
  onOpen,
}: {
  onCreate: () => void;
  onOpen: (templateId: number) => void;
}) {
  const t = useT('mailbox');
  const templates = useMailTemplates(useMailOrganizationId());
  const formatter = useDateTimeFormat();

  const create = (
    <Button type="button" icon={<Plus className="size-4" />} onClick={onCreate}>
      {t('templates.new')}
    </Button>
  );

  if (templates.isPending) {
    return (
      <div className="space-y-3" aria-hidden="true">
        <Skeleton className="h-10 w-40" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  if (templates.error) {
    if (isOrganizationLost(templates.error)) return <OrganizationLostState />;
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(templates.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void templates.refetch()}
      />
    );
  }
  if (templates.data.length === 0) {
    return (
      <EmptyState
        icon={<FileText className="size-5" />}
        title={t('templates.emptyTitle')}
        description={t('templates.emptyDescription')}
        action={create}
      />
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex justify-end">{create}</div>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('templates.name')}</TableHead>
              <TableHead>{t('templates.subject')}</TableHead>
              <TableHead>{t('templates.variables')}</TableHead>
              <TableHead className="text-right">{t('templates.updated')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {templates.data.map(template => (
              <TableRow key={template.id}>
                <TableCell className="max-w-56 truncate font-medium">
                  <button
                    type="button"
                    className="max-w-full truncate text-left hover:underline focus-ring"
                    aria-label={t('templates.edit', { name: template.name })}
                    onClick={() => onOpen(template.id)}
                  >
                    {template.name}
                  </button>
                </TableCell>
                <TableCell className="max-w-72 truncate text-muted-foreground">
                  {template.subject}
                </TableCell>
                <TableCell>
                  {template.variables.length === 0 ? (
                    <span className="text-xs text-muted-foreground">{t('templates.none')}</span>
                  ) : (
                    <span className="flex flex-wrap gap-1">
                      {template.variables.map(name => (
                        <Badge key={name} variant="secondary" className="font-mono text-xs">
                          {name}
                        </Badge>
                      ))}
                    </span>
                  )}
                </TableCell>
                <TableCell className="text-right whitespace-nowrap text-muted-foreground">
                  <time dateTime={template.updated_at}>
                    {formatter.format(new Date(template.updated_at))}
                  </time>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

function LoadedTemplateEditor({ templateId, onBack }: { templateId: number; onBack: () => void }) {
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
  return <TemplateEditor template={template.data} onBack={onBack} onSaved={() => undefined} />;
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

interface Draft {
  name: string;
  subject: string;
  html: string;
  text: string;
  samples: Record<string, string>;
  /** The visual design the bodies are rendered from; null in the HTML editor. */
  design: TemplateDesign | null;
}

/** A new template starts in the visual editor; a stored one opens the way it was written. */
function draftOf(template: MailTemplate | null): Draft {
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
function withDesign(draft: Draft, design: TemplateDesign): Draft {
  return { ...draft, design, html: renderDesignHtml(design), text: renderDesignText(design) };
}

/** The input the API receives: sample values only for variables the template uses. */
function inputOf(draft: Draft, variables: readonly string[]): MailTemplateInput {
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

function useStarterPhrases(): StarterPhrases {
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

const starterKey = {
  blank: 'templates.starterBlank',
  welcome: 'templates.starterWelcome',
  notification: 'templates.starterNotification',
  newsletter: 'templates.starterNewsletter',
} as const satisfies Record<TemplateStarter, string>;

function TemplateEditor({
  template,
  onBack,
  onSaved,
}: {
  template: MailTemplate | null;
  onBack: () => void;
  onSaved: (templateId: number) => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const save = useSaveMailTemplate(organizationId);
  const phrases = useStarterPhrases();
  const [draft, setDraft] = useState<Draft>(() => draftOf(template));
  const [saved, setSaved] = useState<Draft>(() => draftOf(template));
  const [savedId, setSavedId] = useState<number | null>(template?.id ?? null);
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(null);
  const [testing, setTesting] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [switching, setSwitching] = useState<'visual' | 'html' | null>(null);
  const [tab, setTab] = useState<'html' | 'text'>('html');
  const visual = draft.design !== null;
  const ids = { name: useId(), subject: useId(), html: useId(), text: useId(), textHint: useId() };

  const source = { subject: draft.subject, html: draft.html, text: draft.text };
  const variables = useMemo(
    () => templateVariables({ subject: draft.subject, html: draft.html, text: draft.text }),
    [draft.subject, draft.html, draft.text]
  );
  const rendered = useMemo(
    () => renderTemplate(source, draft.samples),
    // The source fields and samples fully determine the render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [draft.subject, draft.html, draft.text, draft.samples]
  );
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);

  const update = (change: Partial<Draft>) => {
    setErrorKey(null);
    setDraft(current => ({ ...current, ...change }));
  };

  const updateDesign = (design: TemplateDesign) => {
    setErrorKey(null);
    setDraft(current => withDesign(current, design));
  };

  /** Visual to HTML keeps the rendered HTML; HTML to visual starts a new design. */
  const switchTo = (mode: 'visual' | 'html') => {
    setSwitching(null);
    setErrorKey(null);
    setDraft(current =>
      mode === 'html' ? { ...current, design: null } : withDesign(current, emptyDesign())
    );
  };

  const requestMode = (mode: 'visual' | 'html') => {
    if ((mode === 'visual') === visual) return;
    const empty = draft.html.trim() === '' && draft.text.trim() === '';
    if (mode === 'visual' && empty) switchTo('visual');
    else setSwitching(mode);
  };

  const applyStarter = (starter: TemplateStarter) => {
    if (visual) {
      const content = starterDesign(starter, phrases);
      setErrorKey(null);
      setDraft(current =>
        withDesign(
          { ...current, subject: content.subject, samples: { ...content.sample_variables } },
          content.design
        )
      );
      return;
    }
    const content = starterContent(starter, phrases);
    update({
      subject: content.subject,
      html: content.html ?? '',
      text: content.text ?? '',
      samples: { ...(content.sample_variables ?? {}) },
    });
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!variables) {
      setErrorKey('errors.templateInvalid');
      return;
    }
    try {
      const stored = await save.mutateAsync({
        input: inputOf(draft, variables),
        ...(savedId === null ? {} : { templateId: savedId }),
      });
      const next = draftOf(stored);
      setDraft(next);
      setSaved(next);
      toast.success(t(savedId === null ? 'templates.created' : 'templates.saved'));
      setSavedId(stored.id);
      onSaved(stored.id);
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <BackButton onBack={onBack} />
        {savedId !== null ? (
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Send className="size-4" />}
              onClick={() => setTesting(true)}
            >
              {t('templates.test')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Trash2 className="size-4" />}
              onClick={() => setDeleting(true)}
            >
              {t('templates.delete')}
            </Button>
          </div>
        ) : null}
      </div>

      {savedId === null ? (
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
              onClick={() => applyStarter(starter)}
            >
              {t(starterKey[starter])}
            </Button>
          ))}
        </div>
      ) : null}

      <div className="grid gap-6 lg:grid-cols-2">
        <form onSubmit={submit} className="min-w-0 space-y-4">
          <div className="space-y-2">
            <Label htmlFor={ids.name}>{t('templates.name')}</Label>
            <Input
              id={ids.name}
              value={draft.name}
              maxLength={MAIL_TEMPLATE_NAME_MAX_LENGTH}
              placeholder={t('templates.namePlaceholder')}
              disabled={save.isPending}
              onChange={event => update({ name: event.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={ids.subject}>{t('templates.subject')}</Label>
            <Input
              id={ids.subject}
              value={draft.subject}
              maxLength={MAIL_SUBJECT_MAX_LENGTH}
              spellCheck={false}
              disabled={save.isPending}
              onChange={event => update({ subject: event.target.value })}
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
              variant={visual ? 'secondary' : 'ghost'}
              aria-pressed={visual}
              icon={<LayoutTemplate className="size-3.5" />}
              disabled={save.isPending}
              onClick={() => requestMode('visual')}
            >
              {t('templates.visual.modeVisual')}
            </Button>
            <Button
              type="button"
              size="xs"
              variant={visual ? 'ghost' : 'secondary'}
              aria-pressed={!visual}
              icon={<Code2 className="size-3.5" />}
              disabled={save.isPending}
              onClick={() => requestMode('html')}
            >
              {t('templates.visual.modeHtml')}
            </Button>
          </div>
          {draft.design ? (
            <>
              <TemplateVisualEditor
                design={draft.design}
                variables={variables ?? []}
                disabled={save.isPending}
                onChange={updateDesign}
              />
              <p className="text-xs text-muted-foreground">{t('templates.visual.textGenerated')}</p>
            </>
          ) : (
            <>
              <div className="space-y-2">
                <Label htmlFor={ids.html}>{t('templates.html')}</Label>
                <Textarea
                  id={ids.html}
                  value={draft.html}
                  rows={14}
                  spellCheck={false}
                  className="font-mono text-xs"
                  disabled={save.isPending}
                  onChange={event => update({ html: event.target.value })}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor={ids.text}>{t('templates.text')}</Label>
                <Textarea
                  id={ids.text}
                  value={draft.text}
                  rows={5}
                  aria-describedby={ids.textHint}
                  disabled={save.isPending}
                  onChange={event => update({ text: event.target.value })}
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
            variables={variables}
            samples={draft.samples}
            disabled={save.isPending}
            onChange={samples => update({ samples })}
          />

          {errorKey ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertDescription>{t(errorKey)}</AlertDescription>
            </Alert>
          ) : null}
          <div className="flex justify-end">
            <Button
              type="submit"
              loading={save.isPending}
              disabled={!dirty && savedId !== null}
              icon={<Save className="size-4" />}
            >
              {t('templates.save')}
            </Button>
          </div>
        </form>

        <section
          aria-label={t('templates.preview')}
          className="min-w-0 space-y-3 lg:sticky lg:top-4 lg:self-start"
        >
          <h3 className="text-sm font-medium">{t('templates.preview')}</h3>
          {rendered ? (
            <>
              <p className="rounded-md border px-3 py-2 text-sm font-medium break-words">
                {rendered.subject || ' '}
              </p>
              {rendered.missing.length > 0 ? (
                <Alert>
                  <AlertCircle className="size-4" aria-hidden="true" />
                  <AlertDescription>
                    {t('templates.missing', { names: rendered.missing.join(', ') })}
                  </AlertDescription>
                </Alert>
              ) : null}
              <Tabs value={tab} onValueChange={value => setTab(value as 'html' | 'text')}>
                <TabsList>
                  <TabsTrigger value="html">{t('templates.previewHtml')}</TabsTrigger>
                  <TabsTrigger value="text">{t('templates.previewText')}</TabsTrigger>
                </TabsList>
                <TabsContent value="html" className="pt-2">
                  <MailHtmlFrame html={rendered.html} title={t('templates.previewFrame')} />
                </TabsContent>
                <TabsContent value="text" className="pt-2">
                  {rendered.text ? (
                    <pre className="max-h-96 overflow-auto rounded-md border p-3 font-sans text-sm break-words whitespace-pre-wrap">
                      {rendered.text}
                    </pre>
                  ) : (
                    <p className="text-sm text-muted-foreground">{t('templates.noText')}</p>
                  )}
                </TabsContent>
              </Tabs>
            </>
          ) : (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertDescription>{t('templates.syntaxError')}</AlertDescription>
            </Alert>
          )}
        </section>
      </div>

      {testing && savedId !== null ? (
        <TemplateTestDialog
          templateId={savedId}
          unsaved={dirty}
          onClose={() => setTesting(false)}
        />
      ) : null}
      {switching ? (
        <SwitchModeDialog
          mode={switching}
          onConfirm={() => switchTo(switching)}
          onClose={() => setSwitching(null)}
        />
      ) : null}
      {deleting && savedId !== null ? (
        <DeleteTemplateDialog
          templateId={savedId}
          name={saved.name}
          onClose={() => setDeleting(false)}
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

function TemplateTestDialog({
  templateId,
  unsaved,
  onClose,
}: {
  templateId: number;
  unsaved: boolean;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const mailboxes = useMailboxes(organizationId);
  const send = useSendMailTemplateTest(organizationId);
  const [mailboxId, setMailboxId] = useState<number | null>(null);
  const [to, setTo] = useState('');
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(null);
  const ids = { from: useId(), to: useId(), hint: useId() };
  // A test send needs `send` on the mailbox it goes from.
  const options = (mailboxes.data ?? []).filter(mailbox => mailbox.access.send);
  const from = mailboxId ?? options[0]?.id ?? null;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorKey(null);
    const recipients = splitRecipients(to).map(normalizeRecipient);
    if (
      from === null ||
      recipients.length === 0 ||
      recipients.length > MAIL_TEMPLATE_TEST_MAX_RECIPIENTS ||
      recipients.some(recipient => recipient === null)
    ) {
      setErrorKey('errors.messageInvalid');
      return;
    }
    try {
      const stored = await send.mutateAsync({
        templateId,
        input: { mailbox_id: from, to: recipients as string[] },
      });
      toast.success(t(stored.queued_reason === 'send_limit' ? 'messages.queued' : 'templates.testSent'));
      onClose();
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !send.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('templates.testTitle')}</DialogTitle>
          <DialogDescription>{t('templates.testDescription')}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit}>
          <DialogBody className="space-y-4">
            {unsaved ? (
              <Alert>
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{t('templates.testNeedsSave')}</AlertDescription>
              </Alert>
            ) : null}
            {mailboxes.isSuccess && options.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t('templates.testNeedsMailbox')}</p>
            ) : (
              <div className="space-y-2">
                <Label htmlFor={ids.from}>{t('templates.testFrom')}</Label>
                <select
                  id={ids.from}
                  className={cn(nativeSelectClassName, 'w-full')}
                  value={from === null ? '' : String(from)}
                  disabled={send.isPending || options.length === 0}
                  onChange={event => setMailboxId(Number(event.target.value))}
                >
                  {options.map(option => (
                    <option key={option.id} value={String(option.id)}>
                      {option.address}
                    </option>
                  ))}
                </select>
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor={ids.to}>{t('templates.testTo')}</Label>
              <Input
                id={ids.to}
                value={to}
                spellCheck={false}
                aria-describedby={ids.hint}
                disabled={send.isPending}
                onChange={event => setTo(event.target.value)}
              />
              <p id={ids.hint} className="text-xs text-muted-foreground">
                {t('templates.testToHint')}
              </p>
            </div>
            {errorKey ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{t(errorKey)}</AlertDescription>
              </Alert>
            ) : null}
          </DialogBody>
          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" disabled={send.isPending} onClick={onClose}>
              {t('actions.cancel')}
            </Button>
            <Button
              type="submit"
              loading={send.isPending}
              disabled={from === null}
              icon={<Send className="size-4" />}
            >
              {t('templates.test')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function DeleteTemplateDialog({
  templateId,
  name,
  onClose,
  onDeleted,
}: {
  templateId: number;
  name: string;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const t = useT('mailbox');
  const remove = useDeleteMailTemplate(useMailOrganizationId());

  const confirm = async () => {
    try {
      await remove.mutateAsync(templateId);
      toast.success(t('templates.deleted'));
      onClose();
      onDeleted();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !remove.isPending && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{t('templates.deleteTitle')}</DialogTitle>
          <DialogDescription>{t('templates.deleteDescription', { name })}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={remove.isPending} onClick={onClose}>
            {t('actions.cancel')}
          </Button>
          <Button
            type="button"
            variant="destructive"
            loading={remove.isPending}
            icon={<Trash2 className="size-4" />}
            onClick={() => void confirm()}
          >
            {t('actions.delete')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function SwitchModeDialog({
  mode,
  onConfirm,
  onClose,
}: {
  mode: 'visual' | 'html';
  onConfirm: () => void;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const toHtml = mode === 'html';
  return (
    <Dialog open onOpenChange={open => !open && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>
            {t(toHtml ? 'templates.visual.toHtmlTitle' : 'templates.visual.toVisualTitle')}
          </DialogTitle>
          <DialogDescription>
            {t(
              toHtml ? 'templates.visual.toHtmlDescription' : 'templates.visual.toVisualDescription'
            )}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('actions.cancel')}
          </Button>
          <Button type="button" onClick={onConfirm}>
            {t(toHtml ? 'templates.visual.toHtmlConfirm' : 'templates.visual.toVisualConfirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
