'use client';

import { useState } from 'react';
import { Check, Code2, Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { Toggle } from '@/components/ui/toggle';
import { DeveloperGrantDialog } from '@/features/organization/components/developer-grant-dialog';
import { useUpdateOrganizationMemberFeatures } from '@/features/organization/hooks/use-organizations';
import type {
  MemberFeature,
  OrganizationContext,
  OrganizationMember,
  UpdateOrganizationMemberFeaturesInput,
} from '@/features/organization/types';
import {
  MEMBER_FEATURES,
  developerScopeLabel,
  effectiveMemberFeatures,
  hasFixedMemberFeatures,
} from '@/features/organization/utils/member-features';
import { resolveOrganizationErrorKey } from '@/features/organization/utils/organization-error';
import { cn } from '@/utils';
import { useT } from '@/i18n';

type FeatureMutation = ReturnType<typeof useUpdateOrganizationMemberFeatures>;

/**
 * The features one member may use. Owners and admins see a switch per feature for a member and
 * each press saves at once; everyone else, and every owner or admin row, sees a read-only summary.
 * Developer access carries a sending scope, so its switch opens a dialog instead, and while it is
 * granted the API keys switch stays on: developer access includes it.
 */
export function MemberFeatureToggles({
  context,
  member,
  mutation,
  className,
}: {
  context: OrganizationContext;
  member: OrganizationMember;
  mutation: FeatureMutation;
  className?: string;
}) {
  const t = useT('organization');
  const name = member.nickname || member.username;
  const [developerDialogOpen, setDeveloperDialogOpen] = useState(false);

  if (hasFixedMemberFeatures(member.role)) {
    return (
      <span
        className={cn('text-xs text-muted-foreground', className)}
        title={t('featuresAllForManagersHint')}
      >
        {t('featuresAllForManagers')}
      </span>
    );
  }

  const features = effectiveMemberFeatures(member.role, member.features);
  const editable =
    (context.role === 'owner' || context.role === 'admin') && member.features !== undefined;

  const developerGranted = features.developer;
  const scopeLabel = developerScopeLabel(member.developer_scope, t('developerGrant.allDomainsShort'));

  const save = (input: UpdateOrganizationMemberFeaturesInput, onDone?: () => void) => {
    const feature = (Object.keys(input) as MemberFeature[]).find(key =>
      MEMBER_FEATURES.includes(key)
    );
    const granted = feature ? input[feature] === true : false;
    const label = feature ? t(`features.${feature}`) : '';
    mutation.mutate(
      { memberId: member.id, input },
      {
        onSuccess: () => {
          onDone?.();
          toast.success(
            t(granted ? 'featureGranted' : 'featureRevoked', { feature: label, member: name })
          );
        },
        onError: error => toast.error(t(resolveOrganizationErrorKey(error))),
      }
    );
  };

  const toggle = (feature: MemberFeature, granted: boolean) => {
    if (feature === 'developer') {
      setDeveloperDialogOpen(true);
      return;
    }
    save({ [feature]: granted });
  };

  const chipLabel = (feature: MemberFeature) => {
    const label = t(`features.${feature}`);
    return feature === 'developer' && developerGranted && scopeLabel
      ? `${label} · ${scopeLabel}`
      : label;
  };

  return (
    <div className={cn('flex flex-wrap gap-1.5', className)}>
      {MEMBER_FEATURES.map(feature => {
        const granted = features[feature];
        const saving =
          mutation.isPending &&
          mutation.variables?.memberId === member.id &&
          mutation.variables.input[feature] !== undefined;
        const label = t(`features.${feature}`);
        if (!editable) {
          return granted ? (
            <span
              key={feature}
              className="inline-flex h-7 items-center gap-1 rounded-md border border-primary/30 bg-primary/10 px-2 text-xs font-medium text-primary"
            >
              {feature === 'developer' && <Code2 className="size-3" aria-hidden="true" />}
              {chipLabel(feature)}
            </span>
          ) : null;
        }
        // Developer access includes API keys, so that switch stays on until access is withdrawn.
        const included = feature === 'api_keys' && developerGranted;
        return (
          <Toggle
            key={feature}
            size="sm"
            variant="outline"
            pressed={granted}
            disabled={saving || included}
            title={included ? t('developerGrant.includesApiKeys') : undefined}
            aria-label={t('featureToggleLabel', { feature: label, member: name })}
            onPressedChange={pressed => toggle(feature, pressed)}
            className={cn(
              'h-7 gap-1 rounded-md px-2 text-xs text-muted-foreground shadow-none',
              'data-[state=on]:border-primary/40 data-[state=on]:bg-primary/10 data-[state=on]:text-primary',
              'data-[state=on]:hover:bg-primary/15 disabled:opacity-70'
            )}
          >
            {saving ? (
              <Loader2 className="size-3 animate-spin" aria-hidden="true" />
            ) : granted ? (
              <Check className="size-3" aria-hidden="true" />
            ) : (
              <span className="size-3 rounded-full border border-current opacity-60" aria-hidden="true" />
            )}
            {chipLabel(feature)}
          </Toggle>
        );
      })}
      {editable && developerDialogOpen && (
        // Mounted only while open, so the domain list loads when a manager needs it.
        <DeveloperGrantDialog
          open={developerDialogOpen}
          onOpenChange={setDeveloperDialogOpen}
          organizationId={context.organization_id}
          memberName={name}
          scope={developerGranted ? member.developer_scope : null}
          saving={mutation.isPending && mutation.variables?.memberId === member.id}
          onSave={input => save(input, () => setDeveloperDialogOpen(false))}
        />
      )}
    </div>
  );
}
