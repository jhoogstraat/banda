// Run: node src/lib/inbox.check.ts
import { alerts, families, type Inbox } from './inbox.ts';
import type { Thread } from './gen/banda/v1/banda_pb.ts';

const Status = { WORKING: 2, AWAITING_INPUT: 3, ERRORED: 4 };

const t = (id: string, status: number, more = {}) => ({ id, status, ownerId: 'bob', ...more }) as Thread;
const titles = (prev: Inbox, next: Inbox) => alerts(prev, next).map((a) => `${a.thread.id}: ${a.title}`);
const eq = (got: string[], want: string[], msg: string) => {
	if (JSON.stringify(got) !== JSON.stringify(want)) throw new Error(`${msg}: got ${JSON.stringify(got)}`);
};

const working = { threads: [t('a', Status.WORKING), t('b', Status.WORKING)], mentioned: [] };
eq(titles(working, working), [], 'no change, no alert');
eq(
	titles(working, { threads: [t('a', Status.AWAITING_INPUT), t('b', Status.ERRORED)], mentioned: [] }),
	['a: Your agent needs input', 'b: Your thread failed'],
	'turn ended and error alert'
);
eq(
	titles(working, { threads: [t('a', Status.AWAITING_INPUT, { settled: true }), t('b', Status.WORKING)], mentioned: [] }),
	[],
	'settled threads stay quiet'
);
eq(
	titles({ threads: [t('a', Status.AWAITING_INPUT)], mentioned: [] }, { threads: [t('a', Status.AWAITING_INPUT)], mentioned: [] }),
	[],
	'still awaiting input is not news'
);
eq(
	titles({ threads: [], mentioned: [t('m', Status.WORKING)] }, { threads: [], mentioned: [t('m', Status.WORKING), t('n', Status.WORKING)] }),
	["n: You were mentioned in bob's thread"],
	'only new mentions alert'
);
const asking = (n: number, status = Status.WORKING) => ({ threads: [t('a', status, { turnRequests: n })], mentioned: [] });
eq(titles(asking(0), asking(1)), ['a: A teammate asks to run your agent'], 'a new turn request alerts, even while working');
eq(titles(asking(1), asking(1)), [], 'a pending request is not news');
eq(titles(asking(2), asking(1)), [], 'a resolved request is quiet');
const perm = (pending: boolean) => ({ threads: [t('a', Status.WORKING, { permissionPending: pending })], mentioned: [] });
eq(titles(perm(false), perm(true)), ['a: Your agent asks for permission'], 'a permission request alerts while working');
eq(titles(perm(true), perm(true)), [], 'a pending permission is not news');
eq(titles(perm(true), perm(false)), [], 'an answered permission is quiet');
const fam = families([
	t('p', Status.WORKING),
	t('c1', Status.WORKING, { parentId: 'p' }),
	t('c2', Status.AWAITING_INPUT, { parentId: 'p' }),
	t('g', Status.WORKING, { parentId: 'c1' }),
	t('orphan', Status.WORKING, { parentId: 'gone' }),
	t('s', Status.AWAITING_INPUT, { settled: true })
]);
eq(fam.needs.map((f) => f.thread.id), ['p'], 'a sub-thread needing input pulls its family into Needs input');
eq(fam.needs[0].children.map((f) => f.thread.id), ['c1', 'c2'], 'sub-threads nest under their parent');
eq(fam.needs[0].children[0].children.map((f) => f.thread.id), ['g'], 'sub-sub-threads nest too');
eq(fam.working.map((f) => f.thread.id), ['orphan'], "a thread whose parent isn't listed is a root");
eq(fam.settled.map((f) => f.thread.id), ['s'], 'others keep their own section');
const asks = families([t('q', Status.WORKING), t('q1', Status.WORKING, { parentId: 'q', permissionPending: true }), t('q2', Status.WORKING, { parentId: 'q', turnRequests: 1 })]);
eq(asks.needs.map((f) => f.thread.id), ['q'], "a sub-thread's permission request or turn request pulls its family into Needs input");
console.log('inbox ok');
