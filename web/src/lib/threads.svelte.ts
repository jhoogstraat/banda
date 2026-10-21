import { goto } from '$app/navigation';
import { page } from '$app/state';
import { api, userApi } from './api';
import type { Thread, User } from './gen/banda/v1/banda_pb.js';
import { alerts, families, sectionOf, type Family, type Inbox } from './inbox';

// `search` and `project` (a repo URL) filter the sidebar's threads.
export const threads = $state({
	list: [] as Thread[],
	mentioned: [] as Thread[],
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

// Unread threads that want me: Mentioned and Needs input. Shown in the tab title.
export const attention = () =>
	[...threads.mentioned, ...threads.list.filter((t) => sectionOf(t, threads.now) === 'needs')].filter(unread).length;

// Keeps the sidebar live: InboxWatch sends it whenever it changes. Reconnects
// keep the last snapshot, so what changed while offline still notifies.
export async function watchInbox(signal: AbortSignal) {
	let prev: Inbox | undefined;
	while (!signal.aborted) {
		try {
			for await (const res of api.inboxWatch({}, { signal })) {
				if (prev) alerts(prev, res, userName).forEach(({ thread, title }) => notify(thread, title));
				prev = res;
				Object.assign(threads, { list: res.threads, mentioned: res.mentioned });
			}
		} catch {
			if (signal.aborted) return;
		}
		await new Promise((r) => setTimeout(r, 1000));
	}
}

function notify(t: Thread, title: string) {
	if (threads.notify !== 'granted' || (!document.hidden && page.params.id === t.id)) return;
	const n = new Notification(title, { body: t.title, tag: t.id });
	n.onclick = () => {
		window.focus();
		goto(`/t/${t.id}`);
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

// Unfolded by the user, or by default while one of its sub-threads is on screen.
export const unfolded = (f: Family) => threads.expanded[f.thread.id] ?? contains(f, page.params.id);

// A family's threads as the sidebar shows them.
export const visible = (f: Family): Thread[] => [f.thread, ...(unfolded(f) ? f.children.flatMap(visible) : [])];

// The sidebar's threads top to bottom, for j/k.
export const sidebarOrder = () => [threads.mentioned.filter(matches), ...Object.values(sidebarSections()).flat().map(visible)].flat();
