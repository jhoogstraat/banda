// Run: node src/lib/events.check.ts
import { endsAt, locate, parseDiff } from './diff.ts';
import { apply, itemAt, newChat, prepend, typingNow, type Chat } from './events.ts';
import { AutoPushState, Mode, PermissionDecision, SetupState, Status, type Event } from './gen/banda/v1/banda_pb.js';

const ev = (kind: unknown, seq = 1n) => ({ seq, actor: 'agent', kind }) as Event;
const chat = newChat();
const live = () => chat.streaming;
const count = () => chat.items.length;

apply(chat, ev({ case: 'agentTextDelta', value: { text: 'he' } }, 0n));
apply(chat, ev({ case: 'agentTextDelta', value: { text: 'llo' } }, 0n));
if (live() !== 'hello' || chat.items.length) throw new Error('deltas should only fill the live bubble');

apply(chat, ev({ case: 'agentText', value: { text: 'hello!' } }));
if (live() !== '' || chat.items[0].kind !== 'text') throw new Error('agentText should replace the live bubble');

apply(chat, ev({ case: 'toolCall', value: { id: 't1', name: 'Bash', inputJson: '{}' } }));
apply(chat, ev({ case: 'toolResult', value: { toolCallId: 't1', output: 'ok', isError: true } }));
const tool = chat.items[1];
if (tool.kind !== 'steps' || tool.steps[0].kind !== 'tool' || tool.steps[0].result?.output !== 'ok')
	throw new Error('toolResult should attach to its toolCall');
apply(chat, ev({ case: 'notice', value: { text: 'not logged in: run `claude`' } }));
if (chat.items[2]?.kind !== 'notice') throw new Error('notice should become a chat item');

const step = (id: string, state: SetupState, output = '') => ev({ case: 'setupStep', value: { id, title: id, state, output } });
apply(chat, step('checkout', SetupState.RUNNING));
apply(chat, step('tools', SetupState.RUNNING));
apply(chat, step('checkout', SetupState.DONE));
apply(chat, step('tools', SetupState.FAILED, 'mise: boom'));
const setup = chat.items[3];
if (count() !== 4 || setup.kind !== 'setup') throw new Error('setup steps should form one checklist');
if (setup.steps.map((s) => `${s.id}:${s.state}`).join() !== `checkout:${SetupState.DONE},tools:${SetupState.FAILED}` || setup.steps[1].output !== 'mise: boom')
	throw new Error('a setup step should replace the earlier one with its id');
apply(chat, ev({ case: 'notice', value: { text: 'later' } }));
apply(chat, step('tools', SetupState.DONE));
const rerun = chat.items[5];
if (count() !== 6 || setup.steps[1].state !== SetupState.FAILED || rerun.kind !== 'setup' || rerun.steps[0].state !== SetupState.DONE)
	throw new Error('a step after other items should start a new checklist, leaving the old one alone');
apply(chat, step('setup', SetupState.RUNNING));
apply(chat, step('setup', SetupState.FAILED));
apply(chat, step('setup', SetupState.RUNNING)); // retry: only status changes in between, which add no item
if (count() !== 7 || rerun.kind !== 'setup' || rerun.steps[1].state !== SetupState.FAILED)
	throw new Error('a finished step running again should start a new checklist');
apply(chat, { seq: 9n, actor: 'alice', kind: { case: 'agentSettings', value: { model: 'm', effort: 'low' } } } as Event);
const switched = chat.items.at(-1);
if (switched?.kind !== 'settings' || switched.actor !== 'alice' || switched.settings.model !== 'm')
	throw new Error('agentSettings should become a divider naming who switched');
apply(chat, { seq: 9n, actor: 'alice', kind: { case: 'repoAdded', value: { repoUrl: 'https://x/y.git', baseBranch: 'main' } } } as Event);
const added = chat.items.at(-1);
if (added?.kind !== 'repo' || added.actor !== 'alice' || added.repo.repoUrl !== 'https://x/y.git') throw new Error('repoAdded should become a divider naming who added it');

