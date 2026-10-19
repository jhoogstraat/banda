<script lang="ts">
	import { page } from '$app/state';
	import ArrowLeftRightIcon from '@lucide/svelte/icons/arrow-left-right';
	import BrainIcon from '@lucide/svelte/icons/brain';
	import PanelRightIcon from '@lucide/svelte/icons/panel-right';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import SquareIcon from '@lucide/svelte/icons/square';
	import {
		Conversation,
		ConversationContent,
		ConversationEmptyState,
		ConversationScrollButton
	} from '#lib/components/ai-elements/conversation/index.js';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import { Message, MessageContent, MessageResponse } from '#lib/components/ai-elements/message/index.js';
	import { Response } from '#lib/components/ai-elements/response/index.js';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import { Reasoning, ReasoningContent, ReasoningTrigger } from '#lib/components/ai-elements/reasoning/index.js';
	import Tool from '#lib/components/ai-elements/tool/tool.svelte';
	import ToolContent from '#lib/components/ai-elements/tool/tool-content.svelte';
	import ToolHeader from '#lib/components/ai-elements/tool/tool-header.svelte';
	import ToolInput from '#lib/components/ai-elements/tool/tool-input.svelte';
	import ToolOutput from '#lib/components/ai-elements/tool/tool-output.svelte';
	import MentionInput from '#lib/components/mention-input.svelte';
	import MentionText from '#lib/components/mention-text.svelte';
	import StatusDot, { statusLabel } from '#lib/components/status-dot.svelte';
	import ThreadActions from '#lib/components/thread-actions.svelte';
	import ThreadBanners from '#lib/components/thread-banners.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Tabs, TabsContent, TabsList, TabsTrigger } from '#lib/components/ui/tabs/index.js';
	import { api } from '#lib/api.js';
	import { apply, type Chat } from '#lib/events.js';
	import { Mode, Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { loadThreads, threads, userName } from '#lib/threads.svelte.js';

	// The layout re-creates this page for every thread, so the id never changes.
	const id = page.params.id!;

	let thread = $state<Thread>();
	const chat = $state<Chat>({ items: [], streaming: '' });
	let diffOpen = $state(true);
	let error = $state('');
	let offline = $state(false);
	let picked = $state<{ id: string; name: string }[]>([]);
	const owner = $derived(threads.me?.id === thread?.ownerId);

	const modeName = (m: Mode) => Mode[m].toLowerCase();
	const json = (s: string) => {
		try {
			return JSON.parse(s);
		} catch {
			return s;
		}
	};
	const diffClass = (l: string) =>
		/^(@@)/.test(l)
			? 'hunk'
			: /^(\+\+\+|---|diff |index )/.test(l)
				? 'meta'
				: l[0] === '+'
					? 'add'
					: l[0] === '-'
						? 'del'
						: '';

	async function load() {
		try {
			thread = (await api.getThread({ threadId: id })).thread;
			loadThreads().catch(() => {});
		} catch (e) {
			error = (e as Error).message;
		}
	}

	// Coalesces the burst of status events that a replay produces into one GetThread.
	let timer: ReturnType<typeof setTimeout>;
	const refresh = () => {
		clearTimeout(timer);
		timer = setTimeout(load, 200);
	};

	async function watch(signal: AbortSignal) {
		let seq = 0n;
		while (!signal.aborted) {
			chat.streaming = ''; // deltas are not replayed, drop a half-finished bubble
			try {
				for await (const { event } of api.watchThread({ threadId: id, afterSeq: seq }, { signal })) {
					if (!event) continue;
					offline = false;
					if (event.seq) seq = event.seq;
					apply(chat, event);
					if (['diffChanged', 'statusChanged', 'modeChanged'].includes(event.kind.case!)) refresh();
				}
			} catch {
				if (signal.aborted) return;
			}
			offline = true;
			await new Promise((r) => setTimeout(r, 1000));
		}
	}

	$effect(() => {
		const ctrl = new AbortController();
		load();
		watch(ctrl.signal);
		return () => {
			ctrl.abort();
			clearTimeout(timer);
		};
	});

	async function act(call: Promise<unknown>) {
		try {
			await call;
			error = '';
			refresh();
			return true;
		} catch (e) {
			error = (e as Error).message;
			return false;
		}
	}

	async function send({ text }: { text: string }) {
		if (!text.trim()) return;
		const message = {
			text,
			mentionsAgent: /@agent\b/.test(text),
			mentionedUserIds: picked.filter((u) => text.includes('@' + u.name)).map((u) => u.id)
		};
		// Throwing keeps the text in the prompt box so it can be retried.
		if (!(await act(api.sendMessage({ threadId: id, message })))) throw new Error(error);
	}
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<header class="flex h-14 shrink-0 items-center gap-3 border-b px-4">
		<h1 class="truncate font-medium">{thread?.title || 'Untitled thread'}</h1>
		{#if thread}
			<StatusDot status={thread.status} label />
			<Button
				variant="outline"
				size="sm"
				title="Toggle agent / chat mode"
				disabled={!owner}
				onclick={() =>
					act(api.setMode({ threadId: id, mode: thread!.mode === Mode.AGENT ? Mode.CHAT : Mode.AGENT }))}
			>
				{modeName(thread.mode)} <ArrowLeftRightIcon />
			</Button>
			<Badge variant="secondary">{thread.model}</Badge>
			<Badge variant="outline">{thread.effort}</Badge>
		{/if}
		{#if offline}<span class="text-xs text-muted-foreground">reconnecting…</span>{/if}
		<div class="ml-auto flex items-center gap-2">
			{#if thread}<ThreadActions {thread} {act} />{/if}
			{#if thread?.status === Status.WORKING}
				<Button variant="destructive" size="sm" onclick={() => act(api.interrupt({ threadId: id }))}>
					<SquareIcon /> Interrupt
				</Button>
			{:else if thread?.status === Status.ERRORED}
				<Button size="sm" onclick={() => act(api.retry({ threadId: id }))}><RotateCcwIcon /> Retry</Button>
			{/if}
			<Button variant="ghost" size="icon" aria-label="Toggle right pane" onclick={() => (diffOpen = !diffOpen)}>
				<PanelRightIcon />
			</Button>
		</div>
	</header>

	<Conversation class="min-h-0 flex-1">
		<ConversationContent class="min-h-0 flex-1 overflow-y-auto">
			<div class="mx-auto flex w-full max-w-3xl flex-col gap-4">
				{#each chat.items as item, i (i)}
					{#if item.kind === 'message'}
						<Message from="user">
							<span class="ml-auto text-xs text-muted-foreground">{userName(item.actor)}</span>
							<MessageContent class="whitespace-pre-wrap"><MentionText text={item.text} /></MessageContent>
						</Message>
					{:else if item.kind === 'text'}
						<Message from="assistant">
							<MessageContent><MessageResponse content={item.text} /></MessageContent>
						</Message>
					{:else if item.kind === 'thinking'}
						<Reasoning defaultOpen={false} class="mb-0">
							<ReasoningTrigger><BrainIcon class="size-4" /> Thinking</ReasoningTrigger>
							<ReasoningContent content={item.text} />
						</Reasoning>
					{:else if item.kind === 'tool'}
						{@const r = item.result}
						<Tool class="mb-0">
							<ToolHeader
								type={item.call.name}
								state={r ? (r.isError ? 'output-error' : 'output-available') : 'input-available'}
							/>
							<ToolContent>
								<ToolInput input={json(item.call.inputJson)} />
								{#if r}
									<ToolOutput
										output={r.isError ? undefined : r.output}
										errorText={r.isError ? r.output || 'Error' : undefined}
									/>
								{/if}
							</ToolContent>
						</Tool>
					{:else if item.kind === 'turn'}
						<p class="text-center text-xs {item.turn.isError ? 'text-destructive' : 'text-muted-foreground'}">
							{#if item.turn.isError}
								Turn failed: {item.turn.error}
							{:else}
								Turn finished in {(Number(item.turn.durationMs) / 1000).toFixed(1)}s · ${item.turn.costUsd.toFixed(4)}
							{/if}
						</p>
					{:else if item.kind === 'notice'}
						<div class="rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm">
							<Response content={item.text} />
						</div>
					{:else if item.kind === 'status'}
						<p class="text-center text-xs text-muted-foreground/70">
							status: {statusLabel(item.status)}{item.reason ? ` (${item.reason})` : ''}
						</p>
					{:else if item.kind === 'mode'}
						<p class="text-center text-xs text-muted-foreground/70">mode: {modeName(item.mode)}</p>
					{/if}
				{/each}

				{#if chat.streaming}
					<Message from="assistant">
						<MessageContent><MessageResponse content={chat.streaming} /></MessageContent>
					</Message>
				{:else if thread?.status === Status.WORKING}
					<div class="flex items-center gap-2 text-sm text-muted-foreground"><Loader /> Working…</div>
				{:else if !chat.items.length}
					<ConversationEmptyState
						title={thread?.status === Status.STARTING ? 'Starting environment…' : 'No messages yet'}
						description=""
					/>
				{/if}
			</div>
		</ConversationContent>
		<ConversationScrollButton />
	</Conversation>

	<div class="mx-auto w-full max-w-3xl shrink-0 px-4 pb-4">
		{#if error}<p class="mb-2 text-sm text-destructive">{error}</p>{/if}
		{#if thread}<ThreadBanners {thread} {act} />{/if}
		<PromptInput.Root onSubmit={send}>
			<fieldset disabled={thread?.status === Status.STARTING} class="contents">
				<PromptInput.Body>
					<MentionInput bind:picked placeholder="Message the agent. Use @agent to mention it in chat mode." />
				</PromptInput.Body>
				<PromptInput.Toolbar class="justify-end"><PromptInput.Submit /></PromptInput.Toolbar>
			</fieldset>
		</PromptInput.Root>
	</div>
</div>

{#if diffOpen}
	<aside class="flex w-[32rem] max-w-[45%] shrink-0 flex-col border-l">
		<Tabs value="diff" class="min-h-0 flex-1 gap-0">
			<TabsList variant="line" class="group-data-horizontal/tabs:h-14 w-full shrink-0 justify-start border-b px-2">
				<TabsTrigger value="diff">Diff</TabsTrigger>
			</TabsList>
			<TabsContent value="diff" class="min-h-0 overflow-auto">
				{#if thread?.diff}
					<pre class="diff">{#each thread.diff.split('\n') as line}<span class={diffClass(line)}>{line || ' '}</span>{/each}</pre>
				{:else}
					<p class="p-4 text-sm text-muted-foreground">No changes yet.</p>
				{/if}
			</TabsContent>
		</Tabs>
	</aside>
{/if}

<style>
	.diff {
		min-width: max-content;
		padding: 0.5rem 0;
		font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
	}
	.diff span {
		display: block;
		padding: 0 1rem;
	}
	.add {
		background: rgb(46 160 67 / 0.15);
		color: #7ee787;
	}
	.del {
		background: rgb(248 81 73 / 0.15);
		color: #ffa198;
	}
	.hunk {
		background: rgb(56 139 253 / 0.12);
		color: #79c0ff;
	}
	.meta {
		color: var(--muted-foreground);
		font-weight: 600;
	}
</style>
