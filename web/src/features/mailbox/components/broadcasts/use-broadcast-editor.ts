'use client';

import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { toast } from 'sonner';

import {
  defaultScheduleValue,
  domainOf,
  scheduleProblem,
  viewerTimeZone,
  type EditorErrorKey,
} from '@/features/mailbox/components/broadcasts/broadcast-shared';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  useDeleteMailBroadcast,
  useSaveMailBroadcast,
  useSendMailBroadcast,
} from '@/features/mailbox/hooks/use-mail-broadcasts';
import { useMailContactLists } from '@/features/mailbox/hooks/use-mail-contacts';
import { useMailTemplate, useMailTemplates } from '@/features/mailbox/hooks/use-mail-templates';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import { MAIL_BROADCAST_TEST_WAIT_DEFAULT_MINUTES } from '@/features/mailbox/schemas';
import type { MailBroadcast } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export type EditorConfirmKind = 'send' | 'schedule' | 'delete' | 'discard' | null;

export interface UseBroadcastEditorOptions {
  broadcast: MailBroadcast | null;
  onBack: () => void;
  onSaved: (broadcastId: number) => void;
}

/** Headless state and mutation orchestration for creating or editing a broadcast draft. */
export function useBroadcastEditor({ broadcast, onBack, onSaved }: UseBroadcastEditorOptions) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const mailboxes = useMailboxes(organizationId);
  const templates = useMailTemplates(organizationId, 'standard');
  const lists = useMailContactLists(organizationId);
  const save = useSaveMailBroadcast(organizationId);
  const send = useSendMailBroadcast(organizationId);
  const remove = useDeleteMailBroadcast(organizationId);

  const [name, setName] = useState(broadcast?.name ?? '');
  const [mailboxId, setMailboxId] = useState<number | null>(broadcast?.mailbox_id ?? null);
  const [templateId, setTemplateId] = useState<number | null>(broadcast?.template_id ?? null);
  const selectedTemplate = useMailTemplate(organizationId, templateId);
  const [listId, setListId] = useState<number | null>(broadcast?.list_id ?? null);
  const [label, setLabel] = useState(broadcast?.unsubscribe_label ?? '');
  const [variables, setVariables] = useState<Record<string, string>>(broadcast?.variables ?? {});
  const [testPercent, setTestPercent] = useState(broadcast?.test_percent ?? 0);
  const [testWait, setTestWait] = useState(
    broadcast?.test_wait_minutes || MAIL_BROADCAST_TEST_WAIT_DEFAULT_MINUTES
  );
  const [fromDomain, setFromDomain] = useState<string | null>(null);
  const [savedId, setSavedId] = useState<number | null>(broadcast?.id ?? null);
  const [dirty, setDirty] = useState(false);
  const [errorKey, setErrorKey] = useState<EditorErrorKey | null>(null);
  const [confirm, setConfirm] = useState<EditorConfirmKind>(null);
  const [scheduleAt, setScheduleAt] = useState(defaultScheduleValue);
  const zone = useMemo(() => viewerTimeZone(), []);

  useEffect(() => {
    if (!dirty) return undefined;
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => window.removeEventListener('beforeunload', handleBeforeUnload);
  }, [dirty]);

  // Senders are picked by domain first: the shared domain and every domain the organization uses.
  // A broadcast needs `send` on its sender mailbox.
  const senderMailboxes = (mailboxes.data ?? []).filter(mailbox => mailbox.access.send);
  const senderDomains = [...new Set(senderMailboxes.map(mailbox => domainOf(mailbox.address)))];
  const currentDomain =
    fromDomain ??
    senderMailboxes.find(mailbox => mailbox.id === mailboxId)?.address.split('@')[1] ??
    senderDomains[0] ??
    null;
  const ready =
    senderMailboxes.length > 0 &&
    (templates.data?.length ?? 0) > 0 &&
    (lists.data?.length ?? 0) > 0;
  const loading = mailboxes.isPending || templates.isPending || lists.isPending;
  const pending = save.isPending || send.isPending || remove.isPending;
  const activeTemplate = templateId !== null ? (selectedTemplate.data ?? null) : null;

  const change =
    <T,>(setter: (value: T) => void) =>
    (value: T) => {
      setErrorKey(null);
      setDirty(true);
      setter(value);
    };

  const requestBack = () => {
    if (dirty) {
      setConfirm('discard');
      return;
    }
    onBack();
  };

  // Only values for variables the chosen template uses, and only filled ones, are saved.
  const templateNames = new Set(
    activeTemplate?.variables ??
      templates.data?.find(template => template.id === templateId)?.variables ??
      []
  );
  const sharedValues = Object.fromEntries(
    Object.entries(variables).filter(
      ([variableName, value]) => templateNames.has(variableName) && value !== ''
    )
  );

  const hasValidDraftFields = (): boolean =>
    name.trim() !== '' && mailboxId !== null && templateId !== null && listId !== null;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!hasValidDraftFields() || mailboxId === null || templateId === null || listId === null) {
      setErrorKey('errors.broadcastInvalid');
      return;
    }
    try {
      const stored = await save.mutateAsync({
        input: {
          name,
          mailbox_id: mailboxId,
          template_id: templateId,
          list_id: listId,
          unsubscribe_label: label,
          test_percent: testPercent,
          test_wait_minutes: testWait,
          variables: sharedValues,
        },
        ...(savedId === null ? {} : { broadcastId: savedId }),
      });
      toast.success(t(savedId === null ? 'broadcasts.created' : 'broadcasts.saved'));
      setSavedId(stored.id);
      setDirty(false);
      onSaved(stored.id);
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
    }
  };

  const requestSend = () => {
    setErrorKey(null);
    if (!hasValidDraftFields()) {
      setErrorKey('errors.broadcastInvalid');
      return;
    }
    setConfirm('send');
  };

  const requestSchedule = () => {
    setErrorKey(null);
    if (!hasValidDraftFields()) {
      setErrorKey('errors.broadcastInvalid');
      return;
    }
    const problem = scheduleProblem(scheduleAt);
    if (problem) {
      setErrorKey(problem);
      return;
    }
    setConfirm('schedule');
  };

  const start = async (scheduled: boolean) => {
    if (scheduled) {
      const problem = scheduleProblem(scheduleAt);
      if (problem) {
        setConfirm(null);
        setErrorKey(problem);
        return;
      }
    }
    let sending = false;
    try {
      let targetId = savedId;
      if (targetId === null || dirty) {
        if (
          !hasValidDraftFields() ||
          mailboxId === null ||
          templateId === null ||
          listId === null
        ) {
          setConfirm(null);
          setErrorKey('errors.broadcastInvalid');
          return;
        }
        const stored = await save.mutateAsync({
          input: {
            name,
            mailbox_id: mailboxId,
            template_id: templateId,
            list_id: listId,
            unsubscribe_label: label,
            test_percent: testPercent,
            test_wait_minutes: testWait,
            variables: sharedValues,
          },
          ...(targetId === null ? {} : { broadcastId: targetId }),
        });
        targetId = stored.id;
        setSavedId(stored.id);
        setDirty(false);
      }
      sending = true;
      await send.mutateAsync({
        broadcastId: targetId,
        input: scheduled ? { scheduled_at: new Date(scheduleAt).toISOString() } : {},
      });
      toast.success(t(scheduled ? 'broadcasts.scheduled' : 'broadcasts.started'));
      setConfirm(null);
      onSaved(targetId);
    } catch (failure) {
      setConfirm(null);
      const key = resolveMailboxErrorKey(failure);
      // A scheduled send's only own field is scheduled_at, so BROADCAST_INVALID there names the
      // date (for example when the viewer's clock is behind), not the draft.
      setErrorKey(
        scheduled && sending && key === 'errors.broadcastInvalid'
          ? 'broadcasts.scheduleTooFar'
          : key
      );
    }
  };

  const confirmDelete = async () => {
    if (savedId === null) return;
    try {
      await remove.mutateAsync(savedId);
      toast.success(t('broadcasts.deleted'));
      setConfirm(null);
      onBack();
    } catch (failure) {
      setConfirm(null);
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const selectDomain = (next: string) => {
    change(setFromDomain)(next);
    // Keep the sender only when it is on the chosen domain.
    const current = senderMailboxes.find(mailbox => mailbox.id === mailboxId);
    if (!current || domainOf(current.address) !== next) setMailboxId(null);
  };

  return {
    name,
    setName: change(setName),
    mailboxId,
    setMailboxId: change(setMailboxId),
    templateId,
    setTemplateId: change(setTemplateId),
    listId,
    setListId: change(setListId),
    label,
    setLabel: change(setLabel),
    variables,
    setVariable: (variableName: string, value: string) =>
      change(setVariables)({ ...variables, [variableName]: value }),
    testPercent,
    setTestPercent: change(setTestPercent),
    testWait,
    setTestWait: change(setTestWait),
    selectDomain,
    savedId,
    dirty,
    errorKey,
    confirm,
    setConfirm,
    scheduleAt,
    setScheduleAt: (value: string) => {
      setErrorKey(null);
      setScheduleAt(value);
    },
    zone,
    senderMailboxes,
    senderDomains,
    currentDomain,
    templates: templates.data ?? [],
    lists: lists.data ?? [],
    activeTemplate,
    ready,
    loading,
    pending,
    saving: save.isPending,
    sending: send.isPending,
    removing: remove.isPending,
    requestBack,
    submit,
    requestSend,
    requestSchedule,
    start,
    confirmDelete,
  };
}
