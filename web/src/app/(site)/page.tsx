import Link from 'next/link';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { getT } from '@/i18n/server';
import {
  ArrowRight,
  Building2,
  FileText,
  Globe,
  KeyRound,
  Mail,
  Megaphone,
  ShieldCheck,
  Sparkles,
} from 'lucide-react';

/**
 * Zmail public landing page (Server Component).
 */
export default async function HomePage() {
  const t = await getT('site');
  const featuresData = [
    {
      title: t('features.authentication.title'),
      description: t('features.authentication.description'),
      icon: Globe,
    },
    {
      title: t('features.console.title'),
      description: t('features.console.description'),
      icon: Mail,
    },
    {
      title: t('features.i18n.title'),
      description: t('features.i18n.description'),
      icon: FileText,
    },
    {
      title: t('features.ui.title'),
      description: t('features.ui.description'),
      icon: Megaphone,
    },
  ];

  const capabilityPills = [
    { name: t('techStack.pills.domains'), icon: Globe },
    { name: t('techStack.pills.templates'), icon: FileText },
    { name: t('techStack.pills.staged'), icon: Megaphone },
    { name: t('techStack.pills.unsubscribe'), icon: ShieldCheck },
    { name: t('techStack.pills.organizations'), icon: Building2 },
    { name: t('techStack.pills.api'), icon: KeyRound },
  ];

  return (
    <div className="flex flex-col">
      {/* Hero Section */}
      <section className="relative overflow-hidden bg-bg-canvas">
        <div className="absolute inset-0 pointer-events-none">
          <div className="absolute top-[-20%] left-[-10%] w-[50%] h-[50%] rounded-full bg-primary/15 blur-3xl" />
          <div className="absolute bottom-[-30%] right-[-15%] w-[60%] h-[60%] rounded-full bg-primary-deeper/10 blur-3xl" />
          <div className="absolute top-[30%] right-[20%] w-[30%] h-[30%] rounded-full bg-primary/5 blur-2xl" />

          <div className="absolute top-[15%] right-[25%] w-32 h-32 rounded-full border border-primary/10" />
          <div className="absolute top-[18%] right-[27%] w-24 h-24 rounded-full border border-primary/5" />
          <div className="absolute bottom-[20%] left-[8%] w-40 h-40 rounded-full border border-primary/10" />

          <div
            className="absolute inset-0 opacity-[0.02]"
            style={{
              backgroundImage: `linear-gradient(var(--primary) 1px, transparent 1px), linear-gradient(90deg, var(--primary) 1px, transparent 1px)`,
              backgroundSize: '60px 60px',
            }}
          />
        </div>

        <div className="container relative py-20 md:py-28 lg:py-36">
          <div className="mx-auto max-w-4xl text-center">
            <Badge variant="secondary" className="mb-6 px-4 py-1.5 text-sm font-medium">
              <Sparkles className="mr-1.5 h-3.5 w-3.5" />
              {t('hero.eyebrow')}
            </Badge>

            <h1 className="text-4xl font-bold tracking-tight md:text-5xl lg:text-6xl">
              {t('hero.titlePrefix')}{' '}
              <span className="bg-linear-to-r from-primary to-primary-deeper bg-clip-text text-transparent">
                {t('hero.titleAccent')}
              </span>
            </h1>

            <p className="mt-6 text-lg text-text-muted md:text-xl max-w-2xl mx-auto">
              {t('hero.description')}
            </p>

            <div className="mt-10 flex flex-wrap items-center justify-center gap-4">
              <Button asChild size="lg" className="h-12 px-8 gap-2 shadow-button-primary">
                <Link href="/console">
                  {t('hero.primaryAction')}
                  <ArrowRight className="h-4 w-4" />
                </Link>
              </Button>
              <Button asChild variant="outline" size="lg" className="h-12 px-8">
                <Link href="/login">{t('hero.secondaryAction')}</Link>
              </Button>
            </div>
          </div>
        </div>
      </section>

      {/* Features Section */}
      <section className="py-20 md:py-28 bg-bg-subtle/30">
        <div className="container">
          <div className="text-center mb-12">
            <h2 className="text-3xl font-bold tracking-tight md:text-4xl">
              {t('features.title')}
            </h2>
            <p className="mt-4 text-text-muted max-w-2xl mx-auto">
              {t('features.description')}
            </p>
          </div>

          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
            {featuresData.map(feature => (
              <Card
                key={feature.title}
                className="group bg-bg-surface border-border/50 hover:shadow-premium hover:-translate-y-1 transition-all duration-300"
              >
                <CardContent className="p-6">
                  <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-primary/10 text-primary group-hover:bg-primary group-hover:text-primary-foreground transition-all duration-300">
                    <feature.icon className="h-6 w-6" />
                  </div>
                  <h3 className="mt-4 text-lg font-semibold">{feature.title}</h3>
                  <p className="mt-2 text-sm text-text-muted leading-relaxed">
                    {feature.description}
                  </p>
                </CardContent>
              </Card>
            ))}
          </div>
        </div>
      </section>

      {/* Capabilities Section */}
      <section className="py-20 md:py-28">
        <div className="container">
          <div className="text-center mb-12">
            <h2 className="text-3xl font-bold tracking-tight md:text-4xl">
              {t('techStack.title')}
            </h2>
            <p className="mt-4 text-text-muted">{t('techStack.description')}</p>
          </div>

          <div className="flex flex-wrap items-center justify-center gap-4 md:gap-6">
            {capabilityPills.map(item => (
              <div
                key={item.name}
                className="flex items-center gap-3 px-5 py-3 rounded-xl bg-bg-surface border border-border/50 hover:border-primary/30 hover:shadow-md transition-all duration-300"
              >
                <item.icon className="h-5 w-5 text-primary" aria-hidden="true" />
                <span className="font-medium">{item.name}</span>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* CTA Section */}
      <section className="relative overflow-hidden bg-linear-to-br from-primary via-primary/95 to-primary-deeper py-20 md:py-28">
        <div className="absolute inset-0 pointer-events-none">
          <div className="absolute top-[-20%] right-[-10%] w-[40%] h-[40%] rounded-full bg-white/10 blur-3xl" />
          <div className="absolute bottom-[-20%] left-[-10%] w-[30%] h-[30%] rounded-full bg-primary-deeper/30 blur-2xl" />
        </div>

        <div className="container relative text-center text-white">
          <h2 className="text-3xl font-bold tracking-tight md:text-4xl lg:text-5xl">
            {t('cta.title')}
          </h2>
          <p className="mt-4 text-white/80 max-w-xl mx-auto md:text-lg">
            {t('cta.description')}
          </p>
          <div className="mt-10 flex flex-wrap items-center justify-center gap-4">
            <Button asChild size="lg" variant="secondary" className="h-12 px-8 gap-2">
              <Link href="/console">
                {t('cta.primaryAction')}
                <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
            <Button
              asChild
              size="lg"
              variant="outline"
              className="h-12 px-8 bg-transparent border-white/30 text-white hover:bg-white/10 hover:text-white"
            >
              <a
                href="https://github.com/fastagi/zmail"
                target="_blank"
                rel="noopener noreferrer"
              >
                {t('cta.githubAction')}
              </a>
            </Button>
          </div>
        </div>
      </section>
    </div>
  );
}
