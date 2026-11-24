export {
  MAIL_LABEL_COLORS,
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_PAGE_MAX_SIZE,
  MAIL_SEARCH_MAX_LENGTH,
  mailDirectionSchema,
  mailEventSchema,
  mailEventSummarySchema,
  mailEventTypeSchema,
  mailLabelColorSchema,
  mailSourceSchema,
  mailStatusSchema,
  pageLinksSchema,
  pageMetaSchema,
  safeIdSchema,
  timestampSchema,
} from './schema-modules/shared-schemas';
export * from './schema-modules/domain-schemas';
export * from './schema-modules/mailbox-core-schemas';
export * from './schema-modules/message-inbox-schemas';
export * from './schema-modules/campaign-schemas';
