import type { Timestamp } from '@bufbuild/protobuf/wkt';
import type { Event, Mode, SetupStep, ToolCall, ToolResult, LineageUpdate, TurnEnded, TurnRequestResolved } from './gen/banda/v1/banda_pb.ts';

// A subagent's (Task's) calls nest in `children`.
export type Tool = { kind: 'tool'; call: ToolCall; result?: ToolResult; children: Tool[] };
// What the agent did between two texts: tool calls and thinking, shown folded.
export type Step = Tool | { kind: 'thinking'; text: string };
export type Todo = { content: string; status: 'pending' | 'in_progress' | 'completed'; activeForm?: string };

export type Item =
	| { kind: 'message'; actor: string; text: string; time?: Timestamp }
	| { kind: 'text' | 'notice'; text: string }
	| { kind: 'steps'; steps: Step[] }
	// A teammate's turn request; `resolved` (by the owner, `by`) once approved or declined.
	| { kind: 'request'; seq: bigint; actor: string; text: string; time?: Timestamp; resolved?: TurnRequestResolved & { by: string } }
	| { kind: 'turn'; turn: TurnEnded }
	// A sub-thread was spawned, finished its turns or failed.
	| { kind: 'lineage'; update: LineageUpdate }
	| { kind: 'mode'; mode: Mode }
	| { kind: 'setup'; steps: SetupStep[] };

// `streaming` is the live assistant bubble: deltas append, the next agent_text replaces it.
// `plan` is the latest TodoWrite list. `tools` finds a call by id in O(1) for its result and its subagent's calls.
// `present` is who watches the thread; `typing` is when each person last typed (Date.now()), until they post.
export type Chat = {
	items: Item[];
	streaming: string;
	plan: Todo[];
	tools: Map<string, Tool>;
	present: string[];
	typing: Record<string, number>;
};

export const newChat = (): Chat => ({ items: [], streaming: '', plan: [], tools: new Map(), present: [], typing: {} });

export const todos = (call: ToolCall): Todo[] => {
	try {
		return JSON.parse(call.inputJson).todos ?? [];
	} catch {
		return [];
	}
};

// Appends a step to the last item if it is a steps group, else starts one. Returns the stored
// (in Svelte: proxied) step, so later mutations through `tools` stay reactive.
function step(chat: Chat, s: Step) {
	const last = chat.items.at(-1);
	if (last?.kind === 'steps') last.steps.push(s);
	else chat.items.push({ kind: 'steps', steps: [s] });
	return (chat.items.at(-1) as Extract<Item, { kind: 'steps' }>).steps.at(-1)!;
}

// People who typed in the last `ms`, oldest first.
export const typingNow = (chat: Chat, now: number, ms = 5000) =>
	Object.entries(chat.typing)
		.filter(([, t]) => now - t < ms)
		.sort(([, a], [, b]) => a - b)
		.map(([actor]) => actor);

export function apply(chat: Chat, e: Event) {
	const k = e.kind;
	switch (k.case) {
		case 'message':
			delete chat.typing[e.actor];
			chat.items.push({ kind: 'message', actor: e.actor, text: k.value.text, time: e.time });
			break;
		case 'lineageUpdate':
			chat.items.push({ kind: 'lineage', update: k.value });
			break;
		case 'turnRequest':
			delete chat.typing[e.actor];
			chat.items.push({ kind: 'request', seq: e.seq, actor: e.actor, text: k.value.message?.text ?? '', time: e.time });
			break;
		case 'turnRequestResolved': {
			const seq = k.value.requestSeq;
			const req = chat.items.find((i): i is Extract<Item, { kind: 'request' }> => i.kind === 'request' && i.seq === seq);
			if (req) req.resolved = { ...k.value, by: e.actor };
			break;
		}
		case 'agentText':
			chat.streaming = '';
			chat.items.push({ kind: 'text', text: k.value.text });
			break;
		case 'agentTextDelta':
			chat.streaming += k.value.text;
			break;
		case 'agentThinking':
			step(chat, { kind: 'thinking', text: k.value.text });
			break;
		case 'notice':
			chat.items.push({ kind: 'notice', text: k.value.text });
			break;
		case 'toolCall': {
			const call = k.value;
			const parent = call.parentToolCallId ? chat.tools.get(call.parentToolCallId) : undefined;
			let tool: Tool = { kind: 'tool', call, children: [] };
			if (parent) tool = parent.children[parent.children.push(tool) - 1];
			else tool = step(chat, tool) as Tool;
			chat.tools.set(call.id, tool);
			if (call.name === 'TodoWrite' && !parent) chat.plan = todos(call);
			break;
		}
		case 'toolResult': {
			const tool = chat.tools.get(k.value.toolCallId);
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
		case 'presenceChanged':
			chat.present = k.value.userIds;
			break;
		case 'typing':
			chat.typing[e.actor] = Date.now();
			break;
		case 'modeChanged':
			chat.items.push({ kind: 'mode', mode: k.value.mode });
			break;
	}
}
