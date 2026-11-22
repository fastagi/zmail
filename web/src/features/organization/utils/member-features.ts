import type {
  MemberFeature,
  MemberFeatures,
  OrganizationRole,
} from '@/features/organization/types';

/** Every member feature in display order. */
export const MEMBER_FEATURES: readonly MemberFeature[] = [
  'broadcasts',
  'contacts',
  'templates',
  'api_keys',
];

const ALL_FEATURES: MemberFeatures = {
  broadcasts: true,
  contacts: true,
  templates: true,
  api_keys: true,
};

/**
 * What a membership may use. Owners and admins always have every feature; so does a view from an
 * API that predates grants, where every member could use everything. The API stays the authority.
 */
export function effectiveMemberFeatures(
  role: OrganizationRole,
  features: MemberFeatures | undefined
): MemberFeatures {
  if (role === 'owner' || role === 'admin' || !features) return ALL_FEATURES;
  return features;
}

/** Whether the role's grants are fixed: owners and admins always have every feature. */
export function hasFixedMemberFeatures(role: OrganizationRole): boolean {
  return role === 'owner' || role === 'admin';
}
