<script lang="ts">
	import { page } from '$app/state';
	import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
	import BotIcon from '@lucide/svelte/icons/bot';
	import BrainIcon from '@lucide/svelte/icons/brain';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import MessagesSquareIcon from '@lucide/svelte/icons/messages-square';
	import PanelRightIcon from '@lucide/svelte/icons/panel-right';
	import PauseIcon from '@lucide/svelte/icons/pause';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import SquareIcon from '@lucide/svelte/icons/square';
	import UploadIcon from '@lucide/svelte/icons/upload';
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
	import DiffView from '#lib/components/diff-view.svelte';
	import FileBrowser from '#lib/components/file-browser.svelte';
	import IconButton from '#lib/components/icon-button.svelte';
	import MentionInput from '#lib/components/mention-input.svelte';
	import MentionText from '#lib/components/mention-text.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import ThreadActions from '#lib/components/thread-actions.svelte';
	import ThreadBanners from '#lib/components/thread-banners.svelte';
	import Terminal from '#lib/components/terminal.svelte';
	import ToolCall from '#lib/components/tool-call.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
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
	// The terminal and the file browser mount when their tab is first opened and then stay alive; a key change reopens the terminal.
	let tab = $state('diff');
	let termOpened = $state(false);
	let filesOpened = $state(false);
	let termKey = $state(0);
	let termCommand = $state('');
	$effect(() => {
		if (tab === 'terminal') termOpened = true;
		if (tab === 'files') filesOpened = true;
	});
	const loginNeeded = (text: string) => /not logged in|rejected the login/.test(text);
	function login() {
		termCommand = 'claude auth login';
		termKey++;
		tab = 'terminal';
		diffOpen = true;
	}
	let error = $state('');
	let offline = $state(false);
	const owner = $derived(!!threads.me && threads.me.id === thread?.ownerId);

	const clock = (t?: Timestamp) =>
		t ? timestampDate(t).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }) : '';

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
			// Typed or picked from the popup: anyone whose @name is in the text.
			mentionedUserIds: threads.users.filter((u) => u.id !== threads.me?.id && text.includes('@' + u.name)).map((u) => u.id)
		};
		// Throwing keeps the text in the prompt box so it can be retried.
		if (!(await act(api.sendMessage({ threadId: id, message })))) throw new Error(error);
	}
</script>

