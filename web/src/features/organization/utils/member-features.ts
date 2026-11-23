import type {
  DeveloperScope,
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
  'mail_clients',
  'developer',
];

const ALL_FEATURES: MemberFeatures = {
  broadcasts: true,
  contacts: true,
  templates: true,
  api_keys: true,
  mail_clients: true,
  developer: true,
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
  // Developer access includes API keys; the API reports it that way too.
  return features.developer ? { ...features, api_keys: true } : features;
}

/** The domains a member's developer access covers, for display; undefined without the grant. */
export function developerScopeLabel(
  scope: DeveloperScope | null | undefined,
  allDomainsLabel: string
): string | undefined {
  if (!scope) return undefined;
  return scope.all_domains ? allDomainsLabel : scope.domains.join(', ');
}

/** Whether the role's grants are fixed: owners and admins always have every feature. */
export function hasFixedMemberFeatures(role: OrganizationRole): boolean {
  return role === 'owner' || role === 'admin';
}
