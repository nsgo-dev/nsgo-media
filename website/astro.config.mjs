import { defineConfig } from 'astro/config';
import nsgoWebsite from 'nsgo-website';
import site from './site.config.ts';

// The site is served at media.nsgo.dev, at its root; a deploy may pass the
// address it was given in SITE and BASE.
export default defineConfig({
	site: process.env.SITE || 'https://media.nsgo.dev',
	base: process.env.BASE || '/',
	integrations: [nsgoWebsite(site)],
});