{#snippet divider(text: string, tone = 'text-muted-foreground')}
	<div class="flex items-center gap-3 text-xs {tone}">
		<div class="h-px flex-1 bg-border"></div>
		{text}
		<div class="h-px flex-1 bg-border"></div>
	</div>
{/snippet}

{#snippet agentText(text: string)}
	<Message from="assistant" class="max-w-full">
		<MessageContent class="w-full"><MessageResponse content={text} /></MessageContent>
	</Message>
{/snippet}

<div class="flex min-w-0 flex-1 flex-col">
	<header class="flex h-12 shrink-0 items-center gap-3 border-b px-4">
		<h1 class="min-w-0 truncate text-sm font-medium">{thread?.title || 'Untitled thread'}</h1>
		{#if thread}<StatusDot status={thread.status} label />{/if}
		{#if offline}<span class="shrink-0 text-xs text-muted-foreground">reconnecting…</span>{/if}
		<div class="ml-auto flex shrink-0 items-center gap-0.5">
			{#if thread?.status === Status.WORKING}
				<IconButton icon={SquareIcon} label="Interrupt the agent" variant="destructive" onclick={() => act(api.interrupt({ threadId: id }))} />
			{/if}
			{#if thread}
				<IconButton
					icon={thread.mode === Mode.CHAT ? MessagesSquareIcon : BotIcon}
					label={thread.mode === Mode.CHAT ? 'Chat mode: switch to agent mode' : 'Agent mode: switch to chat mode'}
					variant={thread.mode === Mode.CHAT ? 'secondary' : 'ghost'}
					disabled={!owner}
					onclick={() => act(api.setMode({ threadId: id, mode: thread!.mode === Mode.AGENT ? Mode.CHAT : Mode.AGENT }))}
				/>
				<ThreadActions {thread} {act} />
			{/if}
			{#if owner}
				<IconButton icon={UploadIcon} label="Push the branch to origin" onclick={() => act(api.push({ threadId: id }))} />
			{/if}
			<IconButton icon={PanelRightIcon} label="Toggle right pane" onclick={() => (diffOpen = !diffOpen)} />
		</div>
	</header>

	<Conversation class="min-h-0 flex-1">
		<ConversationContent class="min-h-0 flex-1 overflow-y-auto">
			<div class="mx-auto flex w-full max-w-3xl flex-col gap-4">
				{#each chat.items as item, i (i)}
					{#if item.kind === 'message'}
						{@const name = userName(item.actor)}
						<div class="flex gap-3">
							<UserAvatar {name} class="mt-0.5 size-7" />
							<div class="min-w-0 flex-1 space-y-1">
								<div class="flex items-baseline gap-2">
									<span class="text-sm font-medium">{name}</span>
									<time class="text-xs text-muted-foreground" title={item.time && timestampDate(item.time).toLocaleString()}>{clock(item.time)}</time>
								</div>
								<Message from="user" class="ml-0 max-w-full">
									<MessageContent class="group-[.is-user]:ml-0"><p class="whitespace-pre-wrap"><MentionText text={item.text} /></p></MessageContent>
								</Message>
							</div>
						</div>
					{:else if item.kind === 'text'}
						{@render agentText(item.text)}
					{:else if item.kind === 'thinking'}
						<Reasoning defaultOpen={false} class="mb-0">
							<ReasoningTrigger><BrainIcon class="size-4" /> Thinking</ReasoningTrigger>
							<ReasoningContent content={item.text} />
						</Reasoning>
					{:else if item.kind === 'tool'}
						<ToolCall call={item.call} result={item.result} />
					{:else if item.kind === 'turn'}
						{#if item.turn.isError}
							{@render divider(`Turn failed: ${item.turn.error}`, 'text-destructive')}
						{:else}
							{@render divider(`Turn finished in ${(Number(item.turn.durationMs) / 1000).toFixed(1)}s · $${item.turn.costUsd.toFixed(4)}`)}
						{/if}
					{:else if item.kind === 'notice'}
						<div class="rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm">
							<Response content={item.text} class="h-auto" />
							{#if owner && loginNeeded(item.text)}
								<Button size="sm" class="mt-2" onclick={login}>Log in to Claude Code</Button>
							{/if}
						</div>
					{:else if item.kind === 'mode'}
						{@render divider(item.mode === Mode.CHAT ? 'Chat mode on' : 'Agent mode')}
					{/if}
				{/each}

				{#if chat.streaming}{@render agentText(chat.streaming)}{/if}
				{#if thread?.status === Status.STARTING}
					<div class="flex items-center gap-2 text-sm text-muted-foreground"><Loader /> Starting environment…</div>
				{:else if thread?.status === Status.WORKING}
					<div class="flex items-center gap-2 text-sm text-muted-foreground"><Loader /> <span class="animate-pulse">Working…</span></div>
				{:else if thread?.status === Status.SUSPENDED}
					<div class="flex items-center gap-2 text-sm text-muted-foreground"><PauseIcon class="size-4" /> Environment paused. Sending a message resumes it.</div>
				{:else if thread?.status === Status.ERRORED}
					<div class="flex items-center gap-3 rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm">
						<CircleAlertIcon class="size-4 shrink-0 text-destructive" />
						<span class="flex-1">The environment failed or was lost.</span>
						{#if owner}
							<Button size="sm" onclick={() => act(api.retry({ threadId: id }))}><RotateCcwIcon /> Retry</Button>
						{/if}
					</div>
				{:else if !chat.items.length}
					<ConversationEmptyState title="No messages yet" description="" />
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
					<MentionInput placeholder="Message the agent. Use @agent to mention it in chat mode." />
				</PromptInput.Body>
				<PromptInput.Toolbar>
					<div class="flex items-center gap-1.5 pl-2">
						{#if thread}
							<Badge variant="outline" class="font-normal text-muted-foreground"><CpuIcon /> {thread.model}</Badge>
							<Badge variant="outline" class="font-normal text-muted-foreground"><BrainIcon /> {thread.effort}</Badge>
						{/if}
					</div>
					<PromptInput.Submit />
				</PromptInput.Toolbar>
			</fieldset>
		</PromptInput.Root>
	</div>
</div>

<!-- Hidden rather than removed, so closing the pane keeps the terminal running. -->
<aside class="{diffOpen ? 'flex' : 'hidden'} w-[32rem] max-w-[45%] shrink-0 flex-col border-l">
	<Tabs bind:value={tab} class="min-h-0 flex-1 gap-0">
		<TabsList variant="line" class="group-data-horizontal/tabs:h-12 w-full shrink-0 justify-start border-b px-2">
			<TabsTrigger value="diff">Diff</TabsTrigger>
			<TabsTrigger value="files">Files</TabsTrigger>
			<TabsTrigger value="terminal">Terminal</TabsTrigger>
		</TabsList>
		<TabsContent value="diff" class="min-h-0 overflow-auto">
			<DiffView diff={thread?.diff ?? ''} />
		</TabsContent>
		<TabsContent value="files" class="min-h-0 overflow-auto">
			{#if filesOpened}<FileBrowser threadId={id} diff={thread?.diff ?? ''} />{/if}
		</TabsContent>
		{#if owner && termOpened}
			<div class="min-h-0 flex-1 {tab === 'terminal' ? '' : 'hidden'}">
				{#key termKey}<Terminal threadId={id} command={termCommand} />{/key}
			</div>
		{:else if tab === 'terminal'}
			<p class="p-4 text-sm text-muted-foreground">Only the thread owner can open the terminal.</p>
		{/if}
	</Tabs>
</aside>
