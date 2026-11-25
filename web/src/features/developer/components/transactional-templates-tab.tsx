'use client';

import { useMemo, useState } from 'react';
import { FileText, Pencil, Plus } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { TransactionalTemplateDialog } from '@/features/developer/components/transactional-template-dialog';
import { useDeveloperTab } from '@/features/developer/hooks/use-developer-tab';
import {
  canWriteTransactional,
  offersSenderDomain,
} from '@/features/developer/utils/developer-scope';
import { useTransactionalLog } from '@/features/developer/hooks/use-developer';
import { OrganizationLostState } from '@/features/mailbox/components/mail-organization';
import { EmptyState, ErrorAlert, MailHtmlFrame } from '@/features/mailbox/components/mail-shared';
import type { MailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailTemplate, useMailTemplates } from '@/features/mailbox/hooks/use-mail-templates';
import type { MailTemplate, MailTemplateSummary } from '@/features/mailbox/types';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { renderTemplate } from '@/features/mailbox/utils/template-render';
import { useT } from '@/i18n';
import { cn } from '@/utils';

type Editing = { kind: 'closed' } | { kind: 'new' } | { kind: 'edit'; template: MailTemplate };

function hasSecrets(template: Pick<MailTemplateSummary, 'variable_definitions'>): boolean {
  return template.variable_definitions.some(definition => definition.secret);
}

/** `{{name}}` as the template writes it; it is template syntax, not interface copy. */
function placeholder(name: string): string {
  return `{{${name}}}`;
}

/**
 * Transactional templates: the list on the left and the selected template on the right, with its
 * variables and a preview filled with the saved sample values. Owners and admins create and edit;
 * members read.
 */
