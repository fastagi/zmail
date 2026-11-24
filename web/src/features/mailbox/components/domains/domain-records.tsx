'use client';

import { Check } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { CopyButton } from '@/features/mailbox/components/mail-shared';
import type { MailDomain, MailDomainRecord } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const purposeKey = {
  receiving: 'domain.purposeReceiving',
  sending: 'domain.purposeSending',
  dmarc: 'domain.purposeDmarc',
} as const;

const recordStatusKey = {
  ok: 'domain.recordOk',
  missing: 'domain.recordMissing',
  conflict: 'domain.recordConflict',
} as const;

const setupSteps = ['stepConnect', 'stepNameservers', 'stepRecords', 'stepMailbox'] as const;

/** Where a connected domain stands on the way to its first mailbox; `current` indexes `setupSteps`. */
export function DomainSetupSteps({ current }: { current: number }) {
  const t = useT('mailbox');
  return (
    <ol aria-label={t('domain.stepsLabel')} className="flex flex-wrap gap-x-4 gap-y-2 text-xs">
      {setupSteps.map((step, index) => {
        const done = index < current;
        return (
          <li
            key={step}
            aria-current={index === current ? 'step' : undefined}
            data-state={done ? 'done' : index === current ? 'current' : 'upcoming'}
            className={cn(
              'flex items-center gap-1.5',
              index === current ? 'font-medium text-foreground' : 'text-muted-foreground'
            )}
          >
            <span
              aria-hidden="true"
              className={cn(
                'flex size-5 items-center justify-center rounded-full border',
                done && 'border-transparent bg-success-subtle text-success',
                index === current && 'border-foreground'
              )}
            >
              {done ? <Check className="size-3" /> : index + 1}
            </span>
            {t(`domain.${step}`)}
            {done ? <span className="sr-only">{t('domain.setupStepDone')}</span> : null}
          </li>
        );
      })}
    </ol>
  );
}

/** What each kind of record the domain needs is for, in plain words, for the purposes it has. */
export function RecordHelp({ domain }: { domain: MailDomain }) {
  const t = useT('mailbox');
  const purposes = new Set(domain.records.map(record => record.purpose));
  const titleId = `mail-domain-${domain.id}-records-help`;
  const lines = [
    purposes.has('receiving') ? t('domain.recordHelpReceiving', { domain: domain.name }) : null,
    purposes.has('sending') ? t('domain.recordHelpSending') : null,
    purposes.has('dmarc') ? t('domain.recordHelpDmarc') : null,
  ].filter((line): line is string => line !== null);
  return (
    <div className="space-y-1.5 rounded-md bg-muted/40 p-3 text-sm">
      <p id={titleId} className="font-medium">
        {t('domain.recordHelpTitle')}
      </p>
      <ul aria-labelledby={titleId} className="list-disc space-y-1 pl-5 text-muted-foreground">
        {lines.map(line => (
          <li key={line}>{line}</li>
        ))}
      </ul>
      <p className="text-xs text-muted-foreground">{t('domain.recordHelpAuto')}</p>
    </div>
  );
}

export function RecordTable({ records }: { records: MailDomainRecord[] }) {
  const t = useT('mailbox');
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('domain.recordPurpose')}</TableHead>
            <TableHead>{t('domain.recordType')}</TableHead>
            <TableHead>{t('domain.recordName')}</TableHead>
            <TableHead>{t('domain.recordValue')}</TableHead>
            <TableHead>{t('domain.recordStatus')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {records.map((record, index) => (
            <TableRow
              key={`${record.purpose}-${record.type}-${record.name}-${index}`}
              data-record-status={record.status}
            >
              <TableCell className="whitespace-nowrap">{t(purposeKey[record.purpose])}</TableCell>
              <TableCell className="font-mono text-xs">{record.type}</TableCell>
              <TableCell className="max-w-56 min-w-40 font-mono text-xs whitespace-normal [overflow-wrap:anywhere]">
                {record.name}
              </TableCell>
              <TableCell className="max-w-md min-w-48 font-mono text-xs break-all whitespace-normal">
                {record.content === '' ? (
                  <span className="font-sans text-muted-foreground">
                    {t('domain.sendingNotEnabled')}
                  </span>
                ) : (
                  <span className="flex items-start gap-1">
                    <span className="min-w-0 flex-1">
                      {record.priority !== undefined && record.type === 'MX'
                        ? `${record.priority} ${record.content}`
                        : record.content}
                    </span>
                    <CopyButton
                      value={record.content}
                      label={t('domain.copyValue', { name: record.name })}
                      copiedLabel={t('domain.copied')}
                    />
                  </span>
                )}
                {record.found ? (
                  <span className="mt-1 block font-sans text-warning">
                    {t('domain.recordFound', { value: record.found })}
                  </span>
                ) : null}
              </TableCell>
              <TableCell>
                <Badge
                  variant="outline"
                  className={cn(
                    'border-transparent whitespace-nowrap',
                    record.status === 'ok'
                      ? 'bg-success-subtle text-success'
                      : record.status === 'conflict'
                        ? 'bg-error-subtle text-error'
                        : 'bg-warning-subtle text-warning'
                  )}
                >
                  {t(recordStatusKey[record.status])}
                </Badge>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

const setupErrorKeys = {
  'domain.permission_denied': 'permissionDenied',
  'domain.provider_unavailable': 'providerUnavailable',
  'domain.routing_refused': 'routingRefused',
  'domain.sending_refused': 'sendingRefused',
  'domain.dns_record_refused': 'dnsRecordRefused',
  'domain.routing_rule_refused': 'routingRuleRefused',
  'domain.provider_refused': 'providerRefused',
  'domain.zone_not_found': 'zoneNotFound',
  'domain.setup_failed': 'setupFailed',
} as const;

type SetupErrorKey = (typeof setupErrorKeys)[keyof typeof setupErrorKeys];

function setupErrorKey(code: string | undefined): SetupErrorKey {
  return code && code in setupErrorKeys
    ? setupErrorKeys[code as keyof typeof setupErrorKeys]
    : 'setupFailed';
}

/** Says what went wrong in a setup step and what to do, with the provider's words folded away. */
export function DomainSetupError({ code, detail }: { code: string | undefined; detail: string }) {
  const t = useT('mailbox');
  const key = setupErrorKey(code);
  return (
    <Alert
      data-testid="mail-domain-last-error"
      className="border-warning/40 bg-warning-subtle text-warning"
    >
      <AlertDescription className="space-y-1.5 break-words">
        <p className="font-medium">{t(`domain.setupError.${key}.title`)}</p>
        <p>{t(`domain.setupError.${key}.action`)}</p>
        <details className="text-xs">
          <summary className="cursor-pointer select-none">{t('domain.setupErrorDetails')}</summary>
          <p className="mt-1 font-mono">{detail}</p>
        </details>
      </AlertDescription>
    </Alert>
  );
}
