// Attachments: uploads, how they are served, and how they reach the agent.
import { API, expect, png, refused, Status, test } from './fixtures.ts';

test('attachments: a chat message’s image is folded into the next prompt as an image block, an approved request’s text file is inlined; older pages keep them', async ({ as }) => {
	const bob = as('bob');
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const bobs = t.as(bob);
	await bobs.send('nice, carol what do you think?', { attachments: [{ id: await bob.attach('dot.png', 'image/png', png) }] });
	await bobs.send('@agent one more', { mentionsAgent: true, attachments: [{ id: await bob.attach('notes.md', '', '# Notes\nuse ``` fences\n') }] });
	await t.waitFor('a turn request', (i) => i.turnRequests === 1);
	const seq = (await t.events()).filter((e) => e.kind.case === 'turnRequest').at(-1)!.seq;
	await t.turn(() => t.user.threads.approveTurn({ threadId: t.id, requestSeq: seq }));
	expect((await t.of('agentText')).map((a) => a.text)).toContain('Received 1 image blocks (image/png) and 1 text files');
	expect((await t.of('message')).find((m) => m.attachments.length)?.attachments[0].name).toBe('dot.png');
	const { events } = await t.user.threads.history({ threadId: t.id, beforeSeq: 1000000n, limit: 500 });
	expect(events.flatMap((e) => (e.kind.case === 'message' ? e.kind.value.attachments.map((a) => a.name) : []))).toContain('dot.png');
});

test('files are served as uploaded, sandboxed; binaries, files over 5 MB and other people’s uploads are refused', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const pngId = await bob.attach('dot.png', 'image/png', png);
	const txtId = await bob.attach('notes.md', '', '# Notes\nuse ``` fences\n');
	const got = await fetch(`${API}/files/${pngId}`, { headers: { 'X-Banda-User': alice.id } });
	expect(Buffer.from(await got.arrayBuffer())).toEqual(png);
	expect(got.headers.get('content-type')).toBe('image/png');
	expect(got.headers.get('x-content-type-options')).toBe('nosniff');
	expect(got.headers.get('content-security-policy')).toMatch(/^default-src 'none'; sandbox/);
	expect(await (await fetch(`${API}/files/${txtId}`, { headers: { 'X-Banda-User': alice.id } })).text()).toContain('use ``` fences');
	expect((await alice.upload('blob.bin', 'application/octet-stream', new Uint8Array([0, 1, 2, 98, 105, 110]))).status).toBe(415);
	expect((await alice.upload('big.png', 'image/png', new Uint8Array(5 * 1024 * 1024 + 1))).status).toBe(413);
	const t = await alice.create();
	await refused(t.send('mine now', { attachments: [{ id: pngId }] }));
});
