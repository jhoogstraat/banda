// Run: node src/lib/events.check.ts
import { endsAt, locate, parseDiff } from './diff.ts';
import { apply, type Chat } from './events.ts';
import type { Event } from './gen/banda/v1/banda_pb.ts';

const ev = (kind: unknown, seq = 1n) => ({ seq, actor: 'agent', kind }) as Event;
const chat: Chat = { items: [], streaming: '' };
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
if (tool.kind !== 'tool' || tool.result?.output !== 'ok') throw new Error('toolResult should attach to its toolCall');
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
