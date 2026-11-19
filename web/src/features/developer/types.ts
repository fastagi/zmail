/** The sections of the developer console, in tab order. */
export const DEVELOPER_TABS = ['start', 'templates', 'logs', 'limits', 'webhooks'] as const;
export type DeveloperTab = (typeof DEVELOPER_TABS)[number];

/** Which API keys the console shows: live keys deliver, test keys only render and record. */
export const DEVELOPER_MODES = ['live', 'test'] as const;
export type DeveloperMode = (typeof DEVELOPER_MODES)[number];
