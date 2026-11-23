// The site's settings, for the nsgo-website integration and the share-cards
// command. Every fact comes from the project's README, CHANGELOG,
// ARCHITECTURE and docs; keep both languages saying the same thing.
import { defineSite } from 'nsgo-website/config';

const repo = 'https://github.com/nsgo-dev/nsgo-media';

/** The usage the README shows. */
const usage = `func Root(cx *core.Scope) core.Node {
	p := player.New(cx)
	p.Open("movie.mkv")
	return core.View{Children: []core.Node{
		player.View{Player: p},
		ui.Button{Text: "Play", OnPress: p.Play},
	}}
}`;

/** A plugin's stream export, as the plugins doc shows it. */
const pluginStream = `export async function stream({ video, part, quality }) {
  return {
    video: { url: "https://cdn.example.com/v.m4s" },
    audio: { url: "https://cdn.example.com/a.m4s" },
    quality: "720",
    qualities: [
      { id: "1080", label: "1080p" },
      { id: "720", label: "720p" },
    ],
    subtitles: [
      { label: "English", url: "https://cdn.example.com/en.vtt" },
    ],
  };
}`;

// Each highlight's icon in a color of its own.
const playerColors = ['#22a45d', '#ec4899', '#3b82f6', '#f59e0b', '#8b5cf6', '#0ea5e9'];
const pluginColors = ['#8b5cf6', '#14b8a6', '#d97706'];
const featureColors = ['#3b82f6', '#8b5cf6', '#22a45d', '#f59e0b', '#0ea5e9', '#ec4899'];
const usageColors = ['#6366f1', '#14b8a6', '#d97706'];
const platformColors = ['#3b82f6', '#0ea5e9', '#d97706', '#8b5cf6', '#6366f1', '#ec4899', '#22a45d', '#14b8a6'];
const paint = <T,>(items: T[], colors: string[]) => items.map((item, i) => ({ ...item, color: colors[i] }));

