// The Kubai Icons the site uses, by name. The server renders their markup
// and the browser imports the same ones to animate them, so only these are
// bundled.
import {
	AppWindow,
	Code,
	Cpu,
	Film,
	Globe,
	Languages,
	Laptop,
	Layers,
	Monitor,
	Moon,
	Package,
	Signal,
	SkipForward,
	Smartphone,
	Speaker,
	Subtitles,
	Sun,
	Terminal,
	type KubaiIconData,
} from 'kubai-icons';

export const icons = {
	'app-window': AppWindow,
	code: Code,
	cpu: Cpu,
	film: Film,
	globe: Globe,
	languages: Languages,
	laptop: Laptop,
	layers: Layers,
	monitor: Monitor,
	moon: Moon,
	package: Package,
	signal: Signal,
	'skip-forward': SkipForward,
	smartphone: Smartphone,
	speaker: Speaker,
	subtitles: Subtitles,
	sun: Sun,
	terminal: Terminal,
} satisfies Record<string, KubaiIconData>;

export type IconName = keyof typeof icons;
