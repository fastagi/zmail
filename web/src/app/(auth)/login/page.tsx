import { LoginForm } from '@/features/auth';
import { getAuthRuntimeMode } from '@/features/auth/server/auth-runtime';
import { resolveMockLoginCredentials } from '@/features/auth/server/mock-identity';
import { serverEnv } from '@/config/server-env';

export default function LoginPage() {
  const demoCredentials = resolveMockLoginCredentials({
    authMode: getAuthRuntimeMode(),
  });

  return (
    <LoginForm
      demoCredentials={demoCredentials}
      registrationEnabled={serverEnv.AUTH_REGISTRATION_ENABLED}
    />
  );
}
