import { goto } from '$app/navigation';
import { page } from '$app/state';
import { api, userApi } from './api';
import type { Decision, Thread, User } from './gen/banda/v1/banda_pb.js';
import { alerts, asking, asks, families, sectionOf, type Alert, type Family, type Inbox } from './inbox';

// `search` and `project` (a repo URL) filter the sidebar's threads.
export const threads = $state({
	list: [] as Thread[],
	mentioned: [] as Thread[],
	// What I can decide now (InboxWatch), oldest first.
	decisions: [] as Decision[],
	loginNeeded: false,
	users: [] as User[],
	me: undefined as User | undefined,
	search: '',
	project: '',
	// Sidebar families the user unfolded, by parent id.
	expanded: {} as Record<string, boolean>,
	// Ticks every 30 s so ages and snooze ends stay current.
	now: Date.now(),
	// Browser notification permission; 'default' until the user answers.
	notify: typeof Notification === 'undefined' ? 'denied' : Notification.permission
});

export async function loadThreads() {
	const [mine, mentioned, { users }, { user }] = await Promise.all([
		api.listThreads({ ownerId: '' }),
		api.listMentioned({}),
		userApi.listUsers({}),
		userApi.me({})
	]);
	Object.assign(threads, { list: mine.threads, mentioned: mentioned.threads, users, me: user });
}

// The thread on screen counts as read: the page marks it read as events arrive.
export const unread = (t: Thread) => t.unread && page.params.id !== t.id;

// What wants me, shown in the tab title: every thread in Needs input, a login needed, and unread Mentioned and Ready threads.
export const attention = () =>
	threads.list.filter((t) => sectionOf(t, threads.now) === 'needs').length +
	Number(threads.loginNeeded) +
	[...threads.mentioned, ...threads.list.filter((t) => sectionOf(t, threads.now) === 'ready')].filter(unread).length;

// "Waiting for you · Bash" while the thread waits on a person's answer: me if I can give it (my thread, or a
// trusted one with a request other than a plan), else its owner. '' when it waits on nobody.
export function waiting(t: Thread) {
	const list = asks(t);
	if (!list.length) return '';
	const me = t.ownerId === threads.me?.id || (t.trusted && t.permissionTools.some((n) => n !== 'ExitPlanMode'));
	return `Waiting for ${me ? 'you' : userName(t.ownerId)} · ${asking(list)}`;
}

// Keeps the sidebar live: InboxWatch sends it whenever it changes. Reconnects
// keep the last snapshot, so what changed while offline still notifies.
export async function watchInbox(signal: AbortSignal) {
	let prev: Inbox | undefined;
	while (!signal.aborted) {
		try {
			for await (const res of api.inboxWatch({}, { signal })) {
				if (prev) alerts(prev, res, userName).forEach(notify);
				prev = res;
				Object.assign(threads, { list: res.threads, mentioned: res.mentioned, decisions: res.decisions, loginNeeded: res.loginNeeded });
			}
		} catch {
			if (signal.aborted) return;
		}
		await new Promise((r) => setTimeout(r, 1000));
	}
}

function notify({ href, title, body }: Alert) {
	if (threads.notify !== 'granted' || (!document.hidden && page.url.pathname === href)) return;
	const n = new Notification(title, { body, tag: href });
	n.onclick = () => {
		window.focus();
		goto(href);
		n.close();
	};
}

export const askNotify = async () => (threads.notify = await Notification.requestPermission());

// Actors are user ids, or "agent:<threadId>" for other agents posting through MCP.
export function userName(id: string) {
	return id.startsWith('agent:')
		? `Agent · ${id.slice(6, 12)}`
		: (threads.users.find((u) => u.id === id)?.name ?? id);
}

// "github.com/org/repo.git" -> "org/repo".
export const projectName = (url: string) =>
	url
		.replace(/\.git$/, '')
		.split('/')
		.slice(-2)
		.join('/');

// The repositories of all loaded threads, most recently active first.
export const projects = () => [...new Set([...threads.list, ...threads.mentioned].map((t) => t.repoUrl))];

export function matches(t: Thread) {
	const q = threads.search.trim().toLowerCase();
	return (
		(!threads.project || t.repoUrl === threads.project) &&
		(!q || `${t.title} ${projectName(t.repoUrl)}`.toLowerCase().includes(q))
	);
}

export const sidebarSections = () => families(threads.list.filter(matches), threads.now);

const contains = (f: Family, id?: string): boolean => f.children.some((c) => c.thread.id === id || contains(c, id));

// Unfolded by the user, or by default while one of its sub-threads (or a race's tries) is on screen.
export const unfolded = (f: Family) => threads.expanded[f.race ?? f.thread.id] ?? contains(f, page.params.id);

// A family's threads as the sidebar shows them: a folded race by its row's try.
export const visible = (f: Family): Thread[] =>
	f.race && unfolded(f) ? f.children.flatMap(visible) : [f.thread, ...(unfolded(f) ? f.children.flatMap(visible) : [])];

// The sidebar's threads top to bottom, for j/k.
export const sidebarOrder = () => [threads.mentioned.filter(matches), ...Object.values(sidebarSections()).flat().map(visible)].flat();