apply(chat, ev({ case: 'turnEnded', value: { durationMs: 1n } }));
apply(chat, ev({ case: 'diffChanged', value: {} }));
const changed = chat.items.at(-1);
if (changed?.kind !== 'turn' || !changed.diff) throw new Error('diffChanged right after a turn (older history) should mark it');
apply(chat, ev({ case: 'agentText', value: { text: 'done' } }));
apply(chat, ev({ case: 'diffChanged', value: {} }));
apply(chat, ev({ case: 'turnEnded', value: { durationMs: 1n } }));
const ahead = chat.items.at(-1);
if (ahead?.kind !== 'turn' || !ahead.diff) throw new Error('diffChanged right before a turn ends should mark that turn');
apply(chat, ev({ case: 'turnEnded', value: { durationMs: 1n } }));
if (chat.items.at(-1)?.kind !== 'turn' || (chat.items.at(-1) as { diff?: boolean }).diff) throw new Error('a turn without a diffChanged should not be marked');
apply(chat, { seq: 10n, actor: 'bob', kind: { case: 'modeChanged', value: { mode: Mode.CHAT, reason: 'posted' } } } as Event);
const mode = chat.items.at(-1);
if (mode?.kind !== 'mode' || mode.actor !== 'bob' || mode.reason !== 'posted') throw new Error('modeChanged should say who switched and why');
apply(chat, ev({ case: 'portOpened', value: { port: 5173, process: 'vite' } }));
const port = chat.items.at(-1);
if (port?.kind !== 'port' || port.port.port !== 5173) throw new Error('portOpened should become a chat item');

// Steps between texts fold into one group; a subagent's calls nest in its Task call; TodoWrite sets the plan.
const plan = (statuses: string[]) =>
	JSON.stringify({ todos: statuses.map((status, i) => ({ content: `todo ${i}`, status, activeForm: `doing ${i}` })) });
const n = count();
apply(chat, ev({ case: 'toolCall', value: { id: 'p1', name: 'TodoWrite', inputJson: plan(['in_progress', 'pending']) } }));
apply(chat, ev({ case: 'agentThinking', value: { text: 'hmm' } }));
apply(chat, ev({ case: 'toolCall', value: { id: 'task', name: 'Task', inputJson: '{"description":"look"}' } }));
apply(chat, ev({ case: 'toolCall', value: { id: 'sub', name: 'Grep', inputJson: '{}', parentToolCallId: 'task' } }));
apply(chat, ev({ case: 'toolResult', value: { toolCallId: 'sub', output: 'a.go' } }));
apply(chat, ev({ case: 'toolCall', value: { id: 'p2', name: 'TodoWrite', inputJson: plan(['completed', 'in_progress']) } }));
const group = chat.items[n];
if (count() !== n + 1 || group.kind !== 'steps' || group.steps.map((s) => s.kind === 'tool' ? s.call.name : s.kind).join() !== 'TodoWrite,thinking,Task,TodoWrite')
	throw new Error('consecutive steps should form one group, without the nested call');
const task = group.steps[2];
if (task.kind !== 'tool' || task.children[0]?.result?.output !== 'a.go') throw new Error("a subagent's call should nest in its Task call");
if (chat.plan.map((t) => t.status).join() !== 'completed,in_progress') throw new Error('the latest TodoWrite should be the plan');
apply(chat, ev({ case: 'agentText', value: { text: 'done' } }));
apply(chat, ev({ case: 'toolCall', value: { id: 'b', name: 'Bash', inputJson: '{}' } }));
if (count() !== n + 3 || chat.items.at(-1)?.kind !== 'steps') throw new Error('text should end a group');

