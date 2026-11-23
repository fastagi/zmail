import { LoginForm } from '@/features/auth/components/login-form';
import { getAuthRuntimeMode } from '@/features/auth/server/auth-runtime';
import { isGoogleSignInAvailable } from '@/features/auth/server/google-sign-in';
import { resolveMockLoginCredentials } from '@/features/auth/server/mock-identity';
import { serverEnv } from '@/config/server-env';

export default async function LoginPage() {
  const demoCredentials = resolveMockLoginCredentials({
    authMode: getAuthRuntimeMode(),
  });

  return (
    <LoginForm
      demoCredentials={demoCredentials}
      registrationEnabled={serverEnv.AUTH_REGISTRATION_ENABLED}
      googleEnabled={await isGoogleSignInAvailable()}
    />
  );
}
