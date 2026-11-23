'use client';

import { useMemo, useState } from 'react';
import { AlertCircle, Loader2 } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import type {
  DeveloperScope,
  UpdateOrganizationMemberFeaturesInput,
} from '@/features/organization/types';
import { MAX_DEVELOPER_GRANT_DOMAINS } from '@/features/organization/schemas';
import { useT } from '@/i18n';

/**
 * Grants, edits, or withdraws one member's developer access. The scope is every verified domain
 * of the organization or the ones ticked here; the platform's shared domain is never offered.
 */
export function DeveloperGrantDialog({
  open,
  onOpenChange,
  organizationId,
  memberName,
  scope,
  saving,
  onSave,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  organizationId: number;
  memberName: string;
  /** The current scope; null or undefined while the member has no developer access. */
  scope: DeveloperScope | null | undefined;
  saving: boolean;
  onSave: (input: UpdateOrganizationMemberFeaturesInput) => void;
}) {
  const t = useT('organization');
  const common = useT('common');
  const domains = useMailboxDomains(organizationId);
  const verified = useMemo(
    () =>
      (domains.data ?? [])
        .filter(domain => domain.kind === 'organization')
        .map(domain => domain.domain),
    [domains.data]
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('developerGrant.title', { member: memberName })}</DialogTitle>
          <DialogDescription>{t('developerGrant.description')}</DialogDescription>
        </DialogHeader>
        {open && (
          // Remount per opening so the choice starts from the stored scope each time.
          <DeveloperScopeForm
            key={domains.isPending ? 'loading' : 'ready'}
            loading={domains.isPending}
            failed={Boolean(domains.error)}
            verified={verified}
            scope={scope}
            saving={saving}
            onCancel={() => onOpenChange(false)}
            onSave={onSave}
            cancelLabel={common('cancel')}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function DeveloperScopeForm({
  loading,
  failed,
  verified,
  scope,
  saving,
  onCancel,
  onSave,
  cancelLabel,
}: {
  loading: boolean;
  failed: boolean;
  verified: string[];
  scope: DeveloperScope | null | undefined;
  saving: boolean;
  onCancel: () => void;
  onSave: (input: UpdateOrganizationMemberFeaturesInput) => void;
  cancelLabel: string;
}) {
  const t = useT('organization');
  const granted = Boolean(scope);
  const [allDomains, setAllDomains] = useState(scope?.all_domains ?? false);
  // No organization default domain exists, so a new grant starts with the first verified one.
  const [chosen, setChosen] = useState<string[]>(() =>
    scope ? scope.domains : verified.slice(0, 1)
  );
  // A listed domain that is no longer verified stays visible so the manager can drop it.
  const offered = [...new Set([...verified, ...(scope?.domains ?? [])])];
  const ready = allDomains || chosen.length > 0;
  const tooMany = !allDomains && chosen.length > MAX_DEVELOPER_GRANT_DOMAINS;

  const toggleDomain = (domain: string, checked: boolean) =>
    setChosen(current =>
      checked ? [...current, domain] : current.filter(name => name !== domain)
    );

  const save = () =>
    onSave(
      allDomains
        ? { developer: true, developer_all_domains: true }
        : { developer: true, developer_domains: chosen }
    );

  return (
    <>
      <DialogBody className="space-y-4">
        {loading ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
            {t('developerGrant.loadingDomains')}
          </p>
        ) : failed ? (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden="true" />
            <AlertDescription>{t('developerGrant.domainsUnavailable')}</AlertDescription>
          </Alert>
        ) : (
          <>
            <div className="flex items-start gap-2">
              <Checkbox
                id="developer-all-domains"
                checked={allDomains}
                onCheckedChange={checked => setAllDomains(checked === true)}
                disabled={verified.length === 0}
              />
              <div className="grid gap-1">
                <Label htmlFor="developer-all-domains">{t('developerGrant.allDomains')}</Label>
                <p className="text-xs text-muted-foreground">
                  {t('developerGrant.allDomainsHint')}
                </p>
              </div>
            </div>
            {offered.length === 0 ? (
              <Alert>
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{t('developerGrant.noVerifiedDomains')}</AlertDescription>
              </Alert>
            ) : (
              <fieldset className="space-y-2" disabled={allDomains}>
                <legend className="mb-1 text-sm font-medium">
                  {t('developerGrant.domainsLabel')}
                </legend>
                {offered.map(domain => {
                  const id = `developer-domain-${domain}`;
                  return (
                    <div key={domain} className="flex items-center gap-2">
                      <Checkbox
                        id={id}
                        checked={allDomains || chosen.includes(domain)}
                        disabled={allDomains}
                        onCheckedChange={checked => toggleDomain(domain, checked === true)}
                      />
                      <Label htmlFor={id} className="font-normal">
                        {domain}
                      </Label>
                    </div>
                  );
                })}
              </fieldset>
            )}
            {tooMany && (
              <p className="text-xs text-error">
                {t('developerGrant.tooManyDomains', { max: MAX_DEVELOPER_GRANT_DOMAINS })}
              </p>
            )}
          </>
        )}
      </DialogBody>
      <DialogFooter className="gap-2 sm:justify-between">
        {granted ? (
          <Button
            type="button"
            variant="outline"
            className="text-error"
            disabled={saving}
            onClick={() => onSave({ developer: false })}
          >
            {t('developerGrant.revoke')}
          </Button>
        ) : (
          <span />
        )}
        <div className="flex gap-2">
          <Button type="button" variant="ghost" onClick={onCancel} disabled={saving}>
            {cancelLabel}
          </Button>
          <Button
            type="button"
            onClick={save}
            disabled={saving || loading || failed || !ready || tooMany}
          >
            {saving && <Loader2 className="size-4 animate-spin" aria-hidden="true" />}
            {granted ? t('developerGrant.save') : t('developerGrant.grant')}
          </Button>
        </div>
      </DialogFooter>
    </>
  );
}