// Thousands of events stay linear: results are found by id, not by scanning the chat.
const big = newChat();
const t0 = performance.now();
for (let i = 0; i < 20000; i++) {
	apply(big, ev({ case: 'toolCall', value: { id: `t${i}`, name: 'Read', inputJson: '{}' } }));
	apply(big, ev({ case: 'toolResult', value: { toolCallId: `t${i}`, output: 'x' } }));
	if (i % 10 === 0) apply(big, ev({ case: 'agentText', value: { text: 'x' } }));
}
if (performance.now() - t0 > 1000) throw new Error('20k tool calls took over a second');

const m = count();
apply(chat, ev({ case: 'presenceChanged', value: { userIds: ['alice', 'bob'] } }, 0n));
if (chat.present.join() !== 'alice,bob' || count() !== m) throw new Error('presence should replace the present set, not add items');
apply(chat, { ...ev({ case: 'typing', value: {} }, 0n), actor: 'bob' });
apply(chat, { ...ev({ case: 'typing', value: {} }, 0n), actor: 'carol' });
if (typingNow(chat, Date.now()).join() !== 'bob,carol' || count() !== m) throw new Error('typing should be tracked per person');
if (typingNow(chat, Date.now() + 6000).length) throw new Error('typing should expire');
apply(chat, { ...ev({ case: 'message', value: { text: 'hi', attachments: [{ id: 'f1', name: 'shot.png', mediaType: 'image/png' }] } }), actor: 'bob' });
const posted = chat.items.at(-1);
if (posted?.kind !== 'message' || posted.attachments[0]?.id !== 'f1') throw new Error('a message should keep its attachments');
if (typingNow(chat, Date.now()).join() !== 'carol') throw new Error("a person's message should end their typing");

const asks = newChat();
apply(asks, { seq: 7n, actor: 'bob', kind: { case: 'turnRequest', value: { message: { text: '@agent go' } } } } as Event);
apply(asks, { seq: 8n, actor: 'bob', kind: { case: 'turnRequest', value: { message: { text: '@agent again' } } } } as Event);
apply(asks, { seq: 9n, actor: 'ann', kind: { case: 'turnRequestResolved', value: { requestSeq: 7n, approved: false, note: 'no' } } } as Event);
const [first, second] = asks.items;
if (first.kind !== 'request' || first.resolved?.by !== 'ann' || first.resolved.note !== 'no' || first.text !== '@agent go')
	throw new Error('a resolution should attach to its request');
if (second.kind !== 'request' || second.resolved || asks.items.length !== 2) throw new Error('other requests should stay open');
apply(asks, { seq: 10n, actor: 'ann', kind: { case: 'handoffOffered', value: { toUserId: 'bob', note: 'tests next' } } } as Event);
apply(asks, { seq: 11n, actor: 'bob', kind: { case: 'handoffResolved', value: { offerSeq: 10n, accepted: true } } } as Event);
const handoff = asks.items.at(-1);
if (handoff?.kind !== 'handoff' || handoff.offer.toUserId !== 'bob' || handoff.resolved?.by !== 'bob' || !handoff.resolved.accepted || asks.items[2] !== handoff)
	throw new Error('a handoff should be a card its resolution attaches to');

const [f] = parseDiff('diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -3,4 +3,4 @@ func\n a\n-b\n-c\n+B\n d\n\\ No newline at end of file\n');
if (f.path !== 'x.go' || f.add !== 1 || f.del !== 2 || f.lines.map((l) => `${l.old ?? ''}/${l.new ?? ''}`).join() !== '/,3/3,4/,5/,/4,6/5,/')
	throw new Error('diff lines should be numbered in both versions');
const both = locate(f.lines, 2, 4); // -b -c +B: numbered in the branch
const removed = { path: 'x.go', body: '', ...locate(f.lines, 2, 3) };
if (both.old || both.startLine !== 4 || both.endLine !== 4 || removed.startLine !== 4 || removed.endLine !== 5 || !removed.old)
	throw new Error('a range should be numbered in the branch, or in the base if it only removes lines');
