import type { MetadataRoute } from 'next';

// Matches the light theme canvas (`--paper-50`) so the installed app opens without a flash.
const canvas = '#FBFAF7';

/** Lets Zmail be installed (required for web push on iPhone and iPad). */
export default function manifest(): MetadataRoute.Manifest {
  return {
    name: 'Zmail',
    short_name: 'Zmail',
    id: '/console/mailboxes',
    start_url: '/console/mailboxes',
    scope: '/',
    display: 'standalone',
    background_color: canvas,
    theme_color: canvas,
    icons: [
      { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
      { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
      { src: '/icons/maskable-192.png', sizes: '192x192', type: 'image/png', purpose: 'maskable' },
      { src: '/icons/maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
    ],
  };
}
