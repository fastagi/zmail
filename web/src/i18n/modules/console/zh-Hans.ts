import type { LocaleMessageShape } from '../../locale-message-shape';
import type { ConsoleMessages } from './en-US';

const messages = {
  notifications: '通知',
  profile: '个人资料',
  returnToSite: '返回站点',
  navigation: '控制台导航',
  openNavigation: '打开导航菜单',
  greeting: {
    hello: '你好，{name}',
    late: '夜深了，{name}',
    morning: '早上好，{name}',
    afternoon: '下午好，{name}',
    evening: '晚上好，{name}',
  },
  boundary: {
    errorTitle: '无法加载此控制台页面',
    errorDescription: '你的会话和数据没有变化。请重试，或返回控制台首页。',
    requestId: '请求 ID：{requestId}',
    errorDigest: '错误引用：{digest}',
    notFoundDescription: '你请求的控制台页面不存在或已移动。',
    backToConsole: '返回控制台',
  },
  home: {
    defaultUser: '朋友',
    welcomeDescription: '在这里收发组织邮件、管理联系人和模板，并向联系人群发产品更新。',
    open: '打开',
    quickLinks: {
      emails: {
        title: '收发记录',
        description: '检索全组织所有邮箱的收发记录、投递事件与最近 7 天统计。',
      },
      broadcasts: {
        title: '群发',
        description: '分批群发产品更新，查看进度、退信和退订。',
      },
      contacts: {
        title: '联系人',
        description: '导入联系人并按名单分组管理，退订会自动生效。',
      },
      templates: {
        title: '模板',
        description: '保存常用的邮件内容，群发时直接选用。',
      },
      mailboxes: {
        title: '邮箱',
        description: '创建团队邮箱地址，查看收件箱、回复和发送日常邮件。',
      },
      apiAccess: {
        title: 'API 访问',
        description: '管理用户 API 密钥与个人账号设置。',
      },
    },
    setup: {
      title: '设置团队邮箱',
      titleNoTeam: '为你的团队设置 Zmail',
      description: '从新账号到每位同事都有自己的团队邮箱，每完成一步会自动打勾。',
      progress: '已完成 {done}/{total}',
      state: {
        done: '已完成',
        skipped: '已跳过',
        waiting: '进行中',
      },
      team: {
        title: '创建团队',
        short: '邮箱、成员和域名都归团队所有。',
        created: '团队“{name}”已创建，接着完成下面的步骤。',
      },
      domain: {
        title: '连接公司域名',
        short: '用 @你的公司.com 收发信，由 Zmail 自动配置。',
        hint: '用 @你的公司.com 发信比共享地址更可信。邮件记录由 Zmail 自动配置，你只需在域名注册商处改一次 DNS 服务器，通常几小时内生效。',
        action: '连接域名',
        view: '查看进度',
        skip: '个人使用，先用 @{shared}',
        skipped: '暂时使用 @{shared}。',
        undoSkip: '改为连接域名',
        pendingZone:
          '等待在域名注册商处修改 {zone} 的 DNS 服务器。Zmail 每 10 分钟自动检查一次，生效后自动完成剩余设置。',
        settingUp: '正在为 {domain} 配置邮件记录，大约需要一分钟。',
        pendingRecords: '{domain} 还有 DNS 记录未就绪。',
        verified: '{domain} 已验证。',
      },
      mailbox: {
        title: '创建你的邮箱',
        short: '你自己的地址，或 hello@ 这样的共享地址。',
        hint: '先为自己开一个地址，或开一个 hello@ 这样由几个人共同处理的共享邮箱。',
        action: '创建邮箱',
        actionDomain: '创建 @{domain} 邮箱',
        done: '已有 {count} 个邮箱。',
      },
      invite: {
        title: '邀请同事并为他们开邮箱',
        short: '同事通过邮件加入，然后每人一个邮箱。',
        hint: '每位同事会收到带加入链接的邮件。加入后，为每个人开一个自己的邮箱。',
        action: '邀请同事',
        manage: '查看谁已加入',
        status: '{joined} 人已加入 · {pending} 人未加入',
        statusJoined: '{joined} 人已加入',
        noMailbox: '{count} 位同事还没有邮箱',
        giveMailbox: '为他们开邮箱',
      },
    },
  },
} as const satisfies LocaleMessageShape<ConsoleMessages>;

export default messages;
