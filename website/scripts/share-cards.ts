// Draws the cards that previews show when a page is shared on X, Telegram
// and the like: one 1200×630 PNG per language in public/share/, from the
// home page's words and the player view it draws. Run it with
// `bun run share-cards` after changing either; it needs Chrome.
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { copy } from '../src/copy';
import type { Lang } from '../src/site';

const chrome = process.env.CHROME ?? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const out = join(import.meta.dir, '../public/share');
const work = await mkdtemp(join(tmpdir(), 'share-cards-'));

async function run(cmd: string[]) {
	const proc = Bun.spawn(cmd, { stdout: 'ignore', stderr: 'pipe' });
	if ((await proc.exited) !== 0) throw new Error(`${cmd[0]}: ${await new Response(proc.stderr).text()}`);
}

const logo = `data:image/svg+xml;base64,${(await readFile(join(import.meta.dir, '../src/assets/logo.svg'))).toString('base64')}`;
const tokens = await readFile(join(import.meta.dir, '../src/styles/tokens.css'), 'utf8');

const html = (lang: Lang) => {
	const t = copy[lang];
	return `<!doctype html><html lang="${t.htmlLang}" data-theme="light"><meta charset="utf-8"><style>
${tokens}
* { box-sizing: border-box; margin: 0; }
html, body { width: 1200px; height: 630px; overflow: hidden; }
body { background: var(--bg); color: var(--text); font-family: var(--font-sans);
	display: grid; grid-template-columns: 1fr 520px; gap: 48px; align-items: center; padding: 64px 64px 64px 72px; -webkit-font-smoothing: antialiased; }
.text { display: flex; flex-direction: column; height: 100%; min-width: 0; }
.brand { display: flex; align-items: center; gap: 14px; font-size: 28px; font-weight: 650; letter-spacing: -0.01em; }
.brand img { width: 52px; height: 52px; }
h1 { margin-top: auto; font-size: ${lang === 'zh' ? 56 : 58}px; line-height: 1.1; font-weight: 750; letter-spacing: ${lang === 'zh' ? '-0.01em' : '-0.03em'}; }
h1 span { display: block; white-space: nowrap; }
h1 span + span { background: linear-gradient(100deg, var(--accent) 10%, #8b5cf6 90%); -webkit-background-clip: text; color: transparent; }
.chips { display: flex; gap: 10px; margin-top: 36px; }
.chips span { border: 1.5px solid var(--border-strong); border-radius: 999px; padding: 8px 18px; font-size: 22px; color: var(--muted); }
.window { border: 1.5px solid var(--border); border-radius: 28px; overflow: hidden; background: var(--surface);
	box-shadow: 0 40px 80px -40px rgb(16 24 40 / 0.45); }
.bar { display: flex; gap: 9px; padding: 16px 18px; border-bottom: 1.5px solid var(--border); }
.bar i { width: 14px; height: 14px; border-radius: 50%; background: var(--border-strong); }
.pic { position: relative; aspect-ratio: 16 / 9; overflow: hidden;
	background: linear-gradient(180deg, #1e3a8a 0%, #6d28d9 45%, #f97316 88%, #fdba74 100%); }
.sun { position: absolute; left: 56%; top: 36%; width: 24%; aspect-ratio: 1; border-radius: 50%;
	background: radial-gradient(circle, #fff7ed 0 35%, #fdba74 60%, rgb(253 186 116 / 0) 72%); }
.hill { position: absolute; left: -20%; right: -20%; border-radius: 50% 50% 0 0; }
.far { bottom: -38%; height: 72%; background: linear-gradient(180deg, #4c1d95, #1e1b4b); transform: translateX(-12%); }
.near { bottom: -52%; height: 80%; background: linear-gradient(180deg, #1e1b4b, #0b1220); transform: translateX(16%); }
.controls { position: absolute; left: 18px; right: 18px; bottom: 18px; display: flex; align-items: center; gap: 14px;
	padding: 9px 16px 9px 9px; border-radius: 999px; background: rgb(10 11 13 / 0.55); border: 2px dashed rgb(140 196 255 / 0.85); }
.play { width: 40px; height: 40px; border-radius: 50%; background: #fff; display: grid; place-items: center; }
.track { flex: 1; height: 6px; border-radius: 999px; background: rgb(255 255 255 / 0.25); position: relative; overflow: hidden; }
.track b { position: absolute; inset: 0 auto 0 0; width: 42%; background: #4da3ff; border-radius: inherit; }
</style><body>
<div class="text">
	<div class="brand"><img src="${logo}" alt="">NSGo Media</div>
	<h1>${t.headline.map((line) => `<span>${line}</span>`).join('')}</h1>
	<div class="chips">${t.meta.map((m) => `<span>${m}</span>`).join('')}</div>
</div>
<div class="window"><div class="bar"><i></i><i></i><i></i></div><div class="pic">
	<span class="sun"></span><span class="hill far"></span><span class="hill near"></span>
	<div class="controls"><span class="play"><svg viewBox="0 0 24 24" width="18" height="18"><path d="M8 4.5v15l12-7.5z" fill="#0a0b0d"/></svg></span><span class="track"><b></b></span></div>
</div></div>
</body></html>`;
};

await mkdir(out, { recursive: true });
for (const lang of ['en', 'zh'] as const) {
	const page = join(work, `${lang}.html`);
	await writeFile(page, html(lang));
	await run([chrome, '--headless=new', '--hide-scrollbars', '--force-device-scale-factor=1',
		'--window-size=1200,630', `--screenshot=${join(out, `${lang}.png`)}`, `file://${page}`]);
}
await rm(work, { recursive: true, force: true });
console.log('share cards: public/share/en.png, public/share/zh.png');
