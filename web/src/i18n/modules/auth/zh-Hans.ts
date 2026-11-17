// Auth translations - Simplified Chinese
import type { LocaleMessageShape } from '../../locale-message-shape';
import type { AuthMessages } from './en-US';

const messages = {
  // Page titles
  welcomeBack: '欢迎回来',
  signInToContinue: '登录以继续访问您的账户',
  createAccount: '创建账户',
  getStarted: '开始使用平台',
  registrationDisabled: '注册已关闭',
  registrationDisabledMessage: '该平台目前暂不开放注册，请联系管理员获取访问权限。',
  backToSignIn: '返回登录',

  // Form labels
  email: '邮箱',
  password: '密码',
  confirmPassword: '确认密码',
  fullName: '姓名',

  // Form placeholders
  enterEmail: '请输入邮箱地址',
  enterPassword: '请输入密码',
  createPassword: '创建一个强密码',
  confirmYourPassword: '请再次输入密码',
  enterFullName: '请输入您的姓名',

  // Actions
  login: '登录',
  logout: '退出登录',
  register: '注册',
  signIn: '登录',
  signUp: '注册',
  forgotPassword: '忘记密码？',
  resetTitle: '重置密码',
  resetRequestDescription: '输入账号的邮箱，我们会发送一个重置验证码。',
  resetConfirmDescription: '输入邮件中的验证码，并设置新密码。',
  resetDoneDescription: '密码已修改，请用新密码登录。',
  resetSendCode: '发送重置验证码',
  resetCodeSent: '如果 {email} 有账号，重置验证码已发出，30 分钟内有效。',
  resetCode: '重置验证码',
  resetNewPassword: '新密码',
  resetSetPassword: '设置新密码',
  resetSendAgain: '重新发送验证码',
  resetPasswordTooShort: '密码至少 6 个字符。',
  resetPasswordMismatch: '两次输入的密码不一致。',
  resetTokenInvalid: '验证码错误、已过期或已使用，请重新获取。',
  resetRequestFailed: '验证码发送失败，请稍后再试。',
  resetConfirmFailed: '密码修改失败，请稍后再试。',
  demoCredentials: '演示账号',
  demoCredentialsHint: '使用默认账号',

  // Links
  noAccount: '没有账号？',
  inviteOnly: '没有账号？请联系组织管理员邀请你加入。',
  hasAccount: '已有账号？',
  rememberMe: '记住我',

  // Terms
  agreeToTerms: '我同意',
  termsOfService: '服务条款',
  and: '和',
  privacyPolicy: '隐私政策',

  // Password requirements
  passwordRequirements: '密码要求',
  passwordReqLength: '至少8个字符',
  passwordReqCase: '包含大小写字母',
  passwordReqNumber: '包含至少一个数字',
  passwordReqSpecial: '包含至少一个特殊字符',

  // Decorative panel
  heroTitle: '团队的邮件中心',
  heroSubtitle: '用自己的域名收发邮件，向联系人群发产品更新，送达情况一目了然',
  feature1: '绑定多个自有域名',
  feature2: '分批群发与自动暂停保护',
  feature3: '模版与联系人管理',
  feature4: '多组织与成员权限',

  // Validation messages
  nameRequired: '请输入姓名',
  nameInvalid: '请输入有效的姓名',
  nameMinLength: '姓名至少需要2个字符',
  nameTooLong: '姓名过长',
  emailRequired: '请输入邮箱',
  emailInvalid: '请输入有效的邮箱地址',
  passwordRequired: '请输入密码',
  passwordMinLength: '密码至少需要8个字符',
  passwordTooLong: '密码过长',
  passwordTooShort: '密码过短',
  passwordInvalid: '请输入有效的密码',
  confirmPasswordRequired: '请确认密码',
  passwordsDoNotMatch: '两次输入的密码不一致',
  termsRequired: '请同意服务条款和隐私政策',

  // Toast messages
  loginSuccess: '登录成功',
  loginFailed: '登录失败',
  welcomeBackUser: '欢迎回来，{name}',
  logoutSuccess: '退出成功',
  logoutFailed: '退出失败',
  registerSuccess: '注册成功',
  registerFailed: '注册失败',
  accountCreated: '账号创建成功，正在登录...',
  accountDisabled: '该账号已被停用',
  emailAlreadyExists: '该邮箱已注册账号',
  invalidCredentials: '邮箱或密码错误',

  // Social login
  signInWithGoogle: '使用 Google 登录',
  signInWithApple: '使用 Apple 登录',
  signInWithGithub: '使用 GitHub 登录',
  signUpWithGoogle: '使用 Google 注册',
  signUpWithApple: '使用 Apple 注册',
  signUpWithGithub: '使用 GitHub 注册',

  // Footer
  copyright: '© {year} Zmail。保留所有权利。',
} as const satisfies LocaleMessageShape<AuthMessages>;

export default messages;
