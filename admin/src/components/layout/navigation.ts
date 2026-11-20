import {
  BellRing,
  Building2,
  FileSearch,
  ChartColumn,
  Gauge,
  History,
  Settings2,
  SlidersHorizontal,
  Users,
} from 'lucide-react';
import { isFeatureEnabled, isOperatorFeatureEnabled } from '@/config/env';
import type { OperatorCapability } from '@/features/operator-session/types';

interface NavigationEntry {
  icon: typeof Gauge;
  labelKey: string;
  to: string;
  /**
   * The operator capability the destination needs, or a list of which any one is enough; absent
   * for screens every session can open.
   */
  capability?: OperatorCapability | readonly OperatorCapability[];
}

const operatorNavigation = isFeatureEnabled('operator')
  ? ([
      {
        icon: Users,
        labelKey: 'navigation.users',
        to: '/console/users',
        capability: 'operator:manage',
      },
      {
        icon: History,
        labelKey: 'navigation.audit',
        to: '/console/audit',
        capability: 'operator:manage',
      },
      {
        icon: SlidersHorizontal,
        labelKey: 'navigation.settings',
        to: '/console/settings',
        capability: 'operator:manage',
      },
    ] as const)
  : ([] as const);

const starterNavigation = [
  ...(isOperatorFeatureEnabled('mailbox') && isOperatorFeatureEnabled('organization')
    ? ([
        {
          icon: ChartColumn,
          labelKey: 'navigation.operations',
          to: '/console/operations',
          capability: 'mail_metrics:read',
        },
        {
          icon: FileSearch,
          labelKey: 'navigation.contentAccess',
          to: '/console/operations/content-access',
          capability: ['mail_content:read', 'operator:manage'],
        },
      ] as const)
    : []),
  ...(isOperatorFeatureEnabled('organization')
    ? ([
        {
          icon: Building2,
          labelKey: 'navigation.organizations',
          to: '/console/organizations',
          capability: 'mail_metrics:read',
        },
      ] as const)
    : []),
  ...(isOperatorFeatureEnabled('notification')
    ? ([
        {
          icon: BellRing,
          labelKey: 'navigation.notifications',
          to: '/console/notifications',
          capability: 'operator:manage',
        },
      ] as const)
    : []),
];

/** Console destinations for the build's enabled features; the sidebar and command palette share it. */
export const navigationItems = [
  {
    icon: Gauge,
    labelKey: 'navigation.overview',
    to: '/console',
  },
  ...operatorNavigation,
  ...starterNavigation,
  {
    icon: Settings2,
    labelKey: 'navigation.preferences',
    to: '/console/preferences',
  },
] as const satisfies readonly NavigationEntry[];

export type NavigationItem = (typeof navigationItems)[number];

/** Keeps the destinations the session's capabilities cover. */
export function visibleNavigationItems(
  granted: ReadonlySet<OperatorCapability>,
): readonly NavigationItem[] {
  return navigationItems.filter((item) => {
    if (!('capability' in item)) {
      return true;
    }
    const needed: readonly OperatorCapability[] =
      typeof item.capability === 'string' ? [item.capability] : item.capability;
    return needed.some((capability) => granted.has(capability));
  });
}
