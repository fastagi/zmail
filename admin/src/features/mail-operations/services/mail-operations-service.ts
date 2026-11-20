import type { z } from 'zod';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractResponse,
} from '@/http/contract';
import { http } from '@/http/client';
import {
  broadcastContentSchema,
  contentAccessPageSchema,
  mailMetricsSchema,
  mailOrganizationPageSchema,
  outboundMessagePageSchema,
  nextUtcDayStart,
  type BroadcastContent,
  type ContentAccessPage,
  type ContentAccessSearch,
  type MailMetrics,
  type MailOrganizationPage,
  type MailOrganizationSearch,
  type OutboundMessagePage,
  type OutboundMessageSearch,
} from '@/features/mail-operations/types';

/** Rows per page, as the spec asks: twenty organizations or messages. */
export const mailOperationsPageSize = 20;

/** Days in the overview trend. */
export const mailTrendDays = 7;

function withSignal(signal?: AbortSignal) {
  return signal ? { signal } : {};
}

export const mailOperationsService = {
  overview(date: string | undefined, signal?: AbortSignal): Promise<MailMetrics> {
    const params = new URLSearchParams({ days: String(mailTrendDays) });
    if (date) {
      params.set('date', date);
    }
    return http.get(`/v1/operator/mail-metrics?${params.toString()}`, {
      schema: mailMetricsSchema,
      ...withSignal(signal),
    });
  },
  organizations(
    search: MailOrganizationSearch,
    signal?: AbortSignal,
  ): Promise<MailOrganizationPage> {
    const params = new URLSearchParams({
      page: String(search.page ?? 1),
      per_page: String(mailOperationsPageSize),
    });
    if (search.date) {
      params.set('date', search.date);
    }
    if (search.q) {
      params.set('q', search.q);
    }
    return http.get(`/v1/operator/mail-metrics/organizations?${params.toString()}`, {
      responseMode: 'json',
      schema: mailOrganizationPageSchema,
      ...withSignal(signal),
    });
  },
  outbound(search: OutboundMessageSearch, signal?: AbortSignal): Promise<OutboundMessagePage> {
    const params = new URLSearchParams({
      page: String(search.page ?? 1),
      per_page: String(mailOperationsPageSize),
    });
    if (search.date) {
      params.set('date', search.date);
    }
    if (search.organization_id) {
      params.set('organization_id', String(search.organization_id));
    }
    if (search.mailbox_id) {
      params.set('mailbox_id', String(search.mailbox_id));
    }
    if (search.status) {
      params.set('status', search.status);
    }
    return http.get(`/v1/operator/outbound-messages?${params.toString()}`, {
      responseMode: 'json',
      schema: outboundMessagePageSchema,
      ...withSignal(signal),
    });
  },
  /** Records the access with the reason, then returns the sent text. Never cached. */
  readContent(organizationId: number, messageId: number, reason: string): Promise<BroadcastContent> {
    return http.post(
      `/v1/operator/outbound-messages/${organizationId}/${messageId}/content`,
      { reason },
      { schema: broadcastContentSchema },
    );
  },
  contentAccess(search: ContentAccessSearch, signal?: AbortSignal): Promise<ContentAccessPage> {
    const params = new URLSearchParams({
      page: String(search.page ?? 1),
      per_page: String(mailOperationsPageSize),
    });
    if (search.organization_id) {
      params.set('organization_id', String(search.organization_id));
    }
    if (search.viewer_user_id) {
      params.set('viewer_user_id', String(search.viewer_user_id));
    }
    if (search.from) {
      params.set('from', `${search.from}T00:00:00Z`);
    }
    // The API rejects a range that ends before it starts; such an end is ignored.
    if (search.to && !(search.from && search.to < search.from)) {
      params.set('to', nextUtcDayStart(search.to));
    }
    return http.get(`/v1/operator/content-access-audits?${params.toString()}`, {
      responseMode: 'json',
      schema: contentAccessPageSchema,
      ...withSignal(signal),
    });
  },
};

// Fails type-check when contracts/openapi.yaml allows a body these schemas would reject.
assertContract<
  Accepts<z.input<typeof mailMetricsSchema>, ContractData<'getOperatorMailMetrics', 200>>
>();
assertContract<
  Accepts<
    z.input<typeof mailOrganizationPageSchema>,
    ContractResponse<'listOperatorMailMetricsOrganizations', 200>
  >
>();
assertContract<
  Accepts<
    z.input<typeof outboundMessagePageSchema>,
    ContractResponse<'listOperatorOutboundMessages', 200>
  >
>();
assertContract<
  Accepts<z.input<typeof broadcastContentSchema>, ContractData<'readOperatorBroadcastContent', 200>>
>();
assertContract<
  Accepts<
    z.input<typeof contentAccessPageSchema>,
    ContractResponse<'listOperatorContentAccessAudits', 200>
  >
>();
