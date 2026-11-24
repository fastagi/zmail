'use client';

import { FileText, Plus } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import { useMailTemplates } from '@/features/mailbox/hooks/use-mail-templates';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export function TemplateList({
  onCreate,
  onOpen,
}: {
  onCreate: () => void;
  onOpen: (templateId: number) => void;
}) {
  const t = useT('mailbox');
  const templates = useMailTemplates(useMailOrganizationId(), 'standard');
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
