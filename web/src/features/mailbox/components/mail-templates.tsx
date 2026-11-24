'use client';

import { useState } from 'react';

import {
  MailOrganizationBoundary,
  MailPageHeader,
} from '@/features/mailbox/components/mail-organization';
import {
  LoadedTemplateEditor,
  TemplateEditor,
} from '@/features/mailbox/components/templates/template-editor';
import { TemplateList } from '@/features/mailbox/components/templates/template-list';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useT } from '@/i18n';

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
    return <TemplateEditor template={null} onBack={back} />;
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
