import type { Timestamp } from '@bufbuild/protobuf/wkt';
import type { Event, Mode, SetupStep, ToolCall, ToolResult, TurnEnded } from './gen/banda/v1/banda_pb.ts';

export type Item =
	| { kind: 'message'; actor: string; text: string; time?: Timestamp }
	| { kind: 'text' | 'thinking' | 'notice'; text: string }
	| { kind: 'tool'; call: ToolCall; result?: ToolResult }
	| { kind: 'turn'; turn: TurnEnded }
	| { kind: 'mode'; mode: Mode }
	| { kind: 'setup'; steps: SetupStep[] };

// `streaming` is the live assistant bubble: deltas append, the next agent_text replaces it.
export type Chat = { items: Item[]; streaming: string };

export function apply(chat: Chat, e: Event) {
	const k = e.kind;
	switch (k.case) {
		case 'message':
			chat.items.push({ kind: 'message', actor: e.actor, text: k.value.text, time: e.time });
			break;
		case 'agentText':
			chat.streaming = '';
			chat.items.push({ kind: 'text', text: k.value.text });
			break;
		case 'agentTextDelta':
			chat.streaming += k.value.text;
			break;
		case 'agentThinking':
			chat.items.push({ kind: 'thinking', text: k.value.text });
			break;
		case 'notice':
			chat.items.push({ kind: 'notice', text: k.value.text });
			break;
		case 'toolCall':
			chat.items.push({ kind: 'tool', call: k.value });
			break;
		case 'toolResult': {
			const id = k.value.toolCallId;
			const tool = chat.items.findLast(
				(i): i is Extract<Item, { kind: 'tool' }> => i.kind === 'tool' && i.call.id === id
			);
			if (tool) tool.result = k.value;
			break;
		}
		case 'turnEnded':
			chat.streaming = '';
			chat.items.push({ kind: 'turn', turn: k.value });
			break;
		case 'setupStep': {
			// A checklist: a step replaces the one with its id in the latest checklist, or joins it while nothing came after it.
			const step = k.value;
			const last = chat.items.findLast((i): i is Extract<Item, { kind: 'setup' }> => i.kind === 'setup');
			const at = last?.steps.findIndex((s) => s.id === step.id) ?? -1;
			if (last && at >= 0) last.steps[at] = step;
			else if (last && chat.items.at(-1) === last) last.steps.push(step);
			else chat.items.push({ kind: 'setup', steps: [step] });
			break;
		}
		case 'modeChanged':
			chat.items.push({ kind: 'mode', mode: k.value.mode });
			break;
	}
}
