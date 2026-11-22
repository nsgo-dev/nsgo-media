// The site's words. Facts come from the project's README, CHANGELOG,
// ARCHITECTURE and docs; keep both languages saying the same thing.
import type { StarlightIcon } from '@astrojs/starlight/types';
import type { IconName } from './icons';
import type { Lang } from './site';

/** A link that leaves the site, with a Starlight icon or 'nsgo' for NSGo's logo. */
export type Link = { label: string; href: string; icon: StarlightIcon | 'nsgo'; note?: string; avatar?: ImageMetadata };

export type Platform = {
	id: 'macos' | 'windows' | 'linux' | 'ios';
	name: string;
	text: string;
};

type Item = { icon: IconName; title: string; text: string };

const community = (t: { nsgoNote: string; telegram: string; awesomeNote: string }): Link[] => [
	{ label: 'NSGo', note: t.nsgoNote, href: 'https://nsgo.dev', icon: 'nsgo' },
	{ label: t.telegram, note: '@nsgo_dev', href: 'https://t.me/nsgo_dev', icon: 'telegram' },
	{ label: 'Awesome NSGo', note: t.awesomeNote, href: 'https://awesomensgo.dev/', icon: 'star' },
];

/** Where readers report problems and share ideas. */
export const feedback = 'https://github.com/oneo-me/nsgo-media/issues';

/** The usage the README shows. */
export const usage = `func Root(cx *core.Scope) core.Node {
	p := player.New(cx)
	p.Open("movie.mkv")
	return core.View{Children: []core.Node{
		player.View{Player: p},
		ui.Button{Text: "Play", OnPress: p.Play},
	}}
}`;

