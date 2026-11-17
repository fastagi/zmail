/** Browser-safe auth navigation and route ownership. */
export const authConfig = {
  routes: {
    login: '/login',
    register: '/register',
    forgotPassword: '/forgot-password',
    afterLogin: '/console',
    afterLogout: '/login',
  },

  protectedRoutes: ['/console', '/styleguide', '/i18n-test'] as const,
  publicOnlyRoutes: ['/login', '/register', '/forgot-password'] as const,
} as const;
