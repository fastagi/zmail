// Nav translations - Simplified Chinese
import type { LocaleMessageShape } from '../../locale-message-shape';
import type { NavMessages } from './en-US';

const messages = {
  home: '首页',
  console: '控制台',
  organizations: '组织',
  assets: '资产',
  usage: '用量',
  settings: '设置',
  profile: '个人资料',
  analytics: '数据分析',
  styleguide: '设计规范',
  mailboxes: '邮箱',
  emails: '收发记录',
  mailBroadcasts: '群发',
  mailContacts: '联系人',
  mailTemplates: '模板',
  admin: '管理后台',
  adminDomains: '域名管理',
  adminSharedMailboxes: '公共邮箱',
  adminPlatformAccounts: '平台账号',
  adminAiTriage: 'AI 分流',
  adminExternalConnections: '外部邮箱连接',
  developers: '开发者',
  // fastbuild:slot web.i18n.nav.zh-hans
} as const satisfies LocaleMessageShape<NavMessages>;

export default messages;
