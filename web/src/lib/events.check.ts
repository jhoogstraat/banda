// Run: node src/lib/events.check.ts
import { apply, type Chat } from './events.ts';
import type { Event } from './gen/banda/v1/banda_pb.ts';

const ev = (kind: unknown, seq = 1n) => ({ seq, actor: 'agent', kind }) as Event;
const chat: Chat = { items: [], streaming: '' };
const live = () => chat.streaming;

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
console.log('ok');
