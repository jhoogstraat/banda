import type { Timestamp } from '@bufbuild/protobuf/wkt';
import {
	AutoPushState,
	SetupState,
	type AgentRetrying,
	type AgentSettings,
	type Attachment,
	type Event,
	type ForkNote,
	type HandoffOffered,
	type HandoffResolved,
	type LineageUpdate,
	type Mode,
	type Overlap,
	type PermissionMode,
	type PermissionRequested,
	type PermissionResolved,
	type PlanItem,
	type PreviewPort,
	type PullRequestChanged,
	type RepoAdded,
	type ReviewComment,
	type SetupStep,
	type SlashCommand,
	type ToolCall,
	type ToolResult,
	type TurnEnded,
	type TurnRequestResolved
} from './gen/banda/v1/banda_pb.js';

// A subagent's (Task's) calls nest in `children`. `seq` and `resultSeq` are the events' seqs;
// `cut` says the stream cut a large input or output (History with `full` loads it).
export type Tool = { kind: 'tool'; call: ToolCall; result?: ToolResult; children: Tool[]; seq: bigint; resultSeq?: bigint; cut?: boolean };
// What the agent did between two texts: tool calls and thinking, shown folded.
export type Step = Tool | { kind: 'thinking'; text: string };

// `key` is the seq of the event that started the item: stable when older history is prepended.
export type Item = { key: number } & (
	| { kind: 'message'; actor: string; text: string; review: ReviewComment[]; attachments: Attachment[]; proposal: boolean; time?: Timestamp }
	| { kind: 'text' | 'notice'; text: string }
	| { kind: 'steps'; steps: Step[] }
	// A teammate's turn request; `resolved` (by the owner, `by`) once approved or declined.
	| { kind: 'request'; seq: bigint; actor: string; text: string; review: ReviewComment[]; attachments: Attachment[]; time?: Timestamp; resolved?: TurnRequestResolved & { by: string } }
	// The agent asks before a tool call (`tool`, the call itself when it was streamed); `resolved` once answered.
	// `votes` are teammates' latest votes on a question (AskUserQuestion), by user.
	| { kind: 'permission'; req: PermissionRequested; tool: Tool; resolved?: PermissionResolved & { by: string }; votes: Record<string, Record<string, string>> }
	// `diff`: the turn changed the diff (the diffChanged event right before it, in older history right after it).
	| { kind: 'turn'; turn: TurnEnded; diff?: boolean }
	// A sub-thread was spawned, finished its turns or failed.
	| { kind: 'lineage'; actor: string; update: LineageUpdate }
	// The thread now changes files another one changes too.
	| { kind: 'overlap'; overlap: Overlap }
	// The pull request was opened or linked (by `actor`), or changed notably.
	| { kind: 'pullRequest'; actor: string; changed: PullRequestChanged }
	// `actor` switched it; `reason` is set when their post did (see ModeChanged).
	| { kind: 'mode'; actor: string; mode: Mode; reason: string }
	// A port started listening in the environment.
	| { kind: 'port'; port: PreviewPort }
	| { kind: 'settings'; actor: string; settings: AgentSettings }
	// The owner (`actor`) gave the thread a repository.
	| { kind: 'repo'; actor: string; repo: RepoAdded }
	| { kind: 'renamed'; actor: string; title: string }
	| { kind: 'permissionMode'; actor: string; mode: PermissionMode }
	| { kind: 'revoked'; actor: string; rule: string }
	// `unavailable`: origin can't be pushed to; shown once per thread, a runner says it again on every start.
	| { kind: 'push'; text: string; pushed: boolean; unavailable: boolean }
	| { kind: 'fork'; actor: string; note: ForkNote }
	// The owner (`actor`) offered the thread to a teammate; `resolved` once accepted, declined or cancelled (`by`).
	| { kind: 'handoff'; seq: bigint; actor: string; offer: HandoffOffered; time?: Timestamp; resolved?: HandoffResolved & { by: string } }
	| { kind: 'setup'; steps: SetupStep[] }
);

