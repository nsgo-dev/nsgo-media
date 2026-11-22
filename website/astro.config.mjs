import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// GitHub Pages serves the site under the repository's name; a deploy may
// pass the address it was given in SITE and BASE, and a local build uses the
// same path so that links are checked under a subpath too.
const site = process.env.SITE ?? 'https://oneo-me.github.io';
const base = process.env.BASE ?? '/nsgo-media';

export default defineConfig({
	site,
	base,
	trailingSlash: 'always',
	devToolbar: { enabled: false },
	integrations: [
		starlight({
			title: 'NSGo Media',
			description: 'A video player core for NSGo apps: FFmpeg decoding and the picture drawn in a view, with every control left to the app.',
			logo: { src: './src/assets/logo.png' },
			favicon: '/favicon.svg',
			defaultLocale: 'root',
			locales: {
				root: { label: 'English', lang: 'en' },
				zh: { label: '简体中文', lang: 'zh-CN' },
			},
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/oneo-me/nsgo-media' },
			],
			// Every docs page, by its English slug; Chinese pages take the same
			// places. Starlight cannot list docs/public/ by itself, so a new
			// page is added here.
			sidebar: [
				{ label: 'Start', translations: { 'zh-CN': '开始' }, items: ['docs', 'docs/quick-start'] },
				{ label: 'Guides', translations: { 'zh-CN': '指南' }, items: ['docs/playback', 'docs/subtitles', 'docs/streams', 'docs/passthrough'] },
				{ label: 'Shipping', translations: { 'zh-CN': '发布' }, items: ['docs/ffmpeg'] },
			],
			// The docs live in docs/public/, outside src/content/docs, so
			// Starlight's Markdown features are turned on there too.
			markdown: { processedDirs: ['../docs/public'] },
			routeMiddleware: './src/share.ts',
			// Code as the home page shows it: the same themes in a soft frame,
			// without a title bar, even for shell commands. Its colors are set
			// in docs.css, from the site's own.
			expressiveCode: {
				themes: ['github-dark-dimmed', 'github-light'],
				defaultProps: { frame: 'code' },
				styleOverrides: {
					borderRadius: '1rem',
					codeFontSize: '0.8125rem',
					codeLineHeight: '1.7',
					codePaddingBlock: '1rem',
					codePaddingInline: '1.25rem',
					frames: {
						shadowColor: 'transparent',
						inlineButtonBorderOpacity: '0',
						inlineButtonBackgroundIdleOpacity: '0',
						inlineButtonBackgroundHoverOrFocusOpacity: '0.12',
						inlineButtonBackgroundActiveOpacity: '0.2',
					},
				},
			},
			customCss: ['./src/styles/tokens.css', './src/styles/shell.css', './src/styles/docs.css'],
			components: {
				Head: './src/components/starlight/Head.astro',
				Header: './src/components/shell/SiteHeader.astro',
				PageFrame: './src/components/starlight/PageFrame.astro',
				TwoColumnContent: './src/components/starlight/TwoColumnContent.astro',
				MobileMenuToggle: './src/components/starlight/Empty.astro',
				ContentPanel: './src/components/starlight/ContentPanel.astro',
				MarkdownContent: './src/components/starlight/MarkdownContent.astro',
				PageTitle: './src/components/starlight/PageTitle.astro',
				Footer: './src/components/starlight/Footer.astro',
				EditLink: './src/components/starlight/EditLink.astro',
			},
			head: [
				{ tag: 'link', attrs: { rel: 'apple-touch-icon', href: `${base}/apple-touch-icon.png` } },
			],
		}),
	],
});
