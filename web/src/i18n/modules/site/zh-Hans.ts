import type { LocaleMessageShape } from '../../locale-message-shape';
import type { SiteMessages } from './en-US';

const messages = {
  nav: {
    home: '产品概览',
    console: '控制台',
  },
  footer: {
    tagline: '自有域名团队邮箱',
    copyright: '© {year} Zmail',
  },
  hero: {
    title: '用自己的域名，和客户通信。',
    description:
      '从产品更新、客户回信，到应用自动发送的收据，每封邮件都用客户熟悉的公司地址。团队可以在同一个工作区处理这些往来。',
    primary: '创建账号',
    secondary: '查看发信 API',
  },
  preview: {
    update: '产品更新',
    releaseSubject: '秋季产品更新',
    releaseBody:
      '现在可以按月查看报表了。在「报表」里选好日期，就能查看团队当月的数据，也可以导出一份分享给同事。',
    address: 'support@atlas.example',
    label: '示例会话',
    inbox: '团队收件箱',
    subject: '回复：秋季产品更新',
    sender: 'Maya Chen',
    question: '新报表很好用。可以导出上个月的数据吗？',
    reply: 'Maya，可以的。打开「报表」，选择上个月，再点击「导出」就好。',
    team: 'Atlas 支持团队',
  },
  sample: {
    title: '用 HTTP 发一封订单收据',
    description:
      '将示例发件地址换成你的邮箱，使用具备 mail:send 权限的密钥，并为每笔订单指定独立的幂等键。',
    action: '进入开发者控制台',
  },
  flows: {
    heading: '团队怎样使用 Zmail',
    inbox: {
      title: '一起处理客户来信',
      description: '把支持邮箱的阅读和发信权限分给对应成员，大家都能接上文，用同一个地址回复客户。',
    },
    broadcast: {
      title: '分批发送产品更新',
      description:
        '用模板写好更新，先发给一小组联系人。退信率或投诉率超过设定阈值时，Zmail 会暂停群发。',
    },
    api: {
      title: '让应用自动发信',
      description: '用限定权限的 API 密钥发送收据。重试同一次请求时复用幂等键，就能避免重复发信。',
    },
  },
} as const satisfies LocaleMessageShape<SiteMessages>;

export default messages;
