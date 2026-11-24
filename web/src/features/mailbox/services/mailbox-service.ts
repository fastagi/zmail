import { mailCampaignService } from './mail-campaign-service';
import { mailDomainService } from './mail-domain-service';
import { mailMessageService } from './mail-message-service';
import { mailboxManagementService } from './mailbox-management-service';

export { parseMailMessage } from './mailbox-service-shared';

/**
 * Browser service for the mailbox routes in `contracts/MAILBOXES.md`. Mailboxes belong to an
 * organization, so every call names it in the `Organization-Id` header.
 */
export const mailboxService = {
  ...mailDomainService,
  ...mailboxManagementService,
  ...mailMessageService,
  ...mailCampaignService,
};
