<script lang="ts">
	import { page } from '$app/state';
	import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
	import BotIcon from '@lucide/svelte/icons/bot';
	import BrainIcon from '@lucide/svelte/icons/brain';
	import CheckIcon from '@lucide/svelte/icons/check';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import CircleMinusIcon from '@lucide/svelte/icons/circle-minus';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import MessagesSquareIcon from '@lucide/svelte/icons/messages-square';
	import PanelRightIcon from '@lucide/svelte/icons/panel-right';
	import XIcon from '@lucide/svelte/icons/x';
	import BellOffIcon from '@lucide/svelte/icons/bell-off';
	import FileDiffIcon from '@lucide/svelte/icons/file-diff';
	import FolderTreeIcon from '@lucide/svelte/icons/folder-tree';
	import SquareTerminalIcon from '@lucide/svelte/icons/square-terminal';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
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
	import type { PromptInputMessage } from '#lib/components/ai-elements/prompt-input/index.js';
	import Tool from '#lib/components/ai-elements/tool/tool.svelte';
	import ToolContent from '#lib/components/ai-elements/tool/tool-content.svelte';
	import Browser from '#lib/components/browser.svelte';
	import DiffView from '#lib/components/diff-view.svelte';
	import FileBrowser from '#lib/components/file-browser.svelte';
	import IconButton from '#lib/components/icon-button.svelte';
	import LoginDialog from '#lib/components/login-dialog.svelte';
	import MentionInput from '#lib/components/mention-input.svelte';
	import MentionText from '#lib/components/mention-text.svelte';
	import OptionMenu, { efforts, models } from '#lib/components/option-menu.svelte';
	import MessageAttachments from '#lib/components/message-attachments.svelte';
	import AttachButton from '#lib/components/attach-button.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import SidePane from '#lib/components/side-pane.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import ThreadActions, { isSnoozed, presets, settle, snooze } from '#lib/components/thread-actions.svelte';
	import ThreadBanners from '#lib/components/thread-banners.svelte';
	import Terminal from '#lib/components/terminal.svelte';
	import Plan from '#lib/components/plan.svelte';
	import ToolSteps from '#lib/components/tool-steps.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '#lib/components/ui/collapsible/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Tabs, TabsContent, TabsList, TabsTrigger } from '#lib/components/ui/tabs/index.js';
	import { Tooltip, TooltipContent, TooltipTrigger } from '#lib/components/ui/tooltip/index.js';
	import { api, upload } from '#lib/api.js';
	import { tone, where, type Comment } from '#lib/diff.js';
	import { apply, newChat, typingNow, type Chat } from '#lib/events.js';
	import { Mode, SetupState, Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { ui, visited, type Command } from '#lib/shortcuts.svelte.js';
	import { threads, userName } from '#lib/threads.svelte.js';

	// The layout re-creates this page for every thread, so the id never changes.
	const id = page.params.id!;
	visited(id);

	let thread = $state<Thread>();
	const chat = $state<Chat>(newChat());
	// Unset: the pane picks its own default (open on desktop, closed on phones).
	let diffOpen = $state<boolean>();
	// The terminal, the file browser and the browser mount when their tab is first opened and then stay alive.
	let tab = $state('diff');
	let termOpened = $state(false);
	let filesOpened = $state(false);
	let browserOpened = $state(false);
	$effect(() => {
		if (tab === 'terminal') termOpened = true;
		if (tab === 'files') filesOpened = true;
		if (tab === 'browser') browserOpened = true;
	});
	const loginNeeded = (text: string) => /not logged in|rejected the login/.test(text);
	let loginOpen = $state(false);
	let error = $state('');
	// The reason of the latest status change, e.g. why the environment failed.
	let reason = $state('');
	let offline = $state(false);
	const owner = $derived(!!threads.me && threads.me.id === thread?.ownerId);
	// The owner's optional decline note per turn request seq.
	const notes = $state<Record<string, string>>({});
	// Ticks so typing expires; `others` drops the viewer from presence and typing.
	let now = $state(Date.now());
	const others = (ids: string[]) => ids.filter((u) => u !== threads.me?.id);
	const here = $derived(others(chat.present));
	const typing = $derived(others(typingNow(chat, now)).map(userName));
	const typingText = $derived(
		typing.length > 2 ? `${typing.length} people are typing…` : typing.length ? `${typing.join(' and ')} ${typing.length > 1 ? 'are' : 'is'} typing…` : ''
	);
	// The composer reports typing at most every 2.5 s; posting resets it.
	let typedAt = 0;
	function typed() {
		if (Date.now() - typedAt < 2500) return;
		typedAt = Date.now();
		api.sendTyping({ threadId: id }).catch(() => {});
	}

	// The review being written in the Diff tab, kept in this browser until it is sent.
	const reviewKey = `banda-review-${id}`;
	let pending = $state<Comment[]>(JSON.parse(localStorage.getItem(reviewKey) ?? '[]'));
	$effect(() => localStorage.setItem(reviewKey, JSON.stringify(pending)));
	// Ask the agent to address the review: on by default for the owner.
	let ask = $state(false);
	$effect(() => {
		ask = owner;
	});
	let focus = $state<Comment>();
	const posted = $derived(chat.items.flatMap((i) => (i.kind === 'message' || i.kind === 'request' ? i.review.map((c) => ({ ...c, by: i.actor })) : [])));
	function reveal(c: Comment) {
		show('diff');
		focus = c;
	}

	const clock = (t?: Timestamp) =>
		t ? timestampDate(t).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }) : '';

	async function load() {
		try {
			thread = (await api.getThread({ threadId: id })).thread;
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

	// Marks the thread read while it is on screen, after a burst of events or when the tab comes back.
	let readTimer: ReturnType<typeof setTimeout>;
	const markRead = () => {
		clearTimeout(readTimer);
		readTimer = setTimeout(() => !document.hidden && api.inboxMarkRead({ threadId: id }).catch(() => {}), 500);
	};

	async function watch(signal: AbortSignal) {
		let seq = 0n;
		while (!signal.aborted) {
			chat.streaming = ''; // deltas are not replayed, drop a half-finished bubble
			try {
				for await (const { event } of api.watchThread({ threadId: id, afterSeq: seq }, { signal })) {
					if (!event) continue;
					offline = false;
					if (event.seq) {
						seq = event.seq;
						markRead();
					}
					apply(chat, event);
					if (event.kind.case === 'statusChanged') reason = event.kind.value.reason;
					if (['diffChanged', 'statusChanged', 'modeChanged', 'turnRequest', 'turnRequestResolved', 'notice', 'agentSettings'].includes(event.kind.case!)) refresh();
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
		const tick = setInterval(() => (now = Date.now()), 1000);
		return () => {
			ctrl.abort();
			clearTimeout(timer);
			clearTimeout(readTimer);
			clearInterval(tick);
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

	function setAgent(change: { model?: string; effort?: string }) {
		Object.assign(thread!, change); // the chip shows the choice right away
		act(api.setAgentSettings({ threadId: id, ...change }));
	}
	// Shared by the header buttons and the command palette.
	const interrupt = () => act(api.interrupt({ threadId: id }));
	const toggleMode = () => act(api.setMode({ threadId: id, mode: thread!.mode === Mode.AGENT ? Mode.CHAT : Mode.AGENT }));
	const push = () => act(api.push({ threadId: id }));
	const retry = () => act(api.retry({ threadId: id }));
	const show = (t: string) => {
		tab = t;
		diffOpen = true;
	};

	// The palette's "This thread" actions, each only where it applies.
	$effect(() => {
		const t = thread;
		if (!t) return;
		const c: Command[] = [
			{ label: 'Toggle right pane', icon: PanelRightIcon, run: () => (diffOpen = !diffOpen) },
			{ label: 'Show diff', icon: FileDiffIcon, run: () => show('diff') },
			{ label: 'Show files', icon: FolderTreeIcon, run: () => show('files') }
		];
		if (owner) c.push({ label: 'Open terminal', icon: SquareTerminalIcon, run: () => show('terminal') });
		if (isSnoozed(t)) c.push({ label: 'Unsnooze', icon: BellOffIcon, run: () => act(snooze(t)) });
		else
			for (const [label, date] of [presets[0], presets[2]])
				c.push({ label: `Snooze ${label === '1 hour' ? 'for 1 hour' : `until ${label.toLowerCase()}`}`, icon: BellOffIcon, run: () => act(snooze(t, date())) });
		c.push({ label: t.settled ? 'Unsettle' : 'Settle', icon: CheckIcon, run: () => act(settle(t)) });
		if (owner && t.status === Status.WORKING) c.push({ label: 'Interrupt the agent', icon: SquareIcon, run: interrupt });
		if (owner && t.status === Status.ERRORED) c.push({ label: 'Retry', icon: RotateCcwIcon, run: retry });
		if (owner) {
			c.push({ label: 'Push the branch to origin', icon: UploadIcon, run: push });
			c.push(t.mode === Mode.CHAT
				? { label: 'Switch to agent mode', icon: BotIcon, run: toggleMode }
				: { label: 'Switch to chat mode', icon: MessagesSquareIcon, run: toggleMode });
		}
		ui.commands = c;
		// When the next thread page has already registered its own, leave them.
		return () => {
			if (!page.params.id || page.params.id === id) ui.commands = [];
		};
	});

	function message(text: string, review: Comment[] = []) {
		const all = [text, ...review.map((c) => c.body)].join('\n');
		return {
			text,
			review,
			mentionsAgent: /@agent\b/.test(text),
			// Typed or picked from the popup: anyone whose @name is in the text.
			mentionedUserIds: threads.users.filter((u) => u.id !== threads.me?.id && all.includes('@' + u.name)).map((u) => u.id)
		};
	}

	async function send({ text, attachments: files }: PromptInputMessage) {
		if (!text.trim() && !files.length) return;
		let attachments;
		try {
			attachments = await Promise.all(files.map((f) => upload(f.file)));
		} catch (e) {
			error = (e as Error).message;
			throw e;
		}
		// Throwing keeps the text and files in the prompt box so they can be retried.
		if (!(await act(api.sendMessage({ threadId: id, message: { ...message(text), attachments } })))) throw new Error(error);
		typedAt = 0;
	}

	async function sendReview(summary: string) {
		let text = summary.trim();
		if (ask && !/@agent\b/.test(text)) text = `@agent ${text || 'please address these review comments.'}`;
		const ok = await act(api.sendMessage({ threadId: id, message: message(text, pending) }));
		if (ok) pending = [];
		return ok;
	}
</script>

<svelte:document onvisibilitychange={markRead} />

{#snippet reviewCards(review: Comment[])}
	{#each review as c, j (j)}
		<Collapsible open class="group/rc overflow-hidden rounded-lg border text-sm">
			<div class="flex items-center gap-1 bg-muted/40 px-2 py-1 text-xs">
				<CollapsibleTrigger class="rounded p-0.5 hover:bg-muted" aria-label="Toggle the code">
					<ChevronRightIcon class="size-3.5 text-muted-foreground transition-transform group-data-[state=open]/rc:rotate-90" />
				</CollapsibleTrigger>
				<button class="min-w-0 truncate font-mono hover:underline" title="Show in the diff" onclick={() => reveal(c)}>{where(c)}</button>
			</div>
			<CollapsibleContent>
				<pre class="max-h-48 overflow-auto border-t py-1 font-mono text-xs leading-5">{#each c.code.split('\n') as line}<div class="px-3 {tone(line)}">{line || ' '}</div>{/each}</pre>
			</CollapsibleContent>
			<p class="border-t px-3 py-2 whitespace-pre-wrap"><MentionText text={c.body} /></p>
		</Collapsible>
	{/each}
{/snippet}

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
	<PageHeader>
		<h1 class="min-w-0 truncate text-sm font-medium">{thread?.title || 'Untitled thread'}</h1>
		{#if thread}<StatusDot status={thread.status} label />{/if}
		{#if offline}<span class="shrink-0 text-xs text-muted-foreground">reconnecting…</span>{/if}
		<div class="ml-auto flex shrink-0 items-center gap-0.5">
			{#if here.length}
				<div class="mr-1.5 flex -space-x-1.5" aria-label="Here now">
					{#each here.slice(0, 4) as u (u)}
						<Tooltip>
							<TooltipTrigger>
								{#snippet child({ props })}
									<span {...props}><UserAvatar name={userName(u)} class="size-6 ring-2 ring-background" /></span>
								{/snippet}
							</TooltipTrigger>
							<TooltipContent>{userName(u)} · here now</TooltipContent>
						</Tooltip>
					{/each}
					{#if here.length > 4}
						<Tooltip>
							<TooltipTrigger>
								{#snippet child({ props })}
									<span {...props} class="flex size-6 items-center justify-center rounded-full bg-muted text-[10px] font-medium ring-2 ring-background">+{here.length - 4}</span>
								{/snippet}
							</TooltipTrigger>
							<TooltipContent>{here.slice(4).map(userName).join(', ')} · here now</TooltipContent>
						</Tooltip>
					{/if}
				</div>
			{/if}
			{#if owner && thread?.status === Status.WORKING}
				<IconButton icon={SquareIcon} label="Interrupt the agent" variant="destructive" onclick={interrupt} />
			{/if}
			{#if thread}
				<IconButton
					icon={thread.mode === Mode.CHAT ? MessagesSquareIcon : BotIcon}
					label={thread.mode === Mode.CHAT ? 'Chat mode: switch to agent mode' : 'Agent mode: switch to chat mode'}
					variant={thread.mode === Mode.CHAT ? 'secondary' : 'ghost'}
					disabled={!owner}
					onclick={toggleMode}
				/>
				<ThreadActions {thread} {act} />
			{/if}
			{#if owner}
				<IconButton icon={UploadIcon} label="Push the branch to origin" onclick={push} />
			{/if}
			<IconButton icon={PanelRightIcon} label="Toggle right pane" onclick={() => (diffOpen = !diffOpen)} />
		</div>
	</PageHeader>

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
								{#if item.text}
									<Message from="user" class="ml-0 max-w-full">
										<MessageContent class="group-[.is-user]:ml-0"><p class="whitespace-pre-wrap"><MentionText text={item.text} /></p></MessageContent>
									</Message>
								{/if}
								{#if item.attachments.length}<MessageAttachments attachments={item.attachments} />{/if}
								{@render reviewCards(item.review)}
							</div>
						</div>
					{:else if item.kind === 'request'}
						{@const name = userName(item.actor)}
						{@const ownerName = userName(thread?.ownerId ?? '')}
						<div class="space-y-2 rounded-lg border bg-muted/30 px-4 py-3 text-sm">
							<div class="flex items-center gap-2">
								<UserAvatar {name} class="size-5" />
								<span><span class="font-medium">{name}</span> asks the agent:</span>
								<time class="text-xs text-muted-foreground" title={item.time && timestampDate(item.time).toLocaleString()}>{clock(item.time)}</time>
							</div>
							{#if item.text}<p class="whitespace-pre-wrap"><MentionText text={item.text} /></p>{/if}
							{#if item.attachments.length}<MessageAttachments attachments={item.attachments} />{/if}
							{@render reviewCards(item.review)}
							{#if item.resolved}
								<p class="text-xs text-muted-foreground">
									{item.resolved.approved ? 'Approved' : 'Declined'} by {userName(item.resolved.by)}{item.resolved.note ? `: ${item.resolved.note}` : ''}
								</p>
							{:else if owner}
								{@const seq = String(item.seq)}
								<div class="flex flex-wrap items-center gap-2">
									<Button size="sm" onclick={() => act(api.approveTurn({ threadId: id, requestSeq: item.seq }))}><CheckIcon /> Approve</Button>
									<Input bind:value={notes[seq]} placeholder="Note (optional)" class="h-8 min-w-0 flex-1 text-xs" aria-label="Decline note" />
									<Button size="sm" variant="outline" onclick={() => act(api.declineTurn({ threadId: id, requestSeq: item.seq, note: notes[seq] ?? '' }))}>
										<XIcon /> Decline
									</Button>
								</div>
								<p class="text-xs text-muted-foreground">It runs on your Claude subscription, in this thread's environment.</p>
							{:else}
								<p class="flex items-center gap-2 text-xs text-muted-foreground"><Loader size={12} /> Waiting for {ownerName} to approve</p>
							{/if}
						</div>
					{:else if item.kind === 'text'}
						{@render agentText(item.text)}
					{:else if item.kind === 'steps'}
						<ToolSteps steps={item.steps} />
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
								<Button size="sm" class="mt-2" onclick={() => (loginOpen = true)}>Log in to Claude Code</Button>
							{/if}
						</div>
					{:else if item.kind === 'setup'}
						<div>
							{#each item.steps as step (step.id)}
								{@const failed = step.state === SetupState.FAILED}
								<Tool class="group/tool mb-0 border-0" open={failed}>
									<CollapsibleTrigger
										disabled={!step.output}
										class="flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs enabled:hover:bg-muted/50 {step.state === SetupState.SKIPPED
											? 'text-muted-foreground'
											: ''}"
									>
										{#if step.state === SetupState.DONE}
											<CheckIcon class="size-3.5 shrink-0 text-emerald-500" />
										{:else if failed}
											<XCircleIcon class="size-3.5 shrink-0 text-destructive" />
										{:else if step.state === SetupState.SKIPPED}
											<CircleMinusIcon class="size-3.5 shrink-0" />
										{:else}
											<Loader size={14} class="text-muted-foreground" />
										{/if}
										<span class="min-w-0 flex-1 truncate">{step.title}{step.state === SetupState.SKIPPED ? ' (skipped)' : ''}</span>
										{#if step.output}
											<ChevronRightIcon class="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/tool:rotate-90" />
										{/if}
									</CollapsibleTrigger>
									<ToolContent class="ml-4 border-l pl-2">
										<pre
											class="my-1 max-h-64 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap {failed
												? 'bg-destructive/10 text-destructive'
												: 'bg-muted/50'}">{step.output}</pre>
									</ToolContent>
								</Tool>
							{/each}
						</div>
					{:else if item.kind === 'mode'}
						{@render divider(item.mode === Mode.CHAT ? 'Chat mode on' : 'Agent mode')}
					{:else if item.kind === 'settings'}
						{@render divider(`${userName(item.actor)} switched to ${item.settings.model} · ${item.settings.effort}`)}
					{/if}
				{/each}

				{#if chat.streaming}{@render agentText(chat.streaming)}{/if}
				{#if thread?.status === Status.STARTING}
					<div class="flex items-center gap-2 text-sm text-muted-foreground"><Loader /> Starting environment…</div>
				{:else if thread?.status === Status.SUSPENDED}
					<div class="flex items-center gap-2 text-sm text-muted-foreground"><PauseIcon class="size-4" /> Environment paused. Sending a message resumes it.</div>
				{:else if thread?.status === Status.ERRORED}
					<div class="flex items-center gap-3 rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm">
						<CircleAlertIcon class="size-4 shrink-0 text-destructive" />
						<span class="flex-1">{reason || 'The environment failed or was lost.'}</span>
						{#if owner}
							<Button size="sm" onclick={retry}><RotateCcwIcon /> Retry</Button>
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
		<!-- Who is producing something right now: the agent while it works, then people typing. Always takes its line, so the composer doesn't jump. -->
		<div class="flex h-6 items-center gap-2 text-xs text-muted-foreground" aria-live="polite">
			{#if thread?.status === Status.WORKING}
				<Loader size={12} /><span class="animate-pulse">Agent is {chat.streaming ? 'writing' : 'working'}…</span>
			{/if}
			{#if typingText}
				{#if thread?.status === Status.WORKING}<span>·</span>{/if}
				<span class="flex gap-0.5"><span class="size-1 animate-bounce rounded-full bg-current"></span><span class="size-1 animate-bounce rounded-full bg-current [animation-delay:150ms]"></span><span class="size-1 animate-bounce rounded-full bg-current [animation-delay:300ms]"></span></span>
				<span class="truncate">{typingText}</span>
			{/if}
		</div>
		{#if error}<p class="mb-2 text-sm text-destructive">{error}</p>{/if}
		{#if thread}<ThreadBanners {thread} {act} />{/if}
		<Plan plan={chat.plan} />
		<PromptInput.Root onSubmit={send} multiple maxFiles={5} maxFileSize={5 << 20} onError={(e) => (error = e.message)}>
			<fieldset disabled={thread?.status === Status.STARTING} class="contents" oninput={typed}>
				<PromptInput.Attachments class="px-2">
					{#snippet children(a)}<PromptInput.Attachment data={a} />{/snippet}
				</PromptInput.Attachments>
				<PromptInput.Body>
					<MentionInput placeholder="Message the agent. Use @agent to mention it in chat mode." />
				</PromptInput.Body>
				<PromptInput.Toolbar>
					<div class="flex items-center gap-1.5 pl-1">
						<AttachButton />
						{#if thread && owner}
							<!-- A running agent switches once its turns have ended. -->
							<OptionMenu label="Model" icon={CpuIcon} bind:value={() => thread!.model, (model) => setAgent({ model })} options={models} />
							<OptionMenu label="Effort" icon={BrainIcon} bind:value={() => thread!.effort, (effort) => setAgent({ effort })} options={efforts} />
						{:else if thread}
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

<SidePane bind:open={diffOpen}>
	<Tabs bind:value={tab} class="min-h-0 flex-1 gap-0">
		<TabsList variant="line" class="group-data-horizontal/tabs:h-12 w-full shrink-0 justify-start border-b px-2">
			<TabsTrigger value="diff">Diff{#if pending.length}<Badge variant="secondary" class="ml-1 h-4 px-1 text-[10px]">{pending.length}</Badge>{/if}</TabsTrigger>
			<TabsTrigger value="files">Files</TabsTrigger>
			<TabsTrigger value="browser">Browser</TabsTrigger>
			<TabsTrigger value="terminal">Terminal</TabsTrigger>
			<IconButton icon={XIcon} label="Close the pane" class="ml-auto" onclick={() => (diffOpen = false)} />
		</TabsList>
		<TabsContent value="diff" class="flex min-h-0 flex-col">
			<DiffView diff={thread?.diff ?? ''} bind:pending bind:focus bind:ask {posted} onsend={sendReview} />
		</TabsContent>
		<TabsContent value="files" class="min-h-0 overflow-auto">
			{#if filesOpened}<FileBrowser threadId={id} diff={thread?.diff ?? ''} />{/if}
		</TabsContent>
		{#if browserOpened}
			<!-- Hidden rather than unmounted, so the framed app keeps its state. -->
			<div class="min-h-0 flex-1 {tab === 'browser' ? '' : 'hidden'}">
				<Browser threadId={id} status={thread?.status} {owner} />
			</div>
		{/if}
		{#if owner && termOpened}
			<div class="min-h-0 flex-1 {tab === 'terminal' ? '' : 'hidden'}">
				<Terminal
					open={(cols, rows, signal) => api.openTerminal({ threadId: id, cols, rows }, { signal })}
					write={(data) => api.writeTerminal({ threadId: id, data })}
					resize={(cols, rows) => api.resizeTerminal({ threadId: id, cols, rows })}
				/>
			</div>
		{:else if tab === 'terminal'}
			<p class="p-4 text-sm text-muted-foreground">Only the thread owner can open the terminal.</p>
		{/if}
	</Tabs>
</SidePane>

<LoginDialog bind:open={loginOpen} />
