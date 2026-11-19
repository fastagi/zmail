'use client';

import Link from 'next/link';
import { RefreshCw } from 'lucide-react';

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
import { getMailZoneDnsRoute } from '@/constants/routes';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { CopyButton } from '@/features/mailbox/components/mail-shared';
import { mailOrganizationHref } from '@/features/mailbox/hooks/use-mail-organization';
import { useListDnsRecords } from '@/features/mailbox/hooks/use-mailbox';
import type { MailZone } from '@/features/mailbox/types';
import { useT } from '@/i18n';

/** At most this many imported records are listed here; the DNS page has them all. */
const IMPORTED_PREVIEW_LIMIT = 10;

type Registrar = 'Alibaba' | 'Tencent' | 'Huawei' | 'Godaddy' | 'Namecheap';

/** Each registrar with the name-server hosts it hands out, to open its guide first. */
const registrars: { key: Registrar; hosts: string[] }[] = [
  { key: 'Alibaba', hosts: ['hichina.com', 'alidns.com'] },
  { key: 'Tencent', hosts: ['dnspod.net'] },
  { key: 'Huawei', hosts: ['hwclouddns'] },
  { key: 'Godaddy', hosts: ['domaincontrol.com'] },
  { key: 'Namecheap', hosts: ['registrar-servers.com'] },
];

/** The registrar whose name servers the apex had before Zmail, when one is recognized. */
export function matchingRegistrar(originalNameServers: string[]): Registrar | null {
  const servers = originalNameServers.map(server => server.toLowerCase());
  const match = registrars.find(registrar =>
    servers.some(server => registrar.hosts.some(host => server.includes(host)))
  );
  return match?.key ?? null;
}

/**
 * What a pending managed zone needs from the organization: set the two Cloudflare name servers at
 * the registrar, with a guide per registrar, and check the records Cloudflare imported first.
 * "Check now" asks Cloudflare to look for the delegation again.
 */
export function MailZoneNameservers({
  zone,
  canManage,
  checking,
  checkDisabled,
  limited,
  onCheck,
}: {
  zone: MailZone;
  canManage: boolean;
  checking: boolean;
  checkDisabled: boolean;
  limited: boolean;
  onCheck: () => void;
}) {
  const t = useT('mailbox');
  const name = { zone: zone.name };
  const open = matchingRegistrar(zone.original_name_servers);

  return (
    <div className="space-y-4 rounded-md border bg-muted/30 p-3">
      <div className="space-y-1">
        <h4 className="text-sm font-semibold">{t('domain.nameserversTitle', name)}</h4>
        <p className="text-sm text-muted-foreground">{t('domain.nameserversDescription', name)}</p>
      </div>
      <ul aria-label={t('domain.nameserversLabel')} className="space-y-1">
        {zone.name_servers.map(server => (
          <li
            key={server}
            className="flex w-fit max-w-full items-center gap-1 rounded-md border bg-background px-2 py-1 font-mono text-sm"
          >
            <span className="min-w-0 break-all">{server}</span>
            <CopyButton
              value={server}
              label={t('domain.copyNameServer', { server })}
              copiedLabel={t('domain.copied')}
            />
          </li>
        ))}
      </ul>
      {zone.original_name_servers.length > 0 ? (
        <p className="text-xs break-words text-muted-foreground">
          {t('domain.originalNameServers', { servers: zone.original_name_servers.join(', ') })}
        </p>
      ) : null}

      {canManage ? (
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="button"
            size="sm"
            loading={checking}
            disabled={checkDisabled}
            icon={<RefreshCw className="size-4" />}
            onClick={onCheck}
          >
            {t('domain.checkNow')}
          </Button>
          {limited ? (
            <p role="status" className="text-sm text-warning">
              {t('domain.checkLimited')}
            </p>
          ) : null}
        </div>
      ) : null}

      <div className="space-y-2">
        <h5 className="text-sm font-medium">{t('domain.registrarsTitle')}</h5>
        <div className="divide-y rounded-md border bg-background">
          {registrars.map(registrar => (
            <details key={registrar.key} open={registrar.key === open} className="group px-3 py-2">
              <summary className="cursor-pointer text-sm font-medium focus-ring">
                {t(`domain.registrar${registrar.key}`)}
              </summary>
              <ol className="mt-2 list-decimal space-y-1 pl-5 text-sm text-muted-foreground">
                <li>{t(`domain.registrar${registrar.key}Step1`)}</li>
                <li>{t(`domain.registrar${registrar.key}Step2`, name)}</li>
                <li>{t(`domain.registrar${registrar.key}Step3`)}</li>
              </ol>
            </details>
          ))}
        </div>
      </div>

      <ImportedRecords zone={zone} />
    </div>
  );
}

function ImportedRecords({ zone }: { zone: MailZone }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const records = useListDnsRecords(organizationId, zone.id);
  const titleId = `mail-zone-${zone.id}-imported`;
  const shown = records.data?.slice(0, IMPORTED_PREVIEW_LIMIT) ?? [];
  const more = (records.data?.length ?? 0) - shown.length;

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h5 id={titleId} className="text-sm font-medium">
          {t('domain.importedTitle')}
        </h5>
        <Button asChild size="sm" variant="outline">
          <Link href={mailOrganizationHref(getMailZoneDnsRoute(zone.id), organizationId)}>
            {t('domain.reviewRecords')}
          </Link>
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">{t('domain.importedDescription')}</p>
      {records.isPending ? (
        <Skeleton className="h-16 w-full" aria-hidden="true" />
      ) : records.error || !records.data ? (
        <p className="text-sm text-muted-foreground">{t('domain.importedFailed')}</p>
      ) : records.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('domain.importedEmpty')}</p>
      ) : (
        <>
          <Table aria-labelledby={titleId} className="bg-background">
            <TableHeader>
              <TableRow>
                <TableHead>{t('dns.type')}</TableHead>
                <TableHead>{t('dns.name')}</TableHead>
                <TableHead>{t('dns.content')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map(record => (
                <TableRow key={record.id}>
                  <TableCell className="font-mono text-xs">{record.type}</TableCell>
                  <TableCell className="max-w-56 font-mono text-xs break-all whitespace-normal">
                    {record.name}
                  </TableCell>
                  <TableCell className="max-w-md font-mono text-xs break-all whitespace-normal">
                    {record.priority !== undefined && record.type === 'MX'
                      ? `${record.priority} ${record.content}`
                      : record.content}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {more > 0 ? (
            <p className="text-xs text-muted-foreground">
              {t('domain.importedMore', { count: more })}
            </p>
          ) : null}
        </>
      )}
    </div>
  );
}