if (f.lines.findIndex((l) => endsAt(removed, l)) !== 3 || endsAt({ ...removed, code: '-x' }, f.lines[3]))
	throw new Error('a comment should mark its last line while that line is unchanged');
apply(chat, ev({ case: 'autoPush', value: { state: AutoPushState.UNAVAILABLE, detail: 'origin cannot be pushed to' } }));
const push = chat.items.at(-1);
if (push?.kind !== 'push' || push.pushed) throw new Error('autoPush should become a chat item');
const pushes = count();
apply(chat, ev({ case: 'autoPush', value: { state: AutoPushState.UNAVAILABLE, detail: 'origin cannot be pushed to' } }));
if (count() !== pushes) throw new Error('a thread should say once that origin cannot be pushed to');

// Paging: a tail plus older pages prepended one by one gives the same chat as the full replay,
// wherever the page edges fall (inside groups, between a call and its result, inside a subagent).
const history: Event[] = [];
const add = (kind: unknown, extra = {}) => history.push({ ...ev(kind, BigInt(history.length + 1)), ...extra } as Event);
const call = (id: string, name: string, parentToolCallId = '') => add({ case: 'toolCall', value: { id, name, inputJson: '{}', parentToolCallId } });
const result = (id: string) => add({ case: 'toolResult', value: { toolCallId: id, output: `out ${id}` } });
for (let t = 0; t < 4; t++) {
	add({ case: 'message', value: { text: `turn ${t}`, attachments: [{ id: `shot${t}`, name: 'shot.png', mediaType: 'image/png' }] } }, { actor: 'dev' });
	if (t !== 3) add({ case: 'toolCall', value: { id: `todo${t}`, name: 'TodoWrite', inputJson: plan(['in_progress', 'pending']) } });
	call(`r${t}`, 'Read');
	if (t === 1) add({ case: 'permissionRequested', value: { id: 'p1', toolCallId: 'r1', toolName: 'Read', inputJson: '{}' } });
	if (t === 2) add({ case: 'permissionResolved', value: { id: 'p1', decision: PermissionDecision.ALLOW, message: '' } }, { actor: 'dev' });
	add({ case: 'agentThinking', value: { text: 'hmm' } });
	call(`task${t}`, 'Task');
	call(`g${t}`, 'Grep', `task${t}`);
	result(`g${t}`);
	call(`b${t}`, 'Bash', `task${t}`);
	result(`r${t}`);
	result(`b${t}`);
	result(`task${t}`);
	add({ case: 'agentText', value: { text: `done ${t}` } });
	add({ case: 'turnEnded', value: { durationMs: 1n, costUsd: 0 } });
	if (t === 1) add({ case: 'turnRequest', value: { message: { text: '@agent please', attachments: [{ id: 'notes', name: 'notes.md', mediaType: 'text/plain' }] } } }, { actor: 'bob' });
	if (t === 2) add({ case: 'turnRequestResolved', value: { requestSeq: BigInt(history.findIndex((e) => e.kind.case === 'turnRequest') + 1), approved: true } }, { actor: 'dev' });
	if (t === 1) add({ case: 'handoffOffered', value: { toUserId: 'bob', note: '' } }, { actor: 'dev' });
	if (t === 2) add({ case: 'handoffResolved', value: { offerSeq: BigInt(history.findIndex((e) => e.kind.case === 'handoffOffered') + 1) } }, { actor: 'bob' });
}
const show = (c: Chat) => JSON.stringify({ items: c.items, plan: c.plan, planSeq: c.planSeq }, (_, v) => (typeof v === 'bigint' ? Number(v) : v));
const whole = newChat();
for (const e of history) apply(whole, e);
for (let tail = 1; tail <= history.length; tail++) {
	for (const size of [1, 2, 3, 7]) {
		const paged = newChat();
		let oldest = history.length - tail;
		// The server sends the latest plan from before the tail first, marked as context.
		const before = history.slice(0, oldest).findLast((e) => e.kind.case === 'toolCall' && e.kind.value.name === 'TodoWrite');
		if (before) apply(paged, { ...before, context: true } as Event);
		for (const e of history.slice(oldest)) apply(paged, e);
		while (oldest > 0) {
			prepend(paged, history.slice(Math.max(0, oldest - size), oldest));
			oldest = Math.max(0, oldest - size);
		}
		if (show(paged) !== show(whole)) throw new Error(`tail ${tail}, pages of ${size}: paged history differs from the full replay`);
		for (let seq = 1n; seq <= history.length; seq++)
			if (itemAt(paged, seq) !== itemAt(whole, seq)) throw new Error(`tail ${tail}, pages of ${size}: #${seq} maps to another item`);
		const first = paged.items[0];
		if (first.kind !== 'message' || first.attachments[0]?.id !== 'shot0' || !paged.items.some((i) => i.kind === 'request' && i.attachments[0]?.id === 'notes'))
			throw new Error('attachments in older pages should render');
		if (paged.orphans.results.size || paged.orphans.calls.size || paged.orphans.resolved.size || paged.orphans.handoffs.size || paged.orphans.answers.size) throw new Error('every orphan should find its call');
	}
}
const tailOnly = newChat();
apply(tailOnly, { ...history[history.findLastIndex((e) => e.kind.case === 'toolCall' && e.kind.value.name === 'TodoWrite')], context: true } as Event);
for (const e of history.slice(-3)) apply(tailOnly, e);
if (!tailOnly.plan.length || tailOnly.items.length !== 2) throw new Error('a context plan sets the plan without adding an item');
{
	// A permission request becomes its own item next to its tool call, and its answer resolves it.
	const c = newChat();
	apply(c, ev({ case: 'toolCall', value: { id: 'tb', name: 'Bash', inputJson: '{"command":"ls"}' } }));
	apply(c, ev({ case: 'permissionRequested', value: { id: 'r1', toolCallId: 'tb', toolName: 'Bash', inputJson: '{"command":"ls"}' } }));
	apply(c, ev({ case: 'permissionRequested', value: { id: 'r2', toolCallId: 'unknown', toolName: 'Write', inputJson: '{}' } }));
	const [, p1, p2] = c.items;
	if (p1?.kind !== 'permission' || p1.tool !== c.tools.get('tb') || p2?.kind !== 'permission' || p2.tool.call.name !== 'Write')
		throw new Error('a permission request should point at its tool call, or one made from the request');
	apply(c, { seq: 9n, actor: 'ann', kind: { case: 'permissionResolved', value: { id: 'r1', decision: PermissionDecision.DENY, message: 'no' } } } as Event);
	if (p1.resolved?.by !== 'ann' || p1.resolved.message !== 'no' || p2.resolved) throw new Error('an answer should resolve only its request');
}
{
	// The CLI may ask before it streams the call: the card points at the call once it arrives, and gets its result.
	const c = newChat();
	apply(c, ev({ case: 'permissionRequested', value: { id: 'r1', toolCallId: 'tf', toolName: 'Bash', inputJson: '{"command":"ls"}' } }, 1n));
	apply(c, ev({ case: 'toolCall', value: { id: 'tf', name: 'Bash', inputJson: '{"command":"ls"}' } }, 2n));
	apply(c, ev({ case: 'toolResult', value: { toolCallId: 'tf', output: 'a.go' } }, 3n));
	const [card, steps] = c.items;
	if (card?.kind !== 'permission' || card.tool.seq !== 2n || card.tool.result?.output !== 'a.go' || steps?.kind !== 'steps' || card.tool !== steps.steps[0] || c.orphans.cards.size)
		throw new Error('a permission card asked before its call should point at the call once it arrives');
}
{
	// Votes on a question: each person's latest counts, an empty vote withdraws it, and votes after a page edge find their card.
	const vote = (actor: string, answers: Record<string, string>, seq: bigint) =>
		({ seq, actor, kind: { case: 'permissionVoted', value: { id: 'q1', answers } } }) as Event;
	const asked = ev({ case: 'permissionRequested', value: { id: 'q1', toolCallId: 'tq', toolName: 'AskUserQuestion', inputJson: '{}' } }, 1n);
	const c = newChat();
	for (const e of [asked, vote('bob', { 'Which?': 'A' }, 2n), vote('bob', { 'Which?': 'B' }, 3n), vote('cy', { 'Which?': 'A' }, 4n), vote('cy', {}, 5n)]) apply(c, e);
	const q = c.items[0];
	if (q?.kind !== 'permission' || JSON.stringify(q.votes) !== '{"bob":{"Which?":"B"}}') throw new Error('a vote should replace the voter’s last one');
	const paged = newChat();
	apply(paged, vote('bob', { 'Which?': 'A' }, 3n));
	prepend(paged, [asked]);
	const p = paged.items[0];
	if (p?.kind !== 'permission' || p.votes.bob?.['Which?'] !== 'A' || paged.orphans.votes.size) throw new Error('a vote after the page edge should find its card');
}
apply(chat, { seq: 10n, actor: 'bob', kind: { case: 'forkNote', value: { threadId: 't2', title: 'Fork of x', turn: 2 } } } as Event);
const forked = chat.items.at(-1);
if (forked?.kind !== 'fork' || forked.actor !== 'bob' || forked.note.threadId !== 't2') throw new Error('forkNote should become a link to the fork');

