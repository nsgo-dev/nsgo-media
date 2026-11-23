// The demo plugin answers every export of the plugin contract with made-up
// videos, and uses each host function, so the runtime's tests and plugin
// authors have one complete example. It fetches nothing, but lists its
// site's host, as the app fetches its pictures and streams only from there.

type Kind = "video" | "uploader" | "collection" | "link";

interface Source {
  url: string;
  headers?: Record<string, string>;
}

interface Item {
  kind: Kind;
  id: string;
  title: string;
  subtitle?: string;
  image?: Source;
  duration?: number;
}

interface Section {
  title?: string;
  items: Item[];
  more?: string;
}

interface Page {
  title?: string;
  sections: Section[];
  next?: string;
}

interface Account {
  id: string;
  name: string;
  avatar?: Source;
}

declare const host: {
  language: string;
  t(text: string): string;
  md5(text: string): string;
  storage: {
    get(key: string): unknown;
    set(key: string, value: unknown): void;
    remove(key: string): void;
  };
  settings: { get(key: string): string | boolean };
  showSignIn(prompt: { qrCode?: string; message?: string }): Promise<boolean>;
  closeSignIn(): void;
  openURL(url: string): void;
};

const site = "https://demo.example.com";
const headers = { Referer: site + "/" };

const uploaders = [
  { id: "u1", name: "Hill Walker" },
  { id: "u2", name: "Night Cook" },
];

const videos = [
  { id: "v1", title: "Sunrise over the hills", uploader: "u1", parts: ["Climb", "Summit"], duration: 95 },
  { id: "v2", title: "Ten-minute noodles", uploader: "u2", parts: ["Cooking"], duration: 610 },
  { id: "v3", title: "Fog in the valley", uploader: "u1", parts: ["Morning"], duration: 240 },
];

const collections = [{ id: "c1", title: "Walks", videos: ["v1", "v3"] }];

// fail throws an error whose code tells the app what to do.
function fail(code: string, message: string): never {
  const err = new Error(host.t(message)) as Error & { code: string };
  err.code = code;
  throw err;
}

function videoItem(id: string): Item {
  const v = videos.find((v) => v.id === id)!;
  const u = uploaders.find((u) => u.id === v.uploader)!;
  return {
    kind: "video",
    id: v.id,
    title: v.title,
    subtitle: u.name,
    image: { url: `${site}/covers/${v.id}.jpg`, headers },
    duration: v.duration,
  };
}

function uploaderItem(id: string): Item {
  const u = uploaders.find((u) => u.id === id)!;
  return { kind: "uploader", id: u.id, title: u.name, image: { url: `${site}/avatars/${u.id}.png` } };
}

function collectionItem(id: string): Item {
  const c = collections.find((c) => c.id === id)!;
  return { kind: "collection", id: c.id, title: c.title };
}

