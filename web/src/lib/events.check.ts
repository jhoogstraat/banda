// Run: node src/lib/events.check.ts
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
console.log('ok');
