import {
  memberWelcomePreviewSchema,
  memberWelcomeSettingSchema,
  type MemberWelcomePreview,
  type MemberWelcomePreviewInput,
  type MemberWelcomeSetting,
  type UpdateMemberWelcomeSettingInput,
} from '@/features/mailbox/member-welcome-schemas';
import { ClientErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';

/**
 * Browser service for the member welcome mail in `contracts/MAILBOXES.md` (section Member Welcome
 * Mail): the organization's switch and copy, and previews. Owners and admins only.
 */
export const memberWelcomeService = {
  async setting(organizationId: number): Promise<MemberWelcomeSetting> {
    return parse(
      memberWelcomeSettingSchema,
      await request.get<unknown>('/member-welcome-mail-setting', {
        headers: organizationHeaders(organizationId),
      })
    );
  },

  async update(
    organizationId: number,
    input: UpdateMemberWelcomeSettingInput
  ): Promise<MemberWelcomeSetting> {
    return parse(
      memberWelcomeSettingSchema,
      await request.put<unknown, UpdateMemberWelcomeSettingInput>(
        '/member-welcome-mail-setting',
        input,
        { headers: organizationHeaders(organizationId) }
      )
    );
  },

  /** Renders the copy with sample values; nothing is saved or sent. */
  async preview(
    organizationId: number,
    input: MemberWelcomePreviewInput
  ): Promise<MemberWelcomePreview> {
    return parse(
      memberWelcomePreviewSchema,
      await request.post<unknown, MemberWelcomePreviewInput>(
        '/member-welcome-mail-previews',
        input,
        { headers: organizationHeaders(organizationId) }
      )
    );
  },
};

function parse<T>(
  schema: { safeParse: (value: unknown) => { success: true; data: T } | { success: false } },
  value: unknown
): T {
  const parsed = schema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return parsed.data;
}

function organizationHeaders(organizationId: number): Record<string, string> {
  if (!Number.isSafeInteger(organizationId) || organizationId < 1) throw invalidResponse();
  return { 'Organization-Id': String(organizationId) };
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Member welcome service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}