export default defineSite({
	languages: ['en', 'zh'],
	name: 'NSGo Media',
	title: { en: 'NSGo Player: a desktop video player on the NSGo Media core', zh: 'NSGo Player：基于 NSGo Media 内核的桌面视频播放器' },
	description: {
		en: 'NSGo Player plays your video folders, and every other source through sandboxed plugins. Beneath it, NSGo Media is a video player core any NSGo app can build on.',
		zh: 'NSGo Player 播放你的视频文件夹，其他来源都由沙箱里的插件提供。它的下面是 NSGo Media，任何 NSGo 应用都能使用的视频播放器内核。',
	},
	logo: './src/assets/logo.png',
	appleTouchIcon: '/apple-touch-icon.png',
	repo,
	nav: [
		{ label: { en: 'Player', zh: '播放器' }, href: '#features' },
		{ label: { en: 'Core', zh: '内核' }, href: '#core' },
		'docs',
		'roadmap',
		'community',
		'feedback',
	],
	accounts: [{ label: 'ONEO', note: '@oneo666', href: 'https://x.com/oneo666', icon: 'x.com', avatar: './src/assets/avatars/oneo.jpg' }],
	community: [
		{ label: 'NSGo', note: { en: 'The UI engine NSGo Media plays in', zh: 'NSGo Media 所在的 UI 引擎' }, href: 'https://nsgo.dev', icon: 'nsgo' },
		{ label: { en: 'NSGo Telegram', zh: 'NSGo Telegram 群组' }, note: '@nsgo_dev', href: 'https://t.me/nsgo_dev', icon: 'telegram' },
		{ label: 'Awesome NSGo', note: { en: 'Apps, libraries and resources', zh: '应用、库和资源' }, href: 'https://awesomensgo.dev/', icon: 'star' },
	],
	// Every docs page, by its English slug; Chinese pages take the same places.
	docs: {
		// Readers of the app first, then developers building on the core.
		sidebar: [
			{ label: { en: 'Using NSGo Player', zh: '使用 NSGo Player' }, items: ['docs', 'docs/plugins'] },
			{ label: { en: 'Building on the core', zh: '基于内核开发' }, items: ['docs/core', 'docs/core/quick-start'] },
			{ label: { en: 'Guides', zh: '指南' }, items: ['docs/core/playback', 'docs/core/subtitles', 'docs/core/streams', 'docs/core/passthrough'] },
			{ label: { en: 'Shipping', zh: '发布' }, items: ['docs/core/ffmpeg'] },
		],
	},
	roadmap: {
		tracking: 2,
		description: {
			en: 'The stages of NSGo Media, their tasks by area and their subtasks, with each one’s progress, pull requests and what it waits for, from its GitHub issues.',
			zh: 'NSGo Media 的各个阶段、按领域分组的任务和子任务，以及每一项的进度、拉取请求和在等待什么，来自它的 GitHub issue。',
		},
	},
	footer: { copyright: '© 2026 ONEO' },
	// The cards' picture: the player view the hero draws, with the app's controls on the frame.
	share: {
		visual: `<div class="window"><div class="bar"><i></i><i></i><i></i></div><div class="pic"><span class="sun"></span><span class="hill far"></span><span class="hill near"></span><div class="controls"><span class="play"><svg viewBox="0 0 24 24" width="18" height="18"><path d="M8 4.5v15l12-7.5z" fill="#0a0b0d"/></svg></span><span class="track"><b></b></span></div></div></div>`,
		css: `.window { border: 1.5px solid var(--border); border-radius: 28px; overflow: hidden; background: var(--surface); box-shadow: 0 40px 80px -40px rgb(16 24 40 / 0.45); }
.bar { display: flex; gap: 9px; padding: 16px 18px; border-bottom: 1.5px solid var(--border); }
.bar i { width: 14px; height: 14px; border-radius: 50%; background: var(--border-strong); }
.pic { position: relative; aspect-ratio: 16 / 9; overflow: hidden; background: linear-gradient(180deg, #1e3a8a 0%, #6d28d9 45%, #f97316 88%, #fdba74 100%); }
.sun { position: absolute; left: 56%; top: 36%; width: 24%; aspect-ratio: 1; border-radius: 50%; background: radial-gradient(circle, #fff7ed 0 35%, #fdba74 60%, rgb(253 186 116 / 0) 72%); }
.hill { position: absolute; left: -20%; right: -20%; border-radius: 50% 50% 0 0; }
.far { bottom: -38%; height: 72%; background: linear-gradient(180deg, #4c1d95, #1e1b4b); transform: translateX(-12%); }
.near { bottom: -52%; height: 80%; background: linear-gradient(180deg, #1e1b4b, #0b1220); transform: translateX(16%); }
.controls { position: absolute; left: 18px; right: 18px; bottom: 18px; display: flex; align-items: center; gap: 14px; padding: 9px 16px 9px 9px; border-radius: 999px; background: rgb(10 11 13 / 0.55); border: 2px dashed rgb(140 196 255 / 0.85); }
.play { width: 40px; height: 40px; border-radius: 50%; background: #fff; display: grid; place-items: center; }
.track { flex: 1; height: 6px; border-radius: 999px; background: rgb(255 255 255 / 0.25); position: relative; overflow: hidden; }
.track b { position: absolute; inset: 0 auto 0 0; width: 42%; background: #4da3ff; border-radius: inherit; }
/* The target platforms' chips, in rows. */
.chips { flex-wrap: wrap; gap: 8px; margin-top: 28px; }
.chips span { padding: 5px 14px; font-size: 18px; white-space: nowrap; }`,
	},
	// The page leads with NSGo Player, for its viewers, and puts the core beneath it, for developers.
	home: {
		status: { en: 'In development · no release yet', zh: '开发中 · 尚未发布' },
		headline: { en: ['A video player,', 'open to any source.'], zh: ['视频播放器，', '来源交给插件。'] },
		lead: {
			en: 'NSGo Player plays the videos in your folders with their subtitles, and every other source through a plugin of one file, sandboxed. Beneath it is NSGo Media, a video player core any NSGo app can build on.',
			zh: 'NSGo Player 播放你文件夹里的视频和旁边的字幕，其他来源只需一个文件的插件，运行在沙箱里。它的下面是 NSGo Media，任何 NSGo 应用都能使用的视频播放器内核。',
		},
		actions: [
			{ label: { en: 'Get started', zh: '开始使用' }, href: 'docs/' },
			{ label: { en: 'Build on the core', zh: '基于内核开发' }, href: 'docs/core/', variant: 'secondary' },
		],
		// The NSGo platforms the core targets; the platforms section says which play today.
		chips: {
			en: ['macOS', 'Windows', 'Linux', 'iOS', 'tvOS', 'Android', 'Web', 'HarmonyOS'],
			zh: ['macOS', 'Windows', 'Linux', 'iOS', 'tvOS', 'Android', 'Web', '鸿蒙'],
		},
		visual: './src/components/AppMock.astro',
		sections: {
			en: [
				{
					type: 'features',
					id: 'features',
					eyebrow: 'NSGo Player',
					title: 'Your videos, its controls',
					items: paint(
						[
							{ icon: 'folder-open', title: 'Your video folders', text: 'The built-in Local Files plugin lists your folders, opens their subfolders, searches their names and plays a path you paste.' },
							{ icon: 'subtitles', title: 'Subtitles beside it', text: 'A subtitle file named after a video, such as Movie.srt or Movie.zh-CN.ass, plays with it; pick any audio or subtitle track.' },
							{ icon: 'keyboard', title: 'Controls and shortcuts', text: 'A progress bar with the buffered range, volume, speed, fullscreen, and keys to play, seek and turn the volume.' },
							{ icon: 'history', title: 'Resumes where you left', text: 'Each video resumes where you stopped, and the next part follows at the end.' },
							{ icon: 'puzzle', title: 'Every other source a plugin', text: 'A plugin brings a source’s pages, search, qualities, subtitles and sign-in into the sidebar. The app itself knows no site.' },
							{ icon: 'languages', title: 'English and Chinese', text: 'The app and plugins’ own texts in English and Simplified Chinese, following the system unless you pick one.' },
						],
						playerColors,
					),
				},
				{
					type: 'split',
					id: 'plugins',
					eyebrow: 'Plugins',
					title: 'A source in one file',
					text: 'A plugin is one JavaScript or TypeScript module and a YAML manifest. The app calls its exports and draws every page itself, so a plugin carries no UI.',
					items: paint(
						[
							{ icon: 'shield-check', title: 'Sandboxed', text: 'Each plugin runs in its own moejs sandbox and reaches only the hosts you approved, never your files.' },
							{ icon: 'file-code', title: 'Used from a path', text: 'Type a plugin folder’s path in the settings and the app reads it where it is, while you write it.' },
							{ icon: 'archive', title: 'Shared as a zip', text: 'Zip a plugin’s folder to share it; others install it from the zip or a folder.' },
						],
						pluginColors,
					),
					code: { lang: 'ts', code: pluginStream },
				},
				{
					type: 'split',
					id: 'core',
					eyebrow: 'For developers',
					title: 'NSGo Media, the core beneath it',
					text: 'NSGo Player is built on a video player core any NSGo app can import. It decodes with FFmpeg, keeps sound and picture in sync and draws the picture in a view; every button, bar and subtitle is the app’s own.',
					items: paint(
						[
							{ icon: 'layers', title: 'Views draw above it', text: 'The picture is an ordinary image view on every desktop, never a native window, so the app’s controls and overlays sit on top.' },
							{ icon: 'code', title: 'State as signals', text: 'State, position, buffered range, tracks and cues are NSGo signals the app’s views read.' },
							{ icon: 'package', title: 'No cgo', text: 'FFmpeg and the audio systems are reached through purego, and FFmpeg’s LGPL shared libraries load at run time.' },
						],
						usageColors,
					),
					component: './src/components/PlayerMock.astro',
				},
				{
					type: 'features',
					eyebrow: 'The core',
					title: 'Everything beneath the picture',
					items: paint(
						[
							{ icon: 'film', title: 'Plays what FFmpeg decodes', text: 'Local files and HTTP(S) URLs in any container and codec the bundled FFmpeg decodes, with the headers a CDN asks for and a separate audio source kept in sync.' },
							{ icon: 'skip-forward', title: 'Down to the frame', text: 'Fast or exact seeking, stepping one frame forward or backward, rates from 0.25× to 4× keeping the pitch, looping and snapshots.' },
							{ icon: 'cpu', title: 'Hardware decoding', text: 'VideoToolbox, D3D11VA and VAAPI, falling back to software decoding when the hardware fails.' },
							{ icon: 'subtitles', title: 'Subtitles as cues', text: 'Text tracks and SRT, WebVTT and ASS files as timed text; PGS, DVD, DVB and VobSub as bitmaps placed on the picture.' },
							{ icon: 'signal', title: 'HLS and DASH', text: 'On demand and live, read in Go, with the variant chosen by the bandwidth or by the app, and HLS segments encrypted with AES-128.' },
							{ icon: 'speaker', title: 'Audio passthrough', text: 'AC-3, E-AC-3, DTS, DTS-HD and TrueHD sent undecoded to a receiver on HDMI or S/PDIF, and PCM where the device refuses.' },
						],
						featureColors,
					),
				},
				{
					type: 'split',
					eyebrow: 'Usage',
					title: 'Your controls, its picture',
					text: 'An app opens a player, places its view and calls its methods from its own controls. NSGo Player is built this way.',
					code: { lang: 'go', code: usage },
				},
				{
					type: 'cards',
					eyebrow: 'Platforms',
					title: 'Eight platforms, one Go API',
					text: 'The core targets eight of the platforms NSGo runs on. It plays on four today, and NSGo Player runs on the three desktops; the rest are goals.',
					columns: 4,
					items: paint(
						[
							{ icon: 'laptop', title: 'macOS', text: 'Apple silicon and Intel, macOS 11 or later. VideoToolbox decoding, CoreAudio output and passthrough.' },
							{ icon: 'app-window', title: 'Windows', text: 'x64, Windows 10 or later. D3D11VA decoding, WASAPI output and passthrough in exclusive mode.' },
							{ icon: 'terminal', title: 'Linux', text: 'x64 with glibc 2.34 and libva 2. VAAPI decoding, PulseAudio or PipeWire output and passthrough.' },
							{ icon: 'smartphone', title: 'iOS', text: 'iOS 15 or later, on devices and the arm64 simulator. VideoToolbox decoding and an AudioQueue output.' },
							{ icon: 'tv', title: 'tvOS', text: 'A goal: the core does not play here yet.' },
							{ icon: 'tablet-smartphone', title: 'Android', text: 'A goal: the core does not play here yet.' },
							{ icon: 'globe', title: 'Web', text: 'A goal: the core does not play here yet.' },
							{ icon: 'tablet', title: 'HarmonyOS', text: 'A goal: the core does not play here yet.' },
						],
						platformColors,
					),
				},
				{
					type: 'end',
					logo: 'nsgo',
					title: 'Built for NSGo',
					text: 'NSGo Player and NSGo Media use only NSGo’s public API. NSGo is a cross-platform native UI engine with a Go backend.',
					actions: [
						{ label: 'NSGo website', href: 'https://nsgo.dev' },
						{ label: 'GitHub', href: 'https://github.com/nsgo-dev/nsgo', icon: 'github' },
					],
				},
			],
			zh: [
				{
					type: 'features',
					id: 'features',
					eyebrow: 'NSGo Player',
					title: '你的视频，它的控件',
					items: paint(
						[
							{ icon: 'folder-open', title: '你的视频文件夹', text: '内置的本地文件插件列出你的文件夹，打开子文件夹、按名字搜索，也能播放粘贴的路径。' },
							{ icon: 'subtitles', title: '字幕跟着视频', text: '和视频同名的字幕文件（如 Movie.srt、Movie.zh-CN.ass）会一起加载；音轨和字幕轨道任你选择。' },
							{ icon: 'keyboard', title: '控件和快捷键', text: '带缓冲范围的进度条、音量、速度和全屏，按键就能播放、跳转和调音量。' },
							{ icon: 'history', title: '从上次停下处继续', text: '每个视频都从上次停下的地方继续，播完后自动播放下一P。' },
							{ icon: 'puzzle', title: '其他来源都是插件', text: '插件把一个来源的页面、搜索、画质、字幕和登录带进侧栏。应用本身不认识任何网站。' },
							{ icon: 'languages', title: '中文和英文', text: '应用和插件自己的文字支持简体中文和英文，默认跟随系统，也可以自己选。' },
						],
						playerColors,
					),
				},
				{
					type: 'split',
					id: 'plugins',
					eyebrow: '插件',
					title: '一个文件，一个来源',
					text: '插件是一个 JavaScript 或 TypeScript 模块加一份 YAML 清单。应用调用它的导出函数，所有页面都由应用自己画，所以插件不带界面。',
					items: paint(
						[
							{ icon: 'shield-check', title: '沙箱运行', text: '每个插件运行在自己的 moejs 沙箱里，只能访问你确认过的主机，碰不到你的文件。' },
							{ icon: 'file-code', title: '从路径使用', text: '在设置里填插件文件夹的路径，应用就从原处读取，边写边用。' },
							{ icon: 'archive', title: '打包成 zip 分享', text: '把插件文件夹打包成 zip 就能分享，别人从 zip 或文件夹安装。' },
						],
						pluginColors,
					),
					code: { lang: 'ts', code: pluginStream },
				},
				{
					type: 'split',
					id: 'core',
					eyebrow: '面向开发者',
					title: 'NSGo Media，它下面的内核',
					text: 'NSGo Player 基于一个任何 NSGo 应用都能导入的视频播放器内核。它用 FFmpeg 解码，同步声音和画面，并在视图中绘制画面；每个按钮、进度条和字幕，都由应用自己来画。',
					items: paint(
						[
							{ icon: 'layers', title: '视图可以画在上面', text: '画面在每个桌面平台上都是普通的图像视图，而不是原生窗口，所以应用的控件和浮层都能盖在上面。' },
							{ icon: 'code', title: '状态即信号', text: '状态、位置、缓冲范围、轨道和字幕 cue 都是 NSGo 信号，由应用的视图读取。' },
							{ icon: 'package', title: '不用 cgo', text: '通过 purego 调用 FFmpeg 和各平台音频系统，FFmpeg 的 LGPL 共享库在运行时加载。' },
						],
						usageColors,
					),
					component: './src/components/PlayerMock.astro',
				},
				{
					type: 'features',
					eyebrow: '内核',
					title: '画面之下的一切',
					items: paint(
						[
							{ icon: 'film', title: 'FFmpeg 能解的都能播', text: '本地文件和 HTTP(S) 地址，任何内置 FFmpeg 能解码的容器和编码；可带上 CDN 要求的请求头，也能同步播放单独的音频源。' },
							{ icon: 'skip-forward', title: '精确到帧', text: '快速或精确跳转，暂停时向前或向后逐帧，0.25× 到 4× 的保持音调变速，循环播放和截图。' },
							{ icon: 'cpu', title: '硬件解码', text: 'VideoToolbox、D3D11VA 和 VAAPI，硬件出错时退回软件解码。' },
							{ icon: 'subtitles', title: '字幕即 cue', text: '文本轨道和 SRT、WebVTT、ASS 文件给出定时文本；PGS、DVD、DVB 和 VobSub 给出放在画面上的位图。' },
							{ icon: 'signal', title: 'HLS 和 DASH', text: '点播和直播，在 Go 中读取；清晰度按带宽或由应用选择，并支持 AES-128 加密的 HLS 分片。' },
							{ icon: 'speaker', title: '音频直通', text: '把 AC-3、E-AC-3、DTS、DTS-HD 和 TrueHD 不经解码送给 HDMI 或 S/PDIF 上的功放；设备不接受时播放 PCM。' },
						],
						featureColors,
					),
				},
				{
					type: 'split',
					eyebrow: '用法',
					title: '控件归你，画面归它',
					text: '应用打开一个播放器，放好它的视图，再从自己的控件里调用它的方法。NSGo Player 就是这样做的。',
					code: { lang: 'go', code: usage },
				},
				{
					type: 'cards',
					eyebrow: '平台',
					title: '八个平台，一套 Go API',
					text: '内核以 NSGo 支持的其中八个平台为目标。目前能在四个平台上播放，NSGo Player 运行在三个桌面平台上；其余平台是目标。',
					columns: 4,
					items: paint(
						[
							{ icon: 'laptop', title: 'macOS', text: 'Apple 芯片和 Intel，macOS 11 及以上。VideoToolbox 解码，CoreAudio 输出和直通。' },
							{ icon: 'app-window', title: 'Windows', text: 'x64，Windows 10 及以上。D3D11VA 解码，WASAPI 输出和独占模式直通。' },
							{ icon: 'terminal', title: 'Linux', text: 'x64，需要 glibc 2.34 和 libva 2。VAAPI 解码，PulseAudio 或 PipeWire 输出和直通。' },
							{ icon: 'smartphone', title: 'iOS', text: 'iOS 15 及以上，真机和 arm64 模拟器。VideoToolbox 解码和 AudioQueue 输出。' },
							{ icon: 'tv', title: 'tvOS', text: '目标：内核还不能在这里播放。' },
							{ icon: 'tablet-smartphone', title: 'Android', text: '目标：内核还不能在这里播放。' },
							{ icon: 'globe', title: 'Web', text: '目标：内核还不能在这里播放。' },
							{ icon: 'tablet', title: '鸿蒙', text: '目标：内核还不能在这里播放。' },
						],
						platformColors,
					),
				},
				{
					type: 'end',
					logo: 'nsgo',
					title: '为 NSGo 而建',
					text: 'NSGo Player 和 NSGo Media 只使用 NSGo 的公开 API。NSGo 是以 Go 为后端的跨平台原生 UI 引擎。',
					actions: [
						{ label: 'NSGo 官网', href: 'https://nsgo.dev' },
						{ label: 'GitHub', href: 'https://github.com/nsgo-dev/nsgo', icon: 'github' },
					],
				},
			],
		},
	},
});