export function TransactionalTemplatesTab({ organization }: { organization: MailOrganization }) {
  const t = useT('developer');
  const tm = useT('mailbox');
  const templates = useMailTemplates(organization.id, 'transactional');
  const [chosenId, setChosenId] = useState<number | null>(null);
  const [editing, setEditing] = useState<Editing>({ kind: 'closed' });
  const items = templates.data ?? [];
  const selected = items.find(item => item.id === chosenId) ?? items[0] ?? null;
  const detail = useMailTemplate(organization.id, selected?.id ?? null);
  const { mode } = useDeveloperTab();
  // Only the selected template's count is read, so the list never costs one request per template.
  const selectedLog = useTransactionalLog(
    organization.id,
    { mode, ...(selected?.key ? { template: selected.key } : {}) },
    { perPage: 1, enabled: Boolean(selected?.key) }
  );
  const sent24h = selectedLog.data?.pages[0]?.meta.total_24h;

  const writable = canWriteTransactional(organization);
  const create = writable ? (
    <Button
      type="button"
      icon={<Plus className="size-4" />}
      onClick={() => setEditing({ kind: 'new' })}
    >
      {t('templates.new')}
    </Button>
  ) : null;

  const dialog =
    editing.kind === 'closed' ? null : (
      <TransactionalTemplateDialog
        key={editing.kind === 'edit' ? `edit-${editing.template.id}` : `new-${organization.id}`}
        organizationId={organization.id}
        template={editing.kind === 'edit' ? editing.template : null}
        canManage={organization.canManage}
        offersDomain={domain => offersSenderDomain(organization, domain)}
        onClose={() => setEditing({ kind: 'closed' })}
        onSaved={template => setChosenId(template.id)}
      />
    );

  if (templates.isPending) {
    return (
      <div className="grid gap-4 lg:grid-cols-[18rem_1fr]" aria-hidden="true">
        <Skeleton className="h-64 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }
  if (templates.error) {
    if (isOrganizationLost(templates.error)) return <OrganizationLostState />;
    return (
      <ErrorAlert
        message={tm(resolveMailboxErrorKey(templates.error))}
        retryLabel={tm('actions.retry')}
        onRetry={() => void templates.refetch()}
      />
    );
  }
  if (items.length === 0) {
    return (
      <>
        <EmptyState
          icon={<FileText className="size-5" aria-hidden="true" />}
          title={t('templates.emptyTitle')}
          description={
            writable ? t('templates.emptyDescription') : t('templates.readOnly')
          }
          action={create}
        />
        {dialog}
      </>
    );
  }

  return (
    <div className="grid gap-4 lg:grid-cols-[18rem_1fr]">
      <div className="space-y-2 lg:border-r lg:pr-4">
        <ul aria-label={t('templates.list')} className="space-y-1">
          {items.map(item => {
            const active = item.id === selected?.id;
            return (
              <li key={item.id}>
                <button
                  type="button"
                  aria-current={active ? 'true' : undefined}
                  onClick={() => setChosenId(item.id)}
                  className={cn(
                    'w-full rounded-md px-3 py-2.5 text-left transition-colors hover:bg-muted focus-ring',
                    active && 'bg-muted'
                  )}
                >
                  <span className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate text-sm font-medium">{item.name}</span>
                    {item.outside_sending_scope ? (
                      <Badge variant="outline" className="text-muted-foreground">
                        {t('templates.outsideScopeBadge')}
                      </Badge>
                    ) : null}
                    {hasSecrets(item) ? (
                      <Badge className="bg-warning-subtle text-warning" variant="secondary">
                        {t('templates.containsSecrets')}
                      </Badge>
                    ) : null}
                  </span>
                  <span className="mt-0.5 block truncate font-mono text-xs text-muted-foreground">
                    {item.key}
                  </span>
                  {active && sent24h !== undefined ? (
                    <span className="mt-0.5 block text-xs text-muted-foreground">
                      {t('templates.sent24h', { count: sent24h })}
                    </span>
                  ) : null}
                </button>
              </li>
            );
          })}
        </ul>
        {create ? <div className="px-1">{create}</div> : null}
        {writable ? null : (
          <p className="px-1 text-xs text-muted-foreground">{t('templates.readOnly')}</p>
        )}
      </div>
      <div className="min-w-0">
        {detail.isPending ? (
          <Skeleton className="h-72 w-full" aria-hidden="true" />
        ) : detail.error || !detail.data ? (
          <ErrorAlert
            message={tm(resolveMailboxErrorKey(detail.error))}
            retryLabel={tm('actions.retry')}
            onRetry={() => void detail.refetch()}
          />
        ) : (
          <TemplateDetail
            template={detail.data}
            canManage={writable && !detail.data.outside_sending_scope}
            onEdit={() => setEditing({ kind: 'edit', template: detail.data })}
          />
        )}
      </div>
      {dialog}
    </div>
  );
}

function TemplateDetail({
  template,
  canManage,
  onEdit,
}: {
  template: MailTemplate;
  canManage: boolean;
  onEdit: () => void;
}) {
  const t = useT('developer');
  const rendered = useMemo(
    () =>
      renderTemplate(
        { subject: template.subject, html: template.html_body, text: template.text_body },
        template.sample_variables
      ),
    [template]
  );
  const definitions = template.variable_definitions;
  const sender = template.from
    ? template.from.name === ''
      ? template.from.address
      : `${template.from.name} <${template.from.address}>`
    : '';

  return (
    <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
      <div className="min-w-0 space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="truncate text-lg font-semibold">{template.name}</h2>
            <p className="truncate font-mono text-xs text-muted-foreground">{template.key}</p>
          </div>
          {template.outside_sending_scope ? (
            <p className="max-w-xs text-xs text-muted-foreground">
              {t('templates.outsideScope')}
            </p>
          ) : canManage ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Pencil className="size-4" />}
              onClick={onEdit}
            >
              {t('templates.edit')}
            </Button>
          ) : null}
        </div>
        <section className="space-y-1">
          <h3 className="text-xs text-muted-foreground">{t('templates.subject')}</h3>
          <p className="rounded-md border px-3 py-2 text-sm break-words">{template.subject}</p>
        </section>
        <section className="space-y-1">
          <h3 className="text-xs text-muted-foreground">{t('templates.body')}</h3>
          {template.text_body.trim() === '' ? (
            <p className="text-sm text-muted-foreground">{t('templates.noText')}</p>
          ) : (
            <p className="rounded-md border px-3 py-3 text-sm leading-relaxed break-words whitespace-pre-line">
              {template.text_body}
            </p>
          )}
        </section>
        <section className="space-y-1.5">
          <h3 className="text-xs text-muted-foreground">{t('templates.variables')}</h3>
          {definitions.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t('templates.noVariables')}</p>
          ) : (
            <ul className="flex flex-wrap gap-1.5">
              {definitions.map(definition => (
                <li
                  key={definition.name}
                  className={cn(
                    'inline-flex h-6 items-center rounded-full px-2.5 font-mono text-xs',
                    definition.secret
                      ? 'bg-warning-subtle text-warning'
                      : 'bg-primary/10 text-primary'
                  )}
                >
                  {definition.secret
                    ? `${placeholder(definition.name)} ${t('templates.secretSuffix')}`
                    : placeholder(definition.name)}
                </li>
              ))}
            </ul>
          )}
        </section>
        <p className="rounded-md bg-warning-subtle px-3 py-2.5 text-xs leading-relaxed text-warning">
          {t('templates.sampleNote')}
        </p>
      </div>
      <section className="min-w-0 space-y-1.5">
        <h3 className="text-xs text-muted-foreground">{t('templates.preview')}</h3>
        <div className="overflow-hidden rounded-md border">
          {sender ? (
            <p className="truncate border-b px-3 py-2 text-xs text-muted-foreground">
              {t('templates.from', { sender })}
            </p>
          ) : null}
          {rendered ? (
            <div className="space-y-2 p-3">
              <p className="text-sm font-medium break-words">{rendered.subject}</p>
              {rendered.html.trim() !== '' ? (
                <MailHtmlFrame html={rendered.html} title={t('templates.previewTitle')} />
              ) : (
                <p className="text-sm whitespace-pre-line">{rendered.text}</p>
              )}
            </div>
          ) : (
            <p className="p-3 text-sm text-muted-foreground">{t('templates.previewUnavailable')}</p>
          )}
        </div>
      </section>
    </div>
  );
}