// `streaming` is the live assistant bubble: deltas append, the next agent_text replaces it.
// `plan` is the latest task list (PlanChanged). `tools` finds a call by id in O(1) for its result and its subagent's calls.
// `orphans` are what arrived before its tool call is loaded (it's in an older page): results by call id,
// subagent calls by parent id, turn request and handoff resolutions by their seq, permission answers and votes by request id,
// and live permission cards whose call comes next, by call id. `planSeq` is the seq of the PlanChanged behind `plan`.
// `seq` is the latest stored event applied (see itemAt).
// `commands` are the slash commands Claude Code offers (the latest SlashCommands).
// `retry` is Claude Code retrying a failed request, until the agent's next output.
// `waiting` is what a starting environment waits on (EnvWaiting), until the thread's status changes.
// `present` is who watches the thread; `typing` is when each person last typed (Date.now()), until they post.
export type Chat = {
	items: Item[];
	streaming: string;
	plan: PlanItem[];
	planSeq: bigint;
	seq: bigint;
	tools: Map<string, Tool>;
	orphans: {
		results: Map<string, { result: ToolResult; seq: bigint; cut: boolean }>;
		calls: Map<string, Tool[]>;
		resolved: Map<bigint, TurnRequestResolved & { by: string }>;
		handoffs: Map<bigint, HandoffResolved & { by: string }>;
		answers: Map<string, PermissionResolved & { by: string }>;
		votes: Map<string, Record<string, Record<string, string>>>;
		cards: Map<string, Extract<Item, { kind: 'permission' }>>;
	};
	commands: SlashCommand[];
	retry?: AgentRetrying;
	waiting: string;
	present: string[];
	typing: Record<string, number>;
	// A diffChanged waiting for the turn it belongs to.
	diffAhead: boolean;
};

export const newChat = (): Chat => ({
	items: [],
	streaming: '',
	plan: [],
	planSeq: 0n,
	commands: [],
	waiting: '',
	seq: 0n,
	tools: new Map(),
	orphans: { results: new Map(), calls: new Map(), resolved: new Map(), handoffs: new Map(), answers: new Map(), votes: new Map(), cards: new Map() },
	present: [],
	typing: {},
	diffAhead: false
});

// Ephemeral events (seq 0) that start an item get keys below zero.
let ephemeral = 0;
const keyOf = (e: Event) => Number(e.seq) || --ephemeral;

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

function setPlan(chat: Chat, plan: PlanItem[], seq: bigint) {
	if (seq < chat.planSeq) return;
	chat.plan = plan;
	chat.planSeq = seq;
}

// The index of the item that shows stored event `seq` (a tool call's group, say), or -1 if it isn't loaded:
// not arrived yet, or in older history.
export const itemAt = (chat: Chat, seq: bigint) => (seq > chat.seq ? -1 : chat.items.findLastIndex((i) => i.key > 0 && i.key <= seq));

