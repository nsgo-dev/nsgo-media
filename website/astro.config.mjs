import { defineConfig } from 'astro/config';
import nsgoWebsite from 'nsgo-website';
import site from './site.config.ts';

// The site is served at media.nsgo.dev, at its root; a deploy may pass the
// address it was given in SITE and BASE.
export default defineConfig({
	site: process.env.SITE || 'https://media.nsgo.dev',
	base: process.env.BASE || '/',
	integrations: [nsgoWebsite(site), umami()],
});

// Umami counts the deployed site's visits, tagged "website" beside the
// demo's "demo" and NSGo Player's "app" (internal/stats); data-domains
// skips local servers. The script is added to every page's head without
// replacing the head Starlight is given.
function umami() {
	const attrs = {
		src: 'https://umami.oneo.me/script.js',
		'data-website-id': '4cb99b9f-3529-4c88-9507-3607baaf15b3',
		'data-domains': 'media.nsgo.dev',
		'data-tag': 'website',
	};
	return {
		name: 'umami',
		hooks: {
			'astro:config:setup': ({ injectScript }) =>
				injectScript(
					'head-inline',
					`(() => { const s = document.createElement('script'); s.defer = true; for (const [k, v] of Object.entries(${JSON.stringify(attrs)})) s.setAttribute(k, v); document.head.append(s); })();`,
				),
		},
	};
}
