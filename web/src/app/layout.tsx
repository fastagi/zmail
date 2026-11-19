import type { Metadata } from "next";
import { Inter, Newsreader, Noto_Serif_SC } from "next/font/google";
import { NextIntlClientProvider } from "next-intl";
import { getLocale, getMessages } from "next-intl/server";
import { cn } from "@/utils";
import { ThemeProvider } from "@/providers/theme-provider";
import { Analytics } from "@/components/analytics";
import { env } from "@/config/env";
import { CLIENT_MESSAGE_NAMESPACES } from "@/i18n/client-message-namespaces";
import { selectMessageNamespaces } from "@/i18n/message-selection";
import { getT } from "@/i18n/server";
import "@/config/server-env";
import "./globals.css";

// Fonts are downloaded at build time and served from this app; pages never request Google Fonts.
const inter = Inter({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-inter",
  display: "swap",
});

const newsreader = Newsreader({
  subsets: ["latin"],
  axes: ["opsz"],
  variable: "--font-newsreader",
  display: "swap",
});

const notoSerifSC = Noto_Serif_SC({
  weight: "600",
  preload: false,
  variable: "--font-noto-serif-sc",
  display: "swap",
});

export async function generateMetadata(): Promise<Metadata> {
  const t = await getT('metadata');

  return {
    title: {
      default: t('title'),
      template: `%s | ${t('title')}`,
    },
    description: t('description'),
    metadataBase: new URL(env.NEXT_PUBLIC_APP_URL),
  };
}

export default async function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const locale = await getLocale();
  const messages = await getMessages();
  const clientMessages = selectMessageNamespaces(
    messages,
    CLIENT_MESSAGE_NAMESPACES.global
  );

  return (
    <html
      lang={locale}
      className={cn(inter.variable, newsreader.variable, notoSerifSC.variable)}
      suppressHydrationWarning
    >
      <body className={cn("min-h-screen font-sans antialiased")}>
        <NextIntlClientProvider messages={clientMessages}>
          <ThemeProvider
            attribute="class"
            defaultTheme="system"
            enableSystem
            disableTransitionOnChange
          >
            {children}
          </ThemeProvider>
        </NextIntlClientProvider>
        <Analytics />
      </body>
    </html>
  );
}