export function apply(chat: Chat, e: Event) {
	const k = e.kind;
	if (e.context) {
		// The plan from before a tail; everything else in it belongs to older history.
		if (k.case === 'planChanged') setPlan(chat, k.value.items, e.seq);
		if (k.case === 'slashCommands') chat.commands = k.value.commands;
		return;
	}
	if (e.seq > chat.seq) chat.seq = e.seq;
	if (e.actor === 'agent') chat.retry = k.case === 'agentRetrying' ? k.value : undefined; // any output means the retry worked
	if (k.case === 'envWaiting') chat.waiting = k.value.reason;
	if (k.case === 'statusChanged') chat.waiting = '';
	const key = keyOf(e);
	switch (k.case) {
		case 'message':
			delete chat.typing[e.actor];
			chat.items.push({ key, kind: 'message', actor: e.actor, text: k.value.text, review: k.value.review, attachments: k.value.attachments, proposal: k.value.proposal, time: e.time });
			break;
		case 'lineageUpdate':
			chat.items.push({ key, kind: 'lineage', actor: e.actor, update: k.value });
			break;
		case 'overlap':
			chat.items.push({ key, kind: 'overlap', overlap: k.value });
			break;
		case 'pullRequestChanged':
			// Without a change it only says the pull request changed (the page refetches it).
			if (k.value.change) chat.items.push({ key, kind: 'pullRequest', actor: e.actor, changed: k.value });
			break;
		case 'turnRequest':
			delete chat.typing[e.actor];
			chat.items.push({ key, kind: 'request', seq: e.seq, actor: e.actor, text: k.value.message?.text ?? '', review: k.value.message?.review ?? [], attachments: k.value.message?.attachments ?? [], time: e.time });
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
			const card = chat.orphans.cards.get(call.id);
			if (card) {
				card.tool = tool;
				chat.orphans.cards.delete(call.id);
			}
			break;
		}
		case 'planChanged':
			setPlan(chat, k.value.items, e.seq);
			break;
		case 'slashCommands':
			chat.commands = k.value.commands;
			break;
		case 'toolResult': {
			const tool = chat.tools.get(k.value.toolCallId);
			if (tool) Object.assign(tool, { result: k.value, resultSeq: e.seq, cut: tool.cut || e.truncated });
			else chat.orphans.results.set(k.value.toolCallId, { result: k.value, seq: e.seq, cut: e.truncated });
			break;
		}
		case 'permissionRequested': {
			const r = k.value;
			const call = { id: r.toolCallId, name: r.toolName, inputJson: r.inputJson } as ToolCall;
			const tool = chat.tools.get(r.toolCallId);
			chat.items.push({ key, kind: 'permission', req: r, tool: tool ?? { kind: 'tool', call, children: [], seq: 0n }, votes: {} });
			// The CLI may ask before it streams the call: the card points at it once it arrives.
			if (!tool) chat.orphans.cards.set(r.toolCallId, chat.items.at(-1) as Extract<Item, { kind: 'permission' }>);
			break;
		}
		case 'permissionResolved': {
			const id = k.value.id;
			const req = chat.items.findLast((i): i is Extract<Item, { kind: 'permission' }> => i.kind === 'permission' && i.req.id === id);
			if (req) req.resolved = { ...k.value, by: e.actor };
			else chat.orphans.answers.set(id, { ...k.value, by: e.actor }); // its request is in older history
			break;
		}
		case 'permissionVoted': {
			const { id, answers } = k.value;
			const req = chat.items.findLast((i): i is Extract<Item, { kind: 'permission' }> => i.kind === 'permission' && i.req.id === id);
			const votes = req?.votes ?? chat.orphans.votes.get(id) ?? {};
			if (Object.keys(answers).length) votes[e.actor] = answers;
			else delete votes[e.actor];
			if (!req) chat.orphans.votes.set(id, votes);
			break;
		}
		case 'permissionRevoked':
			chat.items.push({ key, kind: 'revoked', actor: e.actor, rule: k.value.rule });
			break;
		case 'turnEnded':
			chat.streaming = '';
			chat.items.push({ key, kind: 'turn', turn: k.value, diff: chat.diffAhead || undefined });
			chat.diffAhead = false;
			break;
		case 'setupStep': {
			// A checklist: a step joins the last item's checklist, or replaces its running step with the same id.
			// A step that already finished there means setup runs again (resume, retry): a new checklist starts.
			const step = k.value;
			const last = chat.items.at(-1);
			const at = last?.kind === 'setup' ? last.steps.findIndex((s) => s.id === step.id) : -1;
			if (last?.kind !== 'setup' || (at >= 0 && last.steps[at].state !== SetupState.RUNNING))
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
		case 'autoPush': {
			const unavailable = k.value.state === AutoPushState.UNAVAILABLE;
			if (unavailable && chat.items.some((i) => i.kind === 'push' && i.unavailable)) break;
			chat.items.push({ key, kind: 'push', text: k.value.detail, pushed: k.value.state === AutoPushState.PUSHED, unavailable });
			break;
		}
		case 'modeChanged':
			chat.items.push({ key, kind: 'mode', actor: e.actor, mode: k.value.mode, reason: k.value.reason });
			break;
		case 'portOpened':
			chat.items.push({ key, kind: 'port', port: k.value });
			break;
		case 'diffChanged': {
			// The runner reports a turn's diff right before its end; history from before 2026-10-23 has it right after.
			const last = chat.items.at(-1);
			if (last?.kind === 'turn') last.diff = true;
			else chat.diffAhead = true;
			break;
		}
		case 'agentSettings':
			chat.items.push({ key, kind: 'settings', actor: e.actor, settings: k.value });
			break;
		case 'repoAdded':
			chat.items.push({ key, kind: 'repo', actor: e.actor, repo: k.value });
			break;
		case 'threadRenamed':
			chat.items.push({ key, kind: 'renamed', actor: e.actor, title: k.value.title });
			break;
		case 'permissionModeChanged':
			chat.items.push({ key, kind: 'permissionMode', actor: e.actor, mode: k.value.mode });
			break;
		case 'forkNote':
			chat.items.push({ key, kind: 'fork', actor: e.actor, note: k.value });
			break;
		case 'handoffOffered':
			chat.items.push({ key, kind: 'handoff', seq: e.seq, actor: e.actor, offer: k.value, time: e.time });
			break;
		case 'handoffResolved': {
			const seq = k.value.offerSeq;
			const offer = chat.items.find((i): i is Extract<Item, { kind: 'handoff' }> => i.kind === 'handoff' && i.seq === seq);
			if (offer) offer.resolved = { ...k.value, by: e.actor };
			else chat.orphans.handoffs.set(seq, { ...k.value, by: e.actor }); // its offer is in older history
			break;
		}
	}
}

