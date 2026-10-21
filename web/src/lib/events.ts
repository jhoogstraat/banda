import type { Timestamp } from '@bufbuild/protobuf/wkt';
import type {
	AgentSettings,
	Event,
	Mode,
	PermissionRequested,
	PermissionResolved,
	ReviewComment,
	SetupStep,
	ToolCall,
	ToolResult,
	TurnEnded,
	TurnRequestResolved
} from './gen/banda/v1/banda_pb.ts';

// A subagent's (Task's) calls nest in `children`. `seq` and `resultSeq` are the events' seqs;
// `cut` says the stream cut a large input or output (History with `full` loads it).
export type Tool = { kind: 'tool'; call: ToolCall; result?: ToolResult; children: Tool[]; seq: bigint; resultSeq?: bigint; cut?: boolean };
// What the agent did between two texts: tool calls and thinking, shown folded.
export type Step = Tool | { kind: 'thinking'; text: string };
export type Todo = { content: string; status: 'pending' | 'in_progress' | 'completed'; activeForm?: string };

// `key` is the seq of the event that started the item: stable when older history is prepended.
export type Item = { key: number } & (
	| { kind: 'message'; actor: string; text: string; review: ReviewComment[]; time?: Timestamp }
	| { kind: 'text' | 'notice'; text: string }
	| { kind: 'steps'; steps: Step[] }
	// A teammate's turn request; `resolved` (by the owner, `by`) once approved or declined.
	| { kind: 'request'; seq: bigint; actor: string; text: string; review: ReviewComment[]; time?: Timestamp; resolved?: TurnRequestResolved & { by: string } }
	// The agent asks before a tool call (`tool`, the call itself when it was streamed); `resolved` once answered.
	| { kind: 'permission'; req: PermissionRequested; tool: Tool; resolved?: PermissionResolved & { by: string } }
	| { kind: 'turn'; turn: TurnEnded }
	| { kind: 'mode'; mode: Mode }
	| { kind: 'settings'; actor: string; settings: AgentSettings }
	| { kind: 'push'; text: string; pushed: boolean }
	| { kind: 'setup'; steps: SetupStep[] }
);

// `streaming` is the live assistant bubble: deltas append, the next agent_text replaces it.
// `plan` is the latest TodoWrite list. `tools` finds a call by id in O(1) for its result and its subagent's calls.
// `orphans` are what arrived before its tool call is loaded (it's in an older page): results by call id,
// subagent calls by parent id, turn request resolutions by request seq, permission answers by request id. `planSeq` is the seq of the TodoWrite behind `plan`.
// `present` is who watches the thread; `typing` is when each person last typed (Date.now()), until they post.
export type Chat = {
	items: Item[];
	streaming: string;
	plan: Todo[];
	planSeq: bigint;
	tools: Map<string, Tool>;
	orphans: {
		results: Map<string, { result: ToolResult; seq: bigint; cut: boolean }>;
		calls: Map<string, Tool[]>;
		resolved: Map<bigint, TurnRequestResolved & { by: string }>;
		answers: Map<string, PermissionResolved & { by: string }>;
	};
	present: string[];
	typing: Record<string, number>;
};

export const newChat = (): Chat => ({
	items: [],
	streaming: '',
	plan: [],
	planSeq: 0n,
	tools: new Map(),
	orphans: { results: new Map(), calls: new Map(), resolved: new Map(), answers: new Map() },
	present: [],
	typing: {}
});

// Ephemeral events (seq 0) that start an item get keys below zero.
let ephemeral = 0;
const keyOf = (e: Event) => Number(e.seq) || --ephemeral;

export const todos = (call: ToolCall): Todo[] => {
	try {
		return JSON.parse(call.inputJson).todos ?? [];
	} catch {
		return [];
	}
};

// Appends a step to the last item if it is a steps group, else starts one. Returns the stored
// (in Svelte: proxied) step, so later mutations through `tools` stay reactive.
function step(chat: Chat, s: Step, key: number) {
	const last = chat.items.at(-1);
	if (last?.kind === 'steps') last.steps.push(s);
	else chat.items.push({ key, kind: 'steps', steps: [s] });
	return (chat.items.at(-1) as Extract<Item, { kind: 'steps' }>).steps.at(-1)!;
}

