import { api, userApi } from './api';
import type { Thread, User } from './gen/banda/v1/banda_pb';

// `viewing` is the user whose threads the sidebar lists ('' = me); `search` and `project` (a repo URL) filter it.
export const threads = $state({
	list: [] as Thread[],
	mentioned: [] as Thread[],
	users: [] as User[],
	me: undefined as User | undefined,
	viewing: '',
	mentionsOnly: false,
	search: '',
	project: ''
});

export async function loadThreads() {
	const owner = threads.viewing;
	const [mine, mentioned, { users }, { user }] = await Promise.all([
		api.listThreads({ ownerId: owner }),
		api.listMentioned({}),
		userApi.listUsers({}),
		userApi.me({})
	]);
	if (owner === threads.viewing) threads.list = mine.threads; // the view may have switched meanwhile
	Object.assign(threads, { mentioned: mentioned.threads, users, me: user });
}

export function view(owner: string, mentionsOnly = false) {
	if (owner !== threads.viewing) threads.list = [];
	Object.assign(threads, { viewing: owner, mentionsOnly });
	loadThreads().catch(() => {});
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
