// Run: node src/lib/events.check.ts
import { endsAt, locate, parseDiff } from './diff.ts';
import { apply, newChat, typingNow } from './events.ts';
import type { Event } from './gen/banda/v1/banda_pb.ts';

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

const step = (id: string, state: number, output = '') => ev({ case: 'setupStep', value: { id, title: id, state, output } });
apply(chat, step('checkout', 1));
apply(chat, step('tools', 1));
apply(chat, step('checkout', 2));
apply(chat, step('tools', 3, 'mise: boom'));
const setup = chat.items[3];
if (count() !== 4 || setup.kind !== 'setup') throw new Error('setup steps should form one checklist');
if (setup.steps.map((s) => `${s.id}:${s.state}`).join() !== 'checkout:2,tools:3' || setup.steps[1].output !== 'mise: boom')
	throw new Error('a setup step should replace the earlier one with its id');
apply(chat, ev({ case: 'notice', value: { text: 'later' } }));
apply(chat, step('tools', 2));
const rerun = chat.items[5];
if (count() !== 6 || setup.steps[1].state !== 3 || rerun.kind !== 'setup' || rerun.steps[0].state !== 2)
	throw new Error('a step after other items should start a new checklist, leaving the old one alone');
apply(chat, step('setup', 1));
apply(chat, step('setup', 3));
apply(chat, step('setup', 1)); // retry: only status changes in between, which add no item
if (count() !== 7 || rerun.kind !== 'setup' || rerun.steps[1].state !== 3)
	throw new Error('a finished step running again should start a new checklist');
apply(chat, { seq: 9n, actor: 'alice', kind: { case: 'agentSettings', value: { model: 'm', effort: 'low' } } } as Event);
const switched = chat.items.at(-1);
if (switched?.kind !== 'settings' || switched.actor !== 'alice' || switched.settings.model !== 'm')
	throw new Error('agentSettings should become a divider naming who switched');

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
apply(chat, { ...ev({ case: 'message', value: { text: 'hi' } }), actor: 'bob' });
if (typingNow(chat, Date.now()).join() !== 'carol') throw new Error("a person's message should end their typing");

const asks = newChat();
apply(asks, { seq: 7n, actor: 'bob', kind: { case: 'turnRequest', value: { message: { text: '@agent go' } } } } as Event);
apply(asks, { seq: 8n, actor: 'bob', kind: { case: 'turnRequest', value: { message: { text: '@agent again' } } } } as Event);
apply(asks, { seq: 9n, actor: 'ann', kind: { case: 'turnRequestResolved', value: { requestSeq: 7n, approved: false, note: 'no' } } } as Event);
const [first, second] = asks.items;
if (first.kind !== 'request' || first.resolved?.by !== 'ann' || first.resolved.note !== 'no' || first.text !== '@agent go')
	throw new Error('a resolution should attach to its request');
if (second.kind !== 'request' || second.resolved || asks.items.length !== 2) throw new Error('other requests should stay open');

const [f] = parseDiff('diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -3,4 +3,4 @@ func\n a\n-b\n-c\n+B\n d\n\\ No newline at end of file\n');
if (f.path !== 'x.go' || f.add !== 1 || f.del !== 2 || f.lines.map((l) => `${l.old ?? ''}/${l.new ?? ''}`).join() !== '/,3/3,4/,5/,/4,6/5,/')
	throw new Error('diff lines should be numbered in both versions');
const both = locate(f.lines, 2, 4); // -b -c +B: numbered in the branch
const removed = { path: 'x.go', body: '', ...locate(f.lines, 2, 3) };
if (both.old || both.startLine !== 4 || both.endLine !== 4 || removed.startLine !== 4 || removed.endLine !== 5 || !removed.old)
	throw new Error('a range should be numbered in the branch, or in the base if it only removes lines');
if (f.lines.findIndex((l) => endsAt(removed, l)) !== 3 || endsAt({ ...removed, code: '-x' }, f.lines[3]))
	throw new Error('a comment should mark its last line while that line is unchanged');
console.log('ok');
