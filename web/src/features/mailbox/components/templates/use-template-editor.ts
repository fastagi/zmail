'use client';

import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { toast } from 'sonner';

import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  draftOf,
  inputOf,
  sameDraft,
  useStarterPhrases,
  validDraft,
  withDesign,
  type Draft,
} from '@/features/mailbox/components/templates/template-draft';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import { useSaveMailTemplate } from '@/features/mailbox/hooks/use-mail-templates';
import type { MailTemplate } from '@/features/mailbox/types';
import { resolveMailboxErrorKey, type MailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { emptyDesign, type TemplateDesign } from '@/features/mailbox/utils/template-design';
import { renderTemplate, templateVariables } from '@/features/mailbox/utils/template-render';
import {
  starterContent,
  starterDesign,
  type TemplateStarter,
} from '@/features/mailbox/utils/template-starters';
import { useT } from '@/i18n';

export function useTemplateEditor({
  template,
  onBack,
  onSaved,
}: {
  template: MailTemplate | null;
  onBack: () => void;
  onSaved?: (templateId: number) => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const mailboxes = useMailboxes(organizationId);
  const save = useSaveMailTemplate(organizationId);
  const phrases = useStarterPhrases();
  const [draft, setDraft] = useState<Draft>(() => draftOf(template));
  const [saved, setSaved] = useState<Draft>(() => draftOf(template));
  const [savedId, setSavedId] = useState<number | null>(template?.id ?? null);
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(null);
  const [testing, setTesting] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [discarding, setDiscarding] = useState(false);
  const [switching, setSwitching] = useState<'visual' | 'html' | null>(null);
  const [tab, setTab] = useState<'html' | 'text'>('html');
  const visual = draft.design !== null;
  const noMailboxes = mailboxes.isSuccess && (mailboxes.data?.length ?? 0) === 0;

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
  const dirty = !sameDraft(draft, saved);

  useEffect(() => {
    if (!dirty) return undefined;
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => window.removeEventListener('beforeunload', handleBeforeUnload);
  }, [dirty]);

  const requestBack = () => {
    if (dirty) setDiscarding(true);
    else onBack();
  };

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

  const persist = async (): Promise<number | null> => {
    if (!validDraft(draft, variables)) {
      setErrorKey('errors.templateInvalid');
      return null;
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
      onSaved?.(stored.id);
      return stored.id;
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
      return null;
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    await persist();
  };

  const openTest = async () => {
    setErrorKey(null);
    if (!noMailboxes && (savedId === null || dirty)) {
      const id = await persist();
      if (id === null) return;
    }
    setTesting(true);
  };

  return {
    draft,
    saved,
    savedId,
    errorKey,
    testing,
    setTesting,
    deleting,
    setDeleting,
    discarding,
    setDiscarding,
    switching,
    setSwitching,
    tab,
    setTab,
    visual,
    variables,
    rendered,
    dirty,
    saving: save.isPending,
    requestBack,
    update,
    updateDesign,
    switchTo,
    requestMode,
    applyStarter,
    submit,
    openTest,
  };
}
