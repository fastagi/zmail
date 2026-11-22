import { redirect } from 'next/navigation';

import { authConfig } from '@/config/auth';
import { serverEnv } from '@/config/server-env';
import { RegisterForm } from '@/features/auth/components/register-form';

export default function RegisterPage() {
  // Accounts come from invitations when the API keeps registration closed.
  if (!serverEnv.AUTH_REGISTRATION_ENABLED) redirect(authConfig.routes.login);
  return <RegisterForm />;
}
