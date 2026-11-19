import type { LocaleMessageShape } from '../../locale-message-shape';
import type { SiteMessages } from './en-US';

const messages = {
  nav: {
    home: '首页',
    console: '控制台',
  },
  footer: {
    tagline: '自有域名团队邮箱与安全群发平台',
    copyright: '© {year} Zmail。保留所有权利。',
  },
  hero: {
    eyebrow: '自有域名团队邮件中心',
    titlePrefix: '用你自己的域名',
    titleAccent: '收发日常邮件与产品群发',
    description:
      'Zmail 将团队共享邮箱、可视化邮件模板、联系人名单管理与带自动退信/投诉熔断保护的分批群发整合在同一个工作区。',
    primaryAction: '进入控制台',
    secondaryAction: '登录账号',
  },
  features: {
    title: '兼顾日常收发与产品动态群发',
    description: '围绕组织域名构建，覆盖团队收件箱协作、模板排版与高送达率群发全流程。',
    authentication: {
      title: '绑定自有邮件域名',
      description: '依托 Cloudflare 自动配置收信路由、发信签名与 DMARC 记录并实时巡检状态。',
    },
    console: {
      title: '团队共享邮箱',
      description: '按成员或业务用途创建发件地址，支持收件箱阅读、附件收发与逐封投递追踪。',
    },
    i18n: {
      title: '可视化模板与动态变量',
      description: '提供积木式可视化编辑与 HTML 模式，支持联系人字段、默认值回退及群发共享变量。',
    },
    ui: {
      title: '测试波次与自动熔断保护',
      description: '支持先向随机比例测试组发送，退信率或垃圾邮件举报超标时自动暂停以保护域名信誉。',
    },
  },
  techStack: {
    title: '同时服务运营团队与后端系统',
    description: '既可在网页控制台完成日常收发与群发，也可通过幂等 HTTP API 从业务服务发信。',
    pills: {
      domains: '多域名 DNS 自动巡检',
      templates: '可视化积木模板',
      staged: '测试组灰度群发',
      unsubscribe: '一键退订与自动屏蔽',
      organizations: '多组织与成员角色',
      api: '幂等发信 HTTP API',
    },
  },
  cta: {
    title: '准备好管理团队邮件了吗？',
    description: '登录组织工作区，即刻绑定域名、创建团队邮箱并发送第一封产品更新。',
    primaryAction: '进入控制台',
    githubAction: '在 GitHub 查看',
  },
} as const satisfies LocaleMessageShape<SiteMessages>;

export default messages;