function signedIn(): Account | null {
  return (host.storage.get("account") as Account | undefined) ?? null;
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

export function account(): Account | null {
  return signedIn();
}

// signIn shows a QR code and signs in once it is "scanned", a moment later,
// unless the viewer dismisses the prompt first.
export async function signIn(): Promise<Account> {
  if (host.settings.get("browserSignIn")) {
    host.openURL(`${site}/sign-in`);
  }
  const dismissed = host.showSignIn({ qrCode: `${site}/sign-in?code=demo`, message: host.t("Scan the code to sign in to Demo") });
  const scanned = sleep(20).then(() => false);
  if (await Promise.race([dismissed, scanned])) {
    throw new Error(host.t("Sign-in was cancelled"));
  }
  host.closeSignIn();
  const acc: Account = { id: "demo", name: "Demo Viewer", avatar: { url: `${site}/avatars/demo.png` } };
  host.storage.set("account", acc);
  return acc;
}

export function signOut(): void {
  host.storage.remove("account");
}

export async function home({ cursor }: { cursor?: string }): Promise<Page> {
  await sleep(1);
  if (cursor === "2") {
    return { sections: [{ title: host.t("Collections"), items: collections.map((c) => collectionItem(c.id)) }] };
  }
  return {
    sections: [
      { title: host.t("Popular"), items: videos.map((v) => videoItem(v.id)), more: "popular" },
      { title: host.t("Uploaders"), items: uploaders.map((u) => uploaderItem(u.id)) },
      { items: [{ kind: "link", id: "favorites", title: host.t("Favorites") }] },
    ],
    next: "2",
  };
}

export function browse({ id }: { id: string }): Page {
  if (id === "popular") {
    return { title: host.t("Popular"), sections: [{ items: videos.map((v) => videoItem(v.id)) }] };
  }
  if (id === "favorites") {
    if (!signedIn()) fail("signIn", "Sign in to see your favorites");
    return { title: host.t("Favorites"), sections: [{ items: [videoItem("v2")] }] };
  }
  const u = uploaders.find((u) => u.id === id);
  if (u) {
    const items = videos.filter((v) => v.uploader === u.id).map((v) => videoItem(v.id));
    return { title: u.name, sections: [{ items }] };
  }
  const c = collections.find((c) => c.id === id);
  if (c) {
    return { title: c.title, sections: [{ items: c.videos.map((v) => videoItem(v)) }] };
  }
  throw new Error(`No page ${id}`);
}

export function search({ query, kind }: { query: string; kind?: Kind }): Page {
  const q = query.toLowerCase();
  const sections: Section[] = [];
  if (!kind || kind === "video") {
    const items = videos.filter((v) => v.title.toLowerCase().includes(q)).map((v) => videoItem(v.id));
    sections.push({ title: host.t("Videos"), items });
  }
  if (!kind || kind === "uploader") {
    const items = uploaders.filter((u) => u.name.toLowerCase().includes(q)).map((u) => uploaderItem(u.id));
    sections.push({ title: host.t("Uploaders"), items });
  }
  return { sections: sections.filter((s) => s.items.length > 0) };
}

export function suggest({ query }: { query: string }): string[] {
  const q = query.toLowerCase();
  return videos.map((v) => v.title).filter((t) => t.toLowerCase().startsWith(q));
}

// resolve takes a video's link, as https://demo.example.com/video/v1, or its
// bare id. A link's ?part=2 makes the video's id "v1?part=2", which starts
// playing at that part.
export function resolve({ text }: { text: string }): Item | null {
  let id = text.trim();
  let part = "";
  if (id.startsWith("http")) {
    const url = new URL(id);
    if (url.hostname !== "demo.example.com") return null;
    id = url.pathname.split("/").pop() ?? "";
    part = url.searchParams.get("part") ?? "";
  }
  if (!videos.some((v) => v.id === id)) return null;
  return { ...videoItem(id), id: part ? `${id}?part=${part}` : id };
}

export function video({ id: asked }: { id: string }) {
  const [id, part] = asked.split("?part=");
  if (id === "gone") fail("unavailable", "This video was removed");
  const v = videos.find((v) => v.id === id);
  if (!v) throw new Error(`No video ${id}`);
  return {
    id: v.id,
    title: v.title,
    description: `A made-up video by ${uploaders.find((u) => u.id === v.uploader)!.name}.`,
    image: { url: `${site}/covers/${v.id}.jpg`, headers },
    uploader: uploaderItem(v.uploader),
    published: "2026-11-01T08:00:00Z",
    duration: v.duration,
    parts: v.parts.map((title, i) => ({ id: `${i + 1}`, title, duration: v.duration / v.parts.length })),
    part,
    related: videos.filter((r) => r.id !== v.id).map((r) => videoItem(r.id)),
  };
}

const qualities = [
  { id: "1080", label: "1080p" },
  { id: "720", label: "720p" },
  { id: "480", label: "480p" },
];

// stream answers separate video and audio behind a Referer, as DASH sites
// do, with URLs signed by an MD5 hash.
export function stream({ video, part, quality }: { video: string; part: string; quality?: string }) {
  const q = quality || (host.settings.get("quality") as string);
  if (q === "1080" && !host.settings.get("apiKey")) {
    fail("setup", "1080p needs an API key in the settings");
  }
  const sign = host.md5(`${video}/${part}/${q}`);
  return {
    video: { url: `${site}/media/${video}/${part}/${q}.m4s?sign=${sign}`, headers },
    audio: { url: `${site}/media/${video}/${part}/audio.m4s?sign=${sign}`, headers },
    quality: q,
    qualities,
    subtitles: [
      { label: "English", language: "en", url: `${site}/subtitles/${video}/${part}/en.vtt` },
      { label: "中文", language: "zh-CN", url: `${site}/subtitles/${video}/${part}/zh-CN.vtt` },
      // Subtitles the plugin made itself, as from a site's own format.
      { label: "Notes", text: "WEBVTT\n\n00:00.000 --> 00:02.000\nMade up\n" },
    ],
  };
}

export function progress({ video, part, position }: { video: string; part: string; position: number }): void {
  host.storage.set(`progress:${video}`, { part, position });
}

export function setSetting({ key, value }: { key: string; value: unknown }): void {
  console.log("setting changed", key);
  host.storage.set("changed", { key, value });
}
