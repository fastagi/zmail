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
    welcomeDescription: '在这里收发组织邮件、管理联系人和模版，并向联系人群发产品更新。',
    open: '打开',
    gettingStartedTitle: '开始使用',
    quickLinks: {
      emails: {
        title: '邮件',
        description: '查看收件箱，回复和发送邮件。',
      },
      broadcasts: {
        title: '群发',
        description: '分批群发产品更新，查看进度、退信和退订。',
      },
      contacts: {
        title: '联系人',
        description: '导入联系人并按群组管理，退订会自动生效。',
      },
      templates: {
        title: '模版',
        description: '保存常用的邮件内容，群发时直接选用。',
      },
      mailboxes: {
        title: '邮箱',
        description: '为成员和用途创建发件地址。',
      },
      domain: {
        title: '域名',
        description: '绑定自有域名，Zmail 自动配置收发信记录。',
      },
      apiAccess: {
        title: 'API 访问',
        description: '管理用户 API 密钥和 Starter 偏好设置。',
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
    gettingStarted: {
      domain: '在「域名」绑定自己的域名，或直接使用共享域名。',
      mailbox: '在「邮箱」创建发件地址，例如 hello@你的域名。',
      contacts: '在「联系人」创建群组并导入联系人。',
      template: '在「模版」保存一封产品更新邮件。',
      broadcast: '在「群发」选择域名、邮箱、模版和群组，先发测试批次再全量发送。',
    },
  },
} as const satisfies LocaleMessageShape<ConsoleMessages>;

export default messages;
