import type { Timestamp } from '@bufbuild/protobuf/wkt';
import type { AgentSettings, Event, Mode, SetupStep, ToolCall, ToolResult, TurnEnded } from './gen/banda/v1/banda_pb.ts';

export type Item =
	| { kind: 'message'; actor: string; text: string; time?: Timestamp }
	| { kind: 'text' | 'thinking' | 'notice'; text: string }
	| { kind: 'tool'; call: ToolCall; result?: ToolResult }
	| { kind: 'turn'; turn: TurnEnded }
	| { kind: 'mode'; mode: Mode }
	| { kind: 'settings'; actor: string; settings: AgentSettings }
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
			// A checklist: a step joins the last item's checklist, or replaces its running step with the same id.
			// A step that already finished there means setup runs again (resume, retry): a new checklist starts.
			const step = k.value;
			const last = chat.items.at(-1);
			const at = last?.kind === 'setup' ? last.steps.findIndex((s) => s.id === step.id) : -1;
			// 1 is SetupState.RUNNING (node's type stripping, used by events.check.ts, can't import enums).
			if (last?.kind !== 'setup' || (at >= 0 && last.steps[at].state !== 1))
				chat.items.push({ kind: 'setup', steps: [step] });
			else if (at >= 0) last.steps[at] = step;
			else last.steps.push(step);
			break;
		}
		case 'modeChanged':
			chat.items.push({ kind: 'mode', mode: k.value.mode });
			break;
		case 'agentSettings':
			chat.items.push({ kind: 'settings', actor: e.actor, settings: k.value });
			break;
	}
}