// Prepends older history (the events right before the oldest loaded one, in seq order), giving the
// same chat as applying everything in order: a group split by the page edge is joined, and calls and
// results waiting for their tool call are attached.
// ponytail: a setup checklist split by the page edge stays two checklists, and a turn split from its
// diffChanged by the page edge has no "View diff".
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
		if (item.kind === 'handoff' && chat.orphans.handoffs.has(item.seq)) {
			item.resolved = chat.orphans.handoffs.get(item.seq);
			chat.orphans.handoffs.delete(item.seq);
		}
		if (item.kind === 'permission' && chat.orphans.answers.has(item.req.id)) {
			item.resolved = chat.orphans.answers.get(item.req.id);
			chat.orphans.answers.delete(item.req.id);
		}
		if (item.kind === 'permission' && chat.orphans.votes.has(item.req.id)) {
			item.votes = { ...item.votes, ...chat.orphans.votes.get(item.req.id) };
			chat.orphans.votes.delete(item.req.id);
		}
	}
	// A permission card made before its call was loaded points at the call now.
	for (const item of chat.items)
		if (item.kind === 'permission' && !item.tool.seq && chat.tools.has(item.req.toolCallId)) {
			item.tool = chat.tools.get(item.req.toolCallId)!;
			chat.orphans.cards.delete(item.req.toolCallId);
		}
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
	for (const [seq, r] of old.orphans.handoffs) chat.orphans.handoffs.set(seq, r);
	for (const [id, r] of old.orphans.answers) chat.orphans.answers.set(id, r);
	for (const [id, o] of old.orphans.results) chat.orphans.results.set(id, o);
	for (const [id, kids] of old.orphans.calls) chat.orphans.calls.set(id, [...kids, ...(chat.orphans.calls.get(id) ?? [])]);
}
