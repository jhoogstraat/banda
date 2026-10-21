import { timestampMs } from '@bufbuild/protobuf/wkt';
import type { Thread } from './gen/banda/v1/banda_pb.ts';

// Status values: node's type stripping (inbox.check.ts) can't load the generated enum.
const STARTING = 1, WORKING = 2, AWAITING_INPUT = 3, ERRORED = 4;

export type Inbox = { threads: Thread[]; mentioned: Thread[] };

// The sidebar section of one of my threads. Precedence: settled > snoozed > teammates' turn requests and the
// agent asking for permission > status.
export const sectionOf = (t: Thread, now = Date.now()) =>
	t.settled
		? 'settled'
		: t.snoozedUntil && timestampMs(t.snoozedUntil) > now
			? 'snoozed'
			: !t.turnRequests && !t.permissionPending && (t.status === STARTING || t.status === WORKING)
				? 'working'
				: 'needs';

// What deserves a notification between two inbox snapshots: my thread newly
// needing input or failing (unless snoozed or settled), a new teammate turn request, the agent asking for
// permission, or a new mention.
export function alerts(prev: Inbox, next: Inbox, name: (id: string) => string = (id) => id) {
	const was = new Map(prev.threads.map((t) => [t.id, t.status]));
	const asks = new Map(prev.threads.map((t) => [t.id, t.turnRequests]));
	const asking = new Set(prev.threads.filter((t) => t.permissionPending).map((t) => t.id));
	const out: { thread: Thread; title: string }[] = [];
	for (const t of next.threads) {
		if (sectionOf(t) !== 'needs') continue;
		if (t.turnRequests > (asks.get(t.id) ?? 0)) out.push({ thread: t, title: 'A teammate asks to run your agent' });
		if (t.permissionPending && !asking.has(t.id)) out.push({ thread: t, title: 'Your agent asks for permission' });
		if (was.get(t.id) === t.status) continue;
		if (t.status === AWAITING_INPUT) out.push({ thread: t, title: 'Your agent needs input' });
		if (t.status === ERRORED) out.push({ thread: t, title: 'Your thread failed' });
	}
	const mentioned = new Set(prev.mentioned.map((t) => t.id));
	for (const t of next.mentioned) {
		if (!mentioned.has(t.id)) out.push({ thread: t, title: `You were mentioned in ${name(t.ownerId)}'s thread` });
	}
	return out;
}
