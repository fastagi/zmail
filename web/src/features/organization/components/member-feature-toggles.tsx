'use client';

import { Check, Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { Toggle } from '@/components/ui/toggle';
import { useUpdateOrganizationMemberFeatures } from '@/features/organization/hooks/use-organizations';
import type {
  MemberFeature,
  OrganizationContext,
  OrganizationMember,
} from '@/features/organization/types';
import {
  MEMBER_FEATURES,
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

  const toggle = (feature: MemberFeature, granted: boolean) => {
    const label = t(`features.${feature}`);
    mutation.mutate(
      { memberId: member.id, input: { [feature]: granted } },
      {
        onSuccess: () =>
          toast.success(
            t(granted ? 'featureGranted' : 'featureRevoked', { feature: label, member: name })
          ),
        onError: error => toast.error(t(resolveOrganizationErrorKey(error))),
      }
    );
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
              className="inline-flex h-7 items-center rounded-md border border-primary/30 bg-primary/10 px-2 text-xs font-medium text-primary"
            >
              {label}
            </span>
          ) : null;
        }
        return (
          <Toggle
            key={feature}
            size="sm"
            variant="outline"
            pressed={granted}
            disabled={saving}
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
            {label}
          </Toggle>
        );
      })}
    </div>
  );
}
