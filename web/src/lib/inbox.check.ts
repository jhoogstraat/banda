// Run: node src/lib/inbox.check.ts
import { timestampFromMs } from '@bufbuild/protobuf/wkt';
import { alerts, asking, asks, families, raceTitle, sectionOf, size, type Inbox } from './inbox.ts';
import { Status, type Thread } from './gen/banda/v1/banda_pb.js';

const now = Date.now();
const t = (id: string, status: Status, more = {}) =>
	({ id, status, ownerId: 'bob', title: id, permissionTools: [], turnRequests: 0, updatedAt: timestampFromMs(now), ...more }) as unknown as Thread;
const titles = (prev: Inbox, next: Inbox) => alerts(prev, next).map((a) => `${a.href}: ${a.title}`);
const eq = (got: unknown, want: unknown, msg: string) => {
	if (JSON.stringify(got) !== JSON.stringify(want)) throw new Error(`${msg}: got ${JSON.stringify(got)}`);
};

const working = { threads: [t('a', Status.WORKING), t('b', Status.WORKING)], mentioned: [] };
eq(titles(working, working), [], 'no change, no alert');
eq(
	titles(working, { threads: [t('a', Status.AWAITING_INPUT), t('b', Status.ERRORED)], mentioned: [] }),
	['/t/a: Your agent finished', '/t/b: Your thread failed'],
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
	["/t/n: You were mentioned in bob's thread"],
	'only new mentions alert'
);
const requests = (n: number, status = Status.WORKING) => ({ threads: [t('a', status, { turnRequests: n })], mentioned: [] });
eq(titles(requests(0), requests(1)), ['/t/a: A teammate asks to run your agent'], 'a new turn request alerts, even while working');
eq(titles(requests(1), requests(1)), [], 'a pending request is not news');
eq(titles(requests(2), requests(1)), [], 'a resolved request is quiet');
const perm = (...tools: string[]) => ({ threads: [t('a', Status.WORKING, { permissionPending: tools.length > 0, permissionTools: tools })], mentioned: [] });
eq(titles(perm(), perm('Bash')), ['/t/a: Your agent asks to use Bash'], 'a permission request alerts while working');
eq(titles(perm(), perm('ExitPlanMode')), ['/t/a: Your agent has a plan for you to approve'], 'a plan alerts');
eq(titles(perm('Bash'), perm('Bash')), [], 'a pending permission is not news');
eq(titles(perm('Bash'), perm()), [], 'an answered permission is quiet');
eq(titles({ threads: [], mentioned: [] }, { threads: [], mentioned: [], loginNeeded: true }), ['/settings: Log in to Claude'], 'a login needed alerts once');
const kinds = (prev: Inbox, next: Inbox) => alerts(prev, next).map((a) => a.kind);
eq(
	[...kinds(perm(), perm('Bash')), ...kinds(requests(0), requests(1)), ...kinds(working, { threads: [t('a', Status.ERRORED)], mentioned: [t('n', Status.WORKING)] })],
	['asks', 'asks', 'done', 'mention'],
	'alerts have the kind their setting turns off'
);

const old = timestampFromMs(now - 4 * 24 * 3600_000);
eq(sectionOf(t('r', Status.AWAITING_INPUT)), 'ready', 'a finished thread is ready');
eq(sectionOf(t('r', Status.SUSPENDED)), 'ready', 'a paused thread with nothing pending is ready');
eq(sectionOf(t('r', Status.SUSPENDED, { updatedAt: old })), 'earlier', 'an old, read one sinks');
eq(sectionOf(t('r', Status.AWAITING_INPUT, { updatedAt: old, unread: true })), 'ready', 'unless unread');
eq(sectionOf(t('r', Status.AWAITING_INPUT, { turnRequests: 1 })), 'needs', 'a turn request needs me');
eq(sectionOf(t('r', Status.WORKING, { permissionPending: true, permissionTools: ['Bash'] })), 'needs', 'a permission request needs me');
eq(sectionOf(t('r', Status.ERRORED)), 'needs', 'an error needs me');

const fam = families([
	t('p', Status.WORKING),
	t('c1', Status.WORKING, { parentId: 'p' }),
	t('c2', Status.ERRORED, { parentId: 'p' }),
	t('g', Status.WORKING, { parentId: 'c1' }),
	t('orphan', Status.WORKING, { parentId: 'gone' }),
	t('s', Status.AWAITING_INPUT, { settled: true })
]);
eq(fam.needs.map((f) => f.thread.id), ['p'], 'a sub-thread needing me pulls its family into Needs input');
eq(fam.needs[0].children.map((f) => f.thread.id), ['c1', 'c2'], 'sub-threads nest under their parent');
eq(fam.needs[0].children[0].children.map((f) => f.thread.id), ['g'], 'sub-sub-threads nest too');
eq(fam.working.map((f) => f.thread.id), ['orphan'], "a thread whose parent isn't listed is a root");
eq(fam.settled.map((f) => f.thread.id), ['s'], 'others keep their own section');
const fam2 = families([t('q', Status.WORKING), t('q1', Status.WORKING, { parentId: 'q', permissionPending: true }), t('q2', Status.WORKING, { parentId: 'q', turnRequests: 1 })]);
eq(fam2.needs.map((f) => f.thread.id), ['q'], "a sub-thread's permission request or turn request pulls its family into Needs input");
eq(families([t('d', Status.WORKING), t('d1', Status.AWAITING_INPUT, { parentId: 'd' })]).ready.map((f) => f.thread.id), ['d'], 'a finished sub-thread makes its family ready');

const race = families([
	t('x', Status.WORKING),
	t('r1', Status.WORKING, { raceId: 'R', title: 'Fix it (1/3)' }),
	t('r2', Status.AWAITING_INPUT, { raceId: 'R', title: 'Fix it (2/3)' }),
	t('r3', Status.WORKING, { raceId: 'R', title: 'Fix it (3/3)' }),
	t('f', Status.WORKING, { raceId: 'F' })
]);
eq(race.ready.map((f) => [f.race, f.thread.id, f.children.map((c) => c.thread.id)]), [['R', 'r2', ['r1', 'r2', 'r3']]], 'a race is one family led by its most urgent try');
eq(race.working.map((f) => f.thread.id), ['x', 'f'], 'a lone fork is a thread of its own');
eq([size(race.ready[0]), raceTitle(race.ready[0].thread)], [3, 'Fix it'], 'a race counts its tries');

eq(asking(asks(t('w', Status.WORKING, { permissionTools: ['Bash'] }))), 'Bash', 'waits on a tool');
eq(asking(asks(t('w', Status.WORKING, { permissionTools: ['ExitPlanMode'] }))), 'plan', 'waits on a plan');
eq(asking(asks(t('w', Status.WORKING, { permissionTools: ['Bash'], turnRequests: 1 }))), '2 requests', 'waits on several');
console.log('inbox ok');
