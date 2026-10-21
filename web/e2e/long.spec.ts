// Long threads: a seeded 1200-event thread opens with a tail; older pages and full payloads load on request.
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { DB, expect, listen, test } from './fixtures.ts';

test('long threads: a tail replays the last events with the plan from before it and cut outputs; older pages and full events load', async ({ as }) => {
	const alice = as('alice');
	const { stdout } = await promisify(execFile)('../deploy/long-thread.sh', ['1200'], { env: { ...process.env, DB, OWNER: alice.id } });
	const threadId = stdout.trim();

	const tail = listen((signal) => alice.threads.watchThread({ threadId, tail: 200 }, { signal }));
	// Stored events only: the stream also says who is here (seq 0).
	const stored = () => tail.got.map((r) => r.event!).filter((e) => e.seq > 0n);
	await tail.until(() => stored().filter((e) => !e.context).length >= 200, 'the tail');
	tail.stop();
	const events = stored();
	const replayed = events.filter((e) => !e.context);
	expect(replayed).toHaveLength(200);
	expect(replayed[0].seq).toBe(1001n);
	const plan = events.find((e) => e.context);
	expect(plan?.kind.case === 'toolCall' && plan.kind.value.name).toBe('TodoWrite');
	expect(plan!.seq).toBeLessThan(1001n);
	const cut = events.find((e) => e.truncated);
	expect(cut?.kind.case === 'toolResult' && cut.kind.value.output.length).toBeLessThanOrEqual(4096);

	const older = (await alice.threads.history({ threadId, beforeSeq: 1001n, limit: 500 })).events;
	expect([older.length, older[0].seq, older.at(-1)!.seq]).toEqual([500, 501n, 1000n]);
	const [full] = (await alice.threads.history({ threadId, beforeSeq: 1056n, limit: 1, full: true })).events;
	expect(full.seq).toBe(1055n);
	expect(full.truncated).toBe(false);
	expect(full.kind.case === 'toolResult' && full.kind.value.output.length).toBeGreaterThan(4096);
});