// People who typed in the last `ms`, oldest first.
export const typingNow = (chat: Chat, now: number, ms = 5000) =>
	Object.entries(chat.typing)
		.filter(([, t]) => now - t < ms)
		.sort(([, a], [, b]) => a - b)
		.map(([actor]) => actor);

// Registers a stored tool and its subagent's calls for lookups by id.
function register(chat: Chat, tool: Tool) {
	chat.tools.set(tool.call.id, tool);
	for (const c of tool.children) register(chat, c);
}

function setPlan(chat: Chat, call: ToolCall, seq: bigint) {
	if (seq < chat.planSeq) return;
	chat.plan = todos(call);
	chat.planSeq = seq;
}

export function apply(chat: Chat, e: Event) {
	const k = e.kind;
	if (e.context) {
		// The plan from before a tail; everything else in it belongs to older history.
		if (k.case === 'toolCall' && k.value.name === 'TodoWrite') setPlan(chat, k.value, e.seq);
		return;
	}
	const key = keyOf(e);
	switch (k.case) {
		case 'message':
			delete chat.typing[e.actor];
			chat.items.push({ key, kind: 'message', actor: e.actor, text: k.value.text, review: k.value.review, time: e.time });
			break;
		case 'turnRequest':
			delete chat.typing[e.actor];
			chat.items.push({ key, kind: 'request', seq: e.seq, actor: e.actor, text: k.value.message?.text ?? '', review: k.value.message?.review ?? [], time: e.time });
			break;
		case 'turnRequestResolved': {
			const seq = k.value.requestSeq;
			const req = chat.items.find((i): i is Extract<Item, { kind: 'request' }> => i.kind === 'request' && i.seq === seq);
			if (req) req.resolved = { ...k.value, by: e.actor };
			else chat.orphans.resolved.set(seq, { ...k.value, by: e.actor }); // its request is in older history
			break;
		}
		case 'agentText':
			chat.streaming = '';
			chat.items.push({ key, kind: 'text', text: k.value.text });
			break;
		case 'agentTextDelta':
			chat.streaming += k.value.text;
			break;
		case 'agentThinking':
			step(chat, { kind: 'thinking', text: k.value.text }, key);
			break;
		case 'notice':
			chat.items.push({ key, kind: 'notice', text: k.value.text });
			break;
		case 'toolCall': {
			const call = k.value;
			let tool: Tool = { kind: 'tool', call, children: [], seq: e.seq, cut: e.truncated };
			if (call.parentToolCallId) {
				const parent = chat.tools.get(call.parentToolCallId);
				if (parent) tool = parent.children[parent.children.push(tool) - 1];
				else {
					// Its Task call is in older history: shown once that page is loaded.
					const kids = chat.orphans.calls.get(call.parentToolCallId) ?? [];
					chat.orphans.calls.set(call.parentToolCallId, [...kids, tool]);
				}
			} else tool = step(chat, tool, key) as Tool;
			chat.tools.set(call.id, tool);
			if (call.name === 'TodoWrite' && !call.parentToolCallId) setPlan(chat, call, e.seq);
			break;
		}
		case 'toolResult': {
			const tool = chat.tools.get(k.value.toolCallId);
			if (tool) Object.assign(tool, { result: k.value, resultSeq: e.seq, cut: tool.cut || e.truncated });
			else chat.orphans.results.set(k.value.toolCallId, { result: k.value, seq: e.seq, cut: e.truncated });
			break;
		}
		case 'permissionRequested': {
			const r = k.value;
			const call = { id: r.toolCallId, name: r.toolName, inputJson: r.inputJson } as ToolCall;
			chat.items.push({ key, kind: 'permission', req: r, tool: chat.tools.get(r.toolCallId) ?? { kind: 'tool', call, children: [], seq: 0n } });
			break;
		}
		case 'permissionResolved': {
			const id = k.value.id;
			const req = chat.items.findLast((i): i is Extract<Item, { kind: 'permission' }> => i.kind === 'permission' && i.req.id === id);
			if (req) req.resolved = { ...k.value, by: e.actor };
			else chat.orphans.answers.set(id, { ...k.value, by: e.actor }); // its request is in older history
			break;
		}
		case 'turnEnded':
			chat.streaming = '';
			chat.items.push({ key, kind: 'turn', turn: k.value });
			break;
		case 'setupStep': {
			// A checklist: a step joins the last item's checklist, or replaces its running step with the same id.
			// A step that already finished there means setup runs again (resume, retry): a new checklist starts.
			const step = k.value;
			const last = chat.items.at(-1);
			const at = last?.kind === 'setup' ? last.steps.findIndex((s) => s.id === step.id) : -1;
			// 1 is SetupState.RUNNING (node's type stripping, used by events.check.ts, can't import enums).
			if (last?.kind !== 'setup' || (at >= 0 && last.steps[at].state !== 1))
				chat.items.push({ key, kind: 'setup', steps: [step] });
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
		case 'autoPush':
			// 1 is AutoPushState.PUSHED.
			chat.items.push({ key, kind: 'push', text: k.value.detail, pushed: k.value.state === 1 });
			break;
		case 'modeChanged':
			chat.items.push({ key, kind: 'mode', mode: k.value.mode });
			break;
		case 'agentSettings':
			chat.items.push({ key, kind: 'settings', actor: e.actor, settings: k.value });
			break;
	}
}

// Prepends older history (the events right before the oldest loaded one, in seq order), giving the
// same chat as applying everything in order: a group split by the page edge is joined, and calls and
// results waiting for their tool call are attached.
// ponytail: a setup checklist split by the page edge stays two checklists.
export function prepend(chat: Chat, events: Event[]) {
	const old = newChat();
	for (const e of events) apply(old, e);
	if (old.planSeq > chat.planSeq) {
		chat.plan = old.plan;
		chat.planSeq = old.planSeq;
	}
	const first = chat.items[0];
	const last = old.items.at(-1);
	if (first?.kind === 'steps' && last?.kind === 'steps') {
		last.steps.push(...first.steps);
		chat.items.shift();
	}
	chat.items.unshift(...old.items);
	// Look tools up through chat.items: in Svelte these are the reactive proxies.
	for (const item of chat.items.slice(0, old.items.length)) {
		if (item.kind === 'steps') for (const s of item.steps) if (s.kind === 'tool') register(chat, s);
		if (item.kind === 'request' && chat.orphans.resolved.has(item.seq)) {
			item.resolved = chat.orphans.resolved.get(item.seq);
			chat.orphans.resolved.delete(item.seq);
		}
		if (item.kind === 'permission' && chat.orphans.answers.has(item.req.id)) {
			item.resolved = chat.orphans.answers.get(item.req.id);
			chat.orphans.answers.delete(item.req.id);
		}
	}
	// A permission card made before its call was loaded points at the call now.
	for (const item of chat.items)
		if (item.kind === 'permission' && !item.tool.seq) item.tool = chat.tools.get(item.req.toolCallId) ?? item.tool;
	for (const [id, o] of chat.orphans.results) {
		const tool = chat.tools.get(id);
		if (!tool) continue;
		Object.assign(tool, { result: o.result, resultSeq: o.seq, cut: tool.cut || o.cut });
		chat.orphans.results.delete(id);
	}
	for (const [id, kids] of chat.orphans.calls) {
		const parent = chat.tools.get(id);
		if (!parent) continue;
		parent.children.push(...kids);
		for (const c of parent.children) register(chat, c);
		chat.orphans.calls.delete(id);
	}
	// What waits in the older page waits for even older history.
	for (const [seq, r] of old.orphans.resolved) chat.orphans.resolved.set(seq, r);
	for (const [id, r] of old.orphans.answers) chat.orphans.answers.set(id, r);
	for (const [id, o] of old.orphans.results) chat.orphans.results.set(id, o);
	for (const [id, kids] of old.orphans.calls) chat.orphans.calls.set(id, [...kids, ...(chat.orphans.calls.get(id) ?? [])]);
}
