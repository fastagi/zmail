import type { LocaleMessageShape } from '../../locale-message-shape';
import type { ConsoleMessages } from './en-US';

const messages = {
  notifications: '通知',
  profile: '个人资料',
  returnToSite: '返回站点',
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
    gettingStartedTitle: '开始使用',
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
      styleguide: {
        title: '设计规范',
        description: '浏览设计系统和组件陈列。',
      },
      i18nTest: {
        title: '国际化测试',
        description: '检查 next-intl 翻译树。',
      },
    },
    developerStepsTitle: '后续步骤',
    developerSteps: {
      apiBefore: '将',
      apiAfter: '指向契约兼容的生产端点或适配器。',
      replaceBefore: '用真实业务工作区替换此页面（',
      replaceAfter: '）。',
      featuresBefore: '在',
      featuresBetween: '中添加新 feature，并通过',
      featuresAfter: '下的新路由公开它们。',
    },
    onboarding: {
      team: {
        title: '创建团队',
        hint: '已完成。',
      },
      mailbox: {
        title: '开第一个邮箱',
        hint: '例如 hello@，团队用它收信和发信。',
      },
      invite: {
        title: '邀请同事',
        hint: '对方会收到带加入链接的邮件，有人接受后这一步打勾。',
      },
      domain: {
        title: '使用自己的域名（可选）',
        hint: '用 @你的公司.com 收发信，不再用共享域名；只需在域名服务商那里改一次 NS。',
      },
      more: '还想做更多？',
      moreLink: '给联系人列表群发一封产品更新',
    },
  },
} as const satisfies LocaleMessageShape<ConsoleMessages>;

export default messages;
