import { timestampMs } from '@bufbuild/protobuf/wkt';
import { Status, type Thread } from './gen/banda/v1/banda_pb.js';

export type Inbox = { threads: Thread[]; mentioned: Thread[]; loginNeeded?: boolean };

// A finished thread stays in Ready this long, or while it is unread; then it sinks to Earlier.
export const readyFor = 3 * 24 * 3600_000;

// What the thread waits on from people: its permission requests ("plan" for a plan) and teammates' turn requests.
export const asks = (t: Thread) => [
	...t.permissionTools.map((n) => ({ ExitPlanMode: 'plan', AskUserQuestion: 'a question' })[n] ?? n),
	...Array<string>(t.turnRequests).fill('turn request')
];
// "Bash", "plan", "2 requests".
export const asking = (list: string[]) => (list.length === 1 ? list[0] : `${list.length} requests`);

// The sidebar section of one of my threads. Precedence: settled > snoozed > what blocks on me (a permission or
// plan request, a teammate's turn request, an error) > running > finished: Ready, or Earlier once it is old and read.
export const sectionOf = (t: Thread, now = Date.now()) =>
	t.settled
		? 'settled'
		: t.snoozedUntil && timestampMs(t.snoozedUntil) > now
			? 'snoozed'
			: t.permissionPending || t.turnRequests || t.status === Status.ERRORED
				? 'needs'
				: t.status === Status.STARTING || t.status === Status.WORKING
					? 'working'
					: t.unread || !t.updatedAt || now - timestampMs(t.updatedAt) < readyFor
						? 'ready'
						: 'earlier';

// A sidebar thread with its sub-threads nested under it. A race's tries are one family: `race` is its id,
// `children` the tries, `thread` the most urgent one.
export type Family = { thread: Thread; children: Family[]; race?: string };

export const size = (f: Family): number => (f.race ? 0 : 1) + f.children.reduce((n, c) => n + size(c), 0);

export const urgency = ['needs', 'ready', 'working', 'earlier', 'snoozed', 'settled'];

// My threads as families, by section. A family sits in the most urgent section of any member, so a
// sub-thread that needs me surfaces with its parent. A thread whose parent isn't listed is a root.
export function families(list: Thread[], now = Date.now()) {
	const byId = new Map(list.map((t) => [t.id, { thread: t, children: [] } as Family]));
	let roots: Family[] = [];
	for (const f of byId.values()) {
		const parent = byId.get(f.thread.parentId);
		if (parent && parent !== f) parent.children.push(f);
		else roots.push(f);
	}
	const rank = (f: Family): number => Math.min(urgency.indexOf(sectionOf(f.thread, now)), ...f.children.map(rank));
	// The tries of a race (2 or more listed) become one family where the first of them was.
	const tries = new Map<string, Family[]>();
	for (const f of roots) tries.set(f.thread.raceId, [...(tries.get(f.thread.raceId) ?? []), f]);
	roots = roots.flatMap((f) => {
		const all = tries.get(f.thread.raceId)!;
		if (!f.thread.raceId || all.length < 2) return [f];
		if (all[0] !== f) return [];
		const lead = all.reduce((a, b) => (rank(b) < rank(a) ? b : a));
		return [{ thread: lead.thread, children: all, race: f.thread.raceId }];
	});
	const out: Record<string, Family[]> = Object.fromEntries(urgency.map((s) => [s, []]));
	for (const f of roots) out[urgency[rank(f)]].push(f);
	return out;
}

// "Fix the login (2/3)" -> "Fix the login".
export const raceTitle = (t: Thread) => t.title.replace(/ \(\d+\/\d+\)$/, '');

export type Alert = { href: string; title: string; body: string };

// What deserves a notification between two inbox snapshots, for my threads unless snoozed or settled: a new
// teammate turn request, the agent asking for permission or a plan approval, a turn finished, a failure; also a
// new mention and a login needed.
export function alerts(prev: Inbox, next: Inbox, name: (id: string) => string = (id) => id): Alert[] {
	const was = new Map(prev.threads.map((t) => [t.id, t]));
	const out: Alert[] = [];
	const push = (t: Thread, title: string) => out.push({ href: `/t/${t.id}`, title, body: t.title });
	for (const t of next.threads) {
		if (['snoozed', 'settled'].includes(sectionOf(t))) continue;
		const p = was.get(t.id);
		if (t.turnRequests > (p?.turnRequests ?? 0)) push(t, 'A teammate asks to run your agent');
		const fresh = t.permissionTools.filter((n) => !p?.permissionTools.includes(n));
		if (fresh.includes('ExitPlanMode')) push(t, 'Your agent has a plan for you to approve');
		else if (fresh.includes('AskUserQuestion')) push(t, 'Your agent has a question');
		else if (fresh.length) push(t, `Your agent asks to use ${fresh[0]}`);
		if (p?.status === t.status) continue;
		if (t.status === Status.AWAITING_INPUT && !t.turnRequests) push(t, 'Your agent finished');
		if (t.status === Status.ERRORED) push(t, 'Your thread failed');
	}
	const mentioned = new Set(prev.mentioned.map((t) => t.id));
	for (const t of next.mentioned) {
		if (!mentioned.has(t.id)) push(t, `You were mentioned in ${name(t.ownerId)}'s thread`);
	}
	if (next.loginNeeded && !prev.loginNeeded)
		out.push({ href: '/settings', title: 'Log in to Claude', body: 'Your agents can’t run until you log in again.' });
	return out;
}