// Search results link to an event's seq: it maps to the item showing it, once it arrived.
const jump = newChat();
apply(jump, ev({ case: 'message', value: { text: 'hi', review: [] } }, 3n));
apply(jump, ev({ case: 'toolCall', value: { id: 'j1', name: 'Bash', inputJson: '{}' } }, 4n));
apply(jump, ev({ case: 'toolCall', value: { id: 'j2', name: 'Read', inputJson: '{}' } }, 6n));
if (itemAt(jump, 3n) !== 0 || itemAt(jump, 6n) !== 1 || itemAt(jump, 5n) !== 1 || itemAt(jump, 2n) !== -1 || itemAt(jump, 7n) !== -1)
	throw new Error('a seq should map to the item that shows it, or -1 before it arrived');

const parent = newChat();
apply(parent, ev({ case: 'lineageUpdate', value: { threadId: 'c1', title: 'parser', status: Status.AWAITING_INPUT, text: 'Fixed it.' } }));
const sub = parent.items[0];
if (sub?.kind !== 'lineage' || sub.update.threadId !== 'c1' || sub.update.text !== 'Fixed it.') throw new Error('a lineage update should become a chat item');
const overlapped = newChat();
apply(overlapped, ev({ case: 'overlap', value: { threadId: 'o1', title: 'auth', ownerId: 'bob', paths: ['src/a.ts'] } }));
const ov = overlapped.items[0];
if (ov?.kind !== 'overlap' || ov.overlap.threadId !== 'o1' || ov.overlap.paths[0] !== 'src/a.ts') throw new Error('an overlap should become a chat item');
const prChat = newChat();
apply(prChat, ev({ case: 'pullRequestChanged', value: { change: '', pullRequest: { number: 3 } } }, 0n)); // a refresh: no line
apply(prChat, ev({ case: 'pullRequestChanged', value: { change: 'checks_failed', pullRequest: { number: 3 } } }, 2n));
const prLine = prChat.items;
if (prLine.length !== 1 || prLine[0].kind !== 'pullRequest' || prLine[0].changed.change !== 'checks_failed') throw new Error('only a notable pull request change should become a chat item');
console.log('ok');
