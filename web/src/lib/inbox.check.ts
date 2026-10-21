// Run: node src/lib/inbox.check.ts
import { alerts, type Inbox } from './inbox.ts';
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
console.log('inbox ok');
