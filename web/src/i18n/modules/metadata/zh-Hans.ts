// Metadata translations - Simplified Chinese
import type { LocaleMessageShape } from '../../locale-message-shape';
import type { MetadataMessages } from './en-US';

const messages = {
  title: 'Zmail',
  description: '面向团队的自有域名企业邮箱、可视化邮件模板与安全分批群发平台',
} as const satisfies LocaleMessageShape<MetadataMessages>;

export default messages;