const en = {
	htmlLang: 'en',
	title: 'NSGo Media: a video player core for NSGo apps',
	description:
		'A video player core for NSGo apps: it decodes with FFmpeg, keeps sound and picture in sync and draws the picture in a view, while the app builds its own controls.',
	nav: { label: 'Main', menu: 'Menu', docs: 'Docs', features: 'Features', github: 'GitHub', community: 'Community', feedback: 'Feedback' },
	community: community({ nsgoNote: 'The UI engine NSGo Media plays in', telegram: 'NSGo Telegram', awesomeNote: 'Apps, libraries and resources' }),
	theme: { name: 'Theme', light: 'Light', dark: 'Dark', auto: 'Auto' },
	language: { name: 'Language', auto: 'Auto' },
	status: 'In development · no release yet',
	headline: ['A video player core', 'for NSGo apps.'],
	lead: 'NSGo Media decodes with FFmpeg, keeps sound and picture in sync and draws the picture in a view. Every button, bar and subtitle is your app’s own.',
	cta: { start: 'Get started' },
	meta: ['macOS', 'Windows', 'Linux', 'iOS'],
	mock: {
		label: 'A player view with the app’s controls drawn above the picture',
		core: 'The core draws',
		app: 'Your app draws',
		cue: 'Subtitles reach the app as cues.',
	},
	features: {
		eyebrow: 'The core',
		title: 'Everything beneath the picture',
		items: [
			{ icon: 'film', title: 'Plays what FFmpeg decodes', text: 'Local files and HTTP(S) URLs in any container and codec the bundled FFmpeg decodes, with the headers a CDN asks for and a separate audio source kept in sync.' },
			{ icon: 'skip-forward', title: 'Down to the frame', text: 'Fast or exact seeking, stepping one frame forward or backward, rates from 0.25× to 4× keeping the pitch, looping and snapshots.' },
			{ icon: 'cpu', title: 'Hardware decoding', text: 'VideoToolbox, D3D11VA and VAAPI, falling back to software decoding when the hardware fails.' },
			{ icon: 'subtitles', title: 'Subtitles as cues', text: 'Text tracks and SRT, WebVTT and ASS files as timed text; PGS, DVD, DVB and VobSub as bitmaps placed on the picture.' },
			{ icon: 'signal', title: 'HLS and DASH', text: 'On demand and live, read in Go, with the variant chosen by the bandwidth or by the app, and HLS segments encrypted with AES-128.' },
			{ icon: 'speaker', title: 'Audio passthrough', text: 'AC-3, E-AC-3, DTS, DTS-HD and TrueHD sent undecoded to a receiver on HDMI or S/PDIF, and PCM where the device refuses.' },
		] satisfies Item[],
	},
	usage: {
		eyebrow: 'Usage',
		title: 'Your controls, its picture',
		text: 'An app opens a player, places its view and calls its methods from its own controls. The demo app is a complete player built this way.',
		items: [
			{ icon: 'layers', title: 'Views draw above it', text: 'The picture is an ordinary image view on every desktop, never a native window, so the app’s controls and overlays sit on top.' },
			{ icon: 'code', title: 'State as signals', text: 'State, position, buffered range, tracks and cues are NSGo signals the app’s views read.' },
			{ icon: 'package', title: 'No cgo', text: 'FFmpeg and the audio systems are reached through purego, and FFmpeg’s LGPL shared libraries load at run time.' },
		] satisfies Item[],
	},
	platforms: {
		eyebrow: 'Platforms',
		title: 'Four platforms, one Go API',
		items: [
			{ id: 'macos', name: 'macOS', text: 'Apple silicon and Intel, macOS 11 or later. VideoToolbox decoding, CoreAudio output and passthrough.' },
			{ id: 'windows', name: 'Windows', text: 'x64, Windows 10 or later. D3D11VA decoding, WASAPI output and passthrough in exclusive mode.' },
			{ id: 'linux', name: 'Linux', text: 'x64 with glibc 2.34 and libva 2. VAAPI decoding, PulseAudio or PipeWire output and passthrough.' },
			{ id: 'ios', name: 'iOS', text: 'iOS 15 or later, on devices and the arm64 simulator. VideoToolbox decoding and an AudioQueue output.' },
		] satisfies Platform[],
	},
	end: {
		site: 'NSGo website',
		title: 'Built for NSGo',
		text: 'NSGo Media is a Go module NSGo apps import, and it uses only NSGo’s public API. NSGo is a cross-platform native UI engine with a Go backend.',
	},
	footer: {
		copyright: '© 2026 ONEO',
		license: { label: 'MIT License', href: 'https://github.com/oneo-me/nsgo-media/blob/main/LICENSE' },
		links: [
			{ label: 'Docs', href: 'docs/' },
			{ label: 'Feedback', href: 'https://github.com/oneo-me/nsgo-media/issues' },
			{ label: 'Changelog', href: 'https://github.com/oneo-me/nsgo-media/blob/main/CHANGELOG.md' },
			{ label: 'GitHub', href: 'https://github.com/oneo-me/nsgo-media' },
		],
	},
};

