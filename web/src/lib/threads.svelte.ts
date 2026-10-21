import { timestampMs } from '@bufbuild/protobuf/wkt';
import { api, userApi } from './api';
import { Status, type Thread, type User } from './gen/banda/v1/banda_pb';

// `search` and `project` (a repo URL) filter the sidebar's threads.
export const threads = $state({
	list: [] as Thread[],
	mentioned: [] as Thread[],
	users: [] as User[],
	me: undefined as User | undefined,
	search: '',
	project: ''
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

// Precedence: settled > snoozed > status.
export const sectionOf = (t: Thread) =>
	t.settled
		? 'settled'
		: t.snoozedUntil && timestampMs(t.snoozedUntil) > Date.now()
			? 'snoozed'
			: t.status === Status.STARTING || t.status === Status.WORKING
				? 'working'
				: 'needs';

export function sidebarSections() {
	const s: Record<string, Thread[]> = { needs: [], working: [], snoozed: [], settled: [] };
	for (const t of threads.list.filter(matches)) s[sectionOf(t)].push(t);
	return s;
}

// The sidebar's threads top to bottom, for j/k.
export const sidebarOrder = () => [threads.mentioned.filter(matches), ...Object.values(sidebarSections())].flat();