const zh: typeof en = {
	htmlLang: 'zh-CN',
	title: 'NSGo Media：面向 NSGo 应用的视频播放器内核',
	description: '面向 NSGo 应用的视频播放器内核：用 FFmpeg 解码，同步声音和画面，并在视图中绘制画面；控制界面由应用自己实现。',
	nav: { label: '主导航', menu: '菜单', docs: '文档', features: '功能', github: 'GitHub', community: '社区', feedback: '反馈' },
	community: community({ nsgoNote: 'NSGo Media 所在的 UI 引擎', telegram: 'NSGo Telegram 群组', awesomeNote: '应用、库和资源' }),
	theme: { name: '主题', light: '浅色', dark: '深色', auto: '自动' },
	language: { name: '语言', auto: '自动' },
	status: '开发中 · 尚未发布',
	headline: ['视频播放器内核，', '为 NSGo 应用而生。'],
	lead: 'NSGo Media 用 FFmpeg 解码，同步声音和画面，并在视图中绘制画面。每个按钮、进度条和字幕，都由你的应用自己来画。',
	cta: { start: '入门指南' },
	meta: ['macOS', 'Windows', 'Linux', 'iOS'],
	mock: {
		label: '一个播放器视图，应用的控件画在画面之上',
		core: '内核绘制',
		app: '应用绘制',
		cue: '字幕以 cue 的形式交给应用。',
	},
	features: {
		eyebrow: '内核',
		title: '画面之下的一切',
		items: [
			{ icon: 'film', title: 'FFmpeg 能解的都能播', text: '本地文件和 HTTP(S) 地址，任何内置 FFmpeg 能解码的容器和编码；可带上 CDN 要求的请求头，也能同步播放单独的音频源。' },
			{ icon: 'skip-forward', title: '精确到帧', text: '快速或精确跳转，暂停时向前或向后逐帧，0.25× 到 4× 的保持音调变速，循环播放和截图。' },
			{ icon: 'cpu', title: '硬件解码', text: 'VideoToolbox、D3D11VA 和 VAAPI，硬件出错时退回软件解码。' },
			{ icon: 'subtitles', title: '字幕即 cue', text: '文本轨道和 SRT、WebVTT、ASS 文件给出定时文本；PGS、DVD、DVB 和 VobSub 给出放在画面上的位图。' },
			{ icon: 'signal', title: 'HLS 和 DASH', text: '点播和直播，在 Go 中读取；清晰度按带宽或由应用选择，并支持 AES-128 加密的 HLS 分片。' },
			{ icon: 'speaker', title: '音频直通', text: '把 AC-3、E-AC-3、DTS、DTS-HD 和 TrueHD 不经解码送给 HDMI 或 S/PDIF 上的功放；设备不接受时播放 PCM。' },
		],
	},
	usage: {
		eyebrow: '用法',
		title: '控件归你，画面归它',
		text: '应用打开一个播放器，放好它的视图，再从自己的控件里调用它的方法。演示应用就是这样做出的一个完整播放器。',
		items: [
			{ icon: 'layers', title: '视图可以画在上面', text: '画面在每个桌面平台上都是普通的图像视图，而不是原生窗口，所以应用的控件和浮层都能盖在上面。' },
			{ icon: 'code', title: '状态即信号', text: '状态、位置、缓冲范围、轨道和字幕 cue 都是 NSGo 信号，由应用的视图读取。' },
			{ icon: 'package', title: '不用 cgo', text: '通过 purego 调用 FFmpeg 和各平台音频系统，FFmpeg 的 LGPL 共享库在运行时加载。' },
		],
	},
	platforms: {
		eyebrow: '平台',
		title: '四个平台，一套 Go API',
		items: [
			{ id: 'macos', name: 'macOS', text: 'Apple 芯片和 Intel，macOS 11 及以上。VideoToolbox 解码，CoreAudio 输出和直通。' },
			{ id: 'windows', name: 'Windows', text: 'x64，Windows 10 及以上。D3D11VA 解码，WASAPI 输出和独占模式直通。' },
			{ id: 'linux', name: 'Linux', text: 'x64，需要 glibc 2.34 和 libva 2。VAAPI 解码，PulseAudio 或 PipeWire 输出和直通。' },
			{ id: 'ios', name: 'iOS', text: 'iOS 15 及以上，真机和 arm64 模拟器。VideoToolbox 解码和 AudioQueue 输出。' },
		],
	},
	end: {
		site: 'NSGo 官网',
		title: '为 NSGo 而建',
		text: 'NSGo Media 是 NSGo 应用导入的 Go 模块，只使用 NSGo 的公开 API。NSGo 是以 Go 为后端的跨平台原生 UI 引擎。',
	},
	footer: {
		copyright: '© 2026 ONEO',
		license: { label: 'MIT 许可证', href: 'https://github.com/oneo-me/nsgo-media/blob/main/LICENSE' },
		links: [
			{ label: '文档', href: 'zh/docs/' },
			{ label: '反馈', href: 'https://github.com/oneo-me/nsgo-media/issues' },
			{ label: '更新日志', href: 'https://github.com/oneo-me/nsgo-media/blob/main/CHANGELOG.md' },
			{ label: 'GitHub', href: 'https://github.com/oneo-me/nsgo-media' },
		],
	},
};

export const copy = { en, zh } satisfies Record<Lang, typeof en>;

export const docsHome = { en: 'docs/', zh: 'zh/docs/' } satisfies Record<Lang, string>;
export const homePath = { en: '', zh: 'zh/' } satisfies Record<Lang, string>;
