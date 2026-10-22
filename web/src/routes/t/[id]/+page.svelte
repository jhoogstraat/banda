<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { tick } from 'svelte';
	import { Code, ConnectError } from '@connectrpc/connect';
	import ArchiveIcon from '@lucide/svelte/icons/archive';
	import BellOffIcon from '@lucide/svelte/icons/bell-off';
	import BotIcon from '@lucide/svelte/icons/bot';
	import CheckIcon from '@lucide/svelte/icons/check';
	import CornerLeftUpIcon from '@lucide/svelte/icons/corner-left-up';
	import FileDiffIcon from '@lucide/svelte/icons/file-diff';
	import FolderTreeIcon from '@lucide/svelte/icons/folder-tree';
	import GitBranchPlusIcon from '@lucide/svelte/icons/git-branch-plus';
	import GitForkIcon from '@lucide/svelte/icons/git-fork';
	import GitPullRequestIcon from '@lucide/svelte/icons/git-pull-request';
	import HandshakeIcon from '@lucide/svelte/icons/handshake';
	import MessagesSquareIcon from '@lucide/svelte/icons/messages-square';
	import PanelRightIcon from '@lucide/svelte/icons/panel-right';
	import PauseIcon from '@lucide/svelte/icons/pause';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import SquareIcon from '@lucide/svelte/icons/square';
	import SquareTerminalIcon from '@lucide/svelte/icons/square-terminal';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import {
		Conversation,
		ConversationContent,
		ConversationEmptyState,
		ConversationScrollButton
	} from '#lib/components/ai-elements/conversation/index.js';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import type { PromptInputMessage } from '#lib/components/ai-elements/prompt-input/index.js';
	import ChatItem, { agentText } from '#lib/components/chat-item.svelte';
	import Composer from '#lib/components/composer.svelte';
	import ForkDialog from '#lib/components/fork-dialog.svelte';
	import HandoffDialog from '#lib/components/handoff-dialog.svelte';
	import LoadState from '#lib/components/load-state.svelte';
	import LoginDialog from '#lib/components/login-dialog.svelte';
	import Overlaps from '#lib/components/overlaps.svelte';
	import PullRequestDialog from '#lib/components/pull-request-dialog.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import SpawnDialog from '#lib/components/spawn-dialog.svelte';
	import SubThreads from '#lib/components/sub-threads.svelte';
	import { isSnoozed, presets, settle, snooze } from '#lib/components/thread-actions.svelte';
	import ThreadError from '#lib/components/thread-error.svelte';
	import ThreadHeader from '#lib/components/thread-header.svelte';
	import ThreadPane from '#lib/components/thread-pane.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { api, upload } from '#lib/api.js';
	import type { Comment } from '#lib/diff.js';
	import { apply, itemAt, newChat, prepend, typingNow, type Chat } from '#lib/events.js';
	import { Mode, Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { IsMobile } from '#lib/hooks/is-mobile.svelte.js';
	import { threadKeys, ui, visited, type Command } from '#lib/shortcuts.svelte.js';
	import { threads, userName } from '#lib/threads.svelte.js';
	import { noun } from '#lib/pullrequests.js';

	// The layout re-creates this page for every thread, so the id never changes.
	const id = page.params.id!;
	visited(id);

	let thread = $state<Thread>();
	const chat = $state<Chat>(newChat());
	// Opening replays only the last TAIL events; older ones load a page at a time when scrolling up.
	const TAIL = 500;
	const PAGE = 500;
	let oldest = $state(0n); // the oldest loaded event's seq; history starts at 1
	let loadingOlder = $state(false);
	let scroller = $state<HTMLElement | null>(null);
	// The right pane opens when asked for, or for the first port that starts listening while the page is open.
	let diffOpen = $state(false);
	let tab = $state('diff');
	let previewPort = $state<number>();
	let autoOpened = false;
	// Events after the replay: the first presence event follows it (the server sends it on joining).
	let live = false;
	const mobile = new IsMobile();
	let loginOpen = $state(false);
	let forkOpen = $state(false);
	let spawnOpen = $state(false);
	let handoffOpen = $state(false);
	let prOpen = $state(false);
	// Bumped when the thread hears its pull request changed, so the Pull request tab reloads it.
	let prVersion = $state(0);
	// Bumped by lineage events, so the sub-thread strip refetches.
	let lineage = $state(0);
	let error = $state('');
	// The reason of the latest status change, e.g. why the environment failed.
	let reason = $state('');
	let offline = $state(false);
	// GetThread said there is no such thread (or none the viewer may open).
	let missing = $state(false);
	// Why the thread couldn't be loaded yet; retried every 2 s, or now from the page.
	let loadError = $state('');
	const failed = $derived(!thread && !!loadError);
	let closed = false;
	const owner = $derived(!!threads.me && threads.me.id === thread?.ownerId);
	// Ticks so typing expires; `others` drops the viewer from presence and typing.
	let now = $state(Date.now());
	const others = (ids: string[]) => ids.filter((u) => u !== threads.me?.id);
	const here = $derived(others(chat.present));
	const typing = $derived(others(typingNow(chat, now)).map(userName));
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
	const waiting = $derived(chat.items.filter((i) => i.kind === 'request' && !i.resolved));
	const posted = $derived(chat.items.flatMap((i) => (i.kind === 'message' || i.kind === 'request' ? i.review.map((c) => ({ ...c, by: i.actor })) : [])));
	function reveal(c: Comment) {
		show('diff');
		focus = c;
	}

	// A failed load tries again: a quiet thread sends no event that would refresh it. Before the thread
	// first loads, the page shows the failure instead of an empty thread; after, the composer's error line.
	async function load() {
		clearTimeout(timer);
		try {
			thread = (await api.getThread({ threadId: id })).thread;
			if (error === loadError) error = '';
			loadError = '';
		} catch (e) {
			const code = ConnectError.from(e).code;
			if (code === Code.NotFound || code === Code.PermissionDenied) return void (missing = true);
			loadError = (e as Error).message;
			if (thread) error = loadError;
			if (!closed) timer = setTimeout(load, 2000);
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

	// /t/<id>#<seq> (from search) scrolls to that event's item and flashes it once it is loaded:
	// older history loads down to it, newer events arrive with the stream.
	let target = $state<bigint>();
	let flash = $state<number>();
	$effect(() => {
		const hash = page.url.hash;
		if (/^#\d+$/.test(hash)) target = BigInt(hash.slice(1));
	});
	// /t/<id>?file=<path> (from an overlap) opens the diff at that file once the thread is loaded.
	let shownFile = '';
	$effect(() => {
		const path = page.url.searchParams.get('file') ?? '';
		if (!path || !thread || path === shownFile) return;
		shownFile = path;
		show('diff');
		focus = { path, body: '', startLine: 0, endLine: 0, old: false, code: '' }; // no line: the file itself
	});
	$effect(() => {
		if (target === undefined || !oldest) return;
		if (target < oldest) {
			loadOlder(Math.min(1000, Number(oldest - target) + 20)); // runs again once `oldest` moved
			return;
		}
		const i = itemAt(chat, target);
		if (i < 0) return;
		const key = chat.items[i].key;
		target = undefined;
		flash = key;
		setTimeout(() => document.querySelector(`[data-key="${key}"]`)?.scrollIntoView({ block: 'center' }));
		setTimeout(() => flash === key && (flash = undefined), 2500);
	});

	async function watch(signal: AbortSignal) {
		let seq = 0n;
		while (!signal.aborted && !missing) {
			chat.streaming = ''; // deltas are not replayed, drop a half-finished bubble
			try {
				for await (const { event } of api.watchThread({ threadId: id, afterSeq: seq, tail: TAIL }, { signal })) {
					if (!event) continue;
					offline = false;
					if (event.context) {
						apply(chat, event);
						continue;
					}
					if (event.seq) {
						if (!oldest) oldest = event.seq;
						seq = event.seq;
						markRead();
					}
					apply(chat, event);
					if (event.kind.case === 'presenceChanged') live = true;
					// On phones the pane covers the chat: only the link in the chat opens it there.
					if (event.kind.case === 'portOpened' && live && !autoOpened && !diffOpen && !mobile.current) {
						autoOpened = true;
						show('browser', event.kind.value.port);
					}
					if (event.kind.case === 'lineageUpdate') lineage++;
					if (event.kind.case === 'pullRequestChanged') {
						prVersion++;
						refresh();
					}
					if (event.kind.case === 'statusChanged') reason = event.kind.value.reason;
					if (['diffChanged', 'statusChanged', 'modeChanged', 'turnRequest', 'turnRequestResolved', 'notice', 'agentSettings', 'permissionModeChanged', 'permissionRequested', 'permissionResolved', 'forkNote', 'permissionRevoked', 'autoPush', 'overlap', 'handoffOffered', 'handoffResolved'].includes(event.kind.case!)) refresh();
				}
			} catch {
				if (signal.aborted) return;
			}
			offline = !missing;
			await new Promise((r) => setTimeout(r, 1000));
		}
	}

	async function loadOlder(limit = PAGE) {
		if (loadingOlder || oldest <= 1n || !scroller) return;
		loadingOlder = true;
		try {
			const { events } = await api.history({ threadId: id, beforeSeq: oldest, limit });
			const el = scroller;
			const height = el.scrollHeight;
			const top = el.scrollTop;
			prepend(chat, events);
			oldest = events[0]?.seq ?? 1n;
			await tick();
			// Keep what the reader sees in place, unless the browser's scroll anchoring already did.
			if (el.scrollTop === top) el.scrollTop = top + el.scrollHeight - height;
		} catch (e) {
			error = (e as Error).message;
		} finally {
			loadingOlder = false;
		}
	}

	// Scrolling up near the top loads older history. Only upward scrolls count: the scroll to the
	// bottom on open starts at the top too.
	let lastTop = 0;
	const onScroll = () => {
		const top = scroller?.scrollTop ?? 0;
		if (top < lastTop && top < 1500) loadOlder();
		lastTop = top;
	};

	$effect(() => {
		const ctrl = new AbortController();
		load();
		watch(ctrl.signal);
		const tick = setInterval(() => (now = Date.now()), 1000);
		return () => {
			closed = true;
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
			refresh(); // drops what the page showed ahead of the call (the model chip)
			return false;
		}
	}

	function setAgent(change: { model?: string; effort?: string; profile?: string }) {
		Object.assign(thread!, change); // the chip shows the choice right away
		act(api.setAgentSettings({ threadId: id, ...change }));
	}
	// Shared by the header buttons and the command palette.
	const interrupt = () => act(api.interrupt({ threadId: id }));
	const toggleMode = () => act(api.setMode({ threadId: id, mode: thread!.mode === Mode.AGENT ? Mode.CHAT : Mode.AGENT }));
	const push = () => act(api.push({ threadId: id }));
	const recreate = () => act(api.environmentRecreate({ threadId: id }));
	const pr = $derived(thread?.pullRequestUrl ?? '');
	const retry = () => act(api.retry({ threadId: id }));
	// The agent's latest reply: a pull request's description to start from.
	const lastReply = $derived.by(() => {
		const i = chat.items.findLast((i) => i.kind === 'text');
		return i?.kind === 'text' ? i.text : '';
	});
	const show = (t: string, port?: number) => {
		tab = t;
		if (port) previewPort = port;
		diffOpen = true;
	};

	// The palette's "This thread" actions, each only where it applies.
	$effect(() => {
		const t = thread;
		if (!t) return;
		const c: Command[] = [
			{ label: 'Toggle right pane', icon: PanelRightIcon, keys: threadKeys.pane, run: () => (diffOpen = !diffOpen) },
			...(t.repoUrl ? [{ label: 'Show diff', icon: FileDiffIcon, keys: threadKeys.diff, run: () => show('diff') }] : []),
			{ label: 'Show files', icon: FolderTreeIcon, run: () => show('files') }
		];
		if (owner) c.push({ label: 'Open terminal', icon: SquareTerminalIcon, run: () => show('terminal') });
		if (isSnoozed(t)) c.push({ label: 'Unsnooze', icon: BellOffIcon, run: () => act(snooze(t)) });
		else
			for (const [label, date] of [presets[0], presets[2]])
				c.push({ label: `Snooze ${label === '1 hour' ? 'for 1 hour' : `until ${label.toLowerCase()}`}`, icon: BellOffIcon, run: () => act(snooze(t, date())) });
		c.push({ label: 'Fork this thread', icon: GitForkIcon, keys: threadKeys.fork, run: () => (forkOpen = true) });
		c.push({ label: t.settled ? 'Unsettle' : 'Settle', icon: CheckIcon, keys: threadKeys.settle, run: () => act(settle(t)) });
		c.push({ label: 'Spawn sub-thread', icon: GitBranchPlusIcon, keys: threadKeys.spawn, run: () => (spawnOpen = true) });
		if (t.parentId) c.push({ label: `Go to parent: ${t.parentTitle}`, icon: CornerLeftUpIcon, run: () => goto(`/t/${t.parentId}`) });
		if (owner && t.status === Status.WORKING) c.push({ label: 'Interrupt the agent', icon: SquareIcon, run: interrupt });
		if (owner && t.status === Status.ERRORED) c.push({ label: 'Retry', icon: RotateCcwIcon, run: retry });
		if (t.pullRequest) c.push({ label: `Show the ${noun(t.forge)}`, icon: GitPullRequestIcon, run: () => show('pull') });
		else if (pr && owner && t.forgeReady) c.push({ label: `Create a ${noun(t.forge)}`, icon: GitPullRequestIcon, run: () => (prOpen = true) });
		else if (pr) c.push({ label: 'Open a pull request', icon: GitPullRequestIcon, run: () => open(pr) });
		if (owner && t.environmentDeleted) c.push({ label: 'Recreate environment', icon: RotateCcwIcon, run: recreate });
		if (owner && !t.handoffTo) c.push({ label: 'Hand off to…', icon: HandshakeIcon, run: () => (handoffOpen = true) });
		if (owner) {
			if (!pr && t.repoUrl) c.push({ label: 'Push the branch to origin', icon: UploadIcon, run: push });
			c.push(t.mode === Mode.CHAT
				? { label: 'Switch to agent mode', icon: BotIcon, keys: threadKeys.mode, run: toggleMode }
				: { label: 'Switch to chat mode', icon: MessagesSquareIcon, keys: threadKeys.mode, run: toggleMode });
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

<div class="@container flex min-w-0 flex-1 flex-col">
	{#if missing}
		<PageHeader />
		<LoadState title="Thread not found" description="It doesn't exist, it was deleted, or the link is wrong." />
	{:else if failed}
		<PageHeader />
		<LoadState title="Couldn't load this thread" description={loadError} retry={load} />
	{:else}
		<ThreadHeader {thread} {owner} offline={offline && !missing} {here} {act} {interrupt} {toggleMode} {retry} {push} onpull={() => show('pull')} oncreate={() => (prOpen = true)} bind:forkOpen bind:spawnOpen bind:handoffOpen bind:paneOpen={diffOpen} />
		<SubThreads threadId={id} version={lineage} />
		<Overlaps overlaps={thread?.overlaps ?? []} />

		<Conversation class="min-h-0 flex-1">
			<ConversationContent bind:ref={scroller} onscroll={onScroll} class="min-h-0 flex-1 overflow-y-auto">
				<div class="mx-auto flex w-full max-w-3xl flex-col gap-4">
					{#if oldest > 1n}
						<Button variant="ghost" size="sm" class="self-center text-muted-foreground" disabled={loadingOlder} onclick={() => loadOlder()}>
							{#if loadingOlder}<Loader /> Loading earlier messages…{:else}Load earlier messages{/if}
						</Button>
					{/if}
					{#each chat.items as item (item.key)}
						<!-- Off-screen items skip layout and paint; long threads stay smooth. -->
						<div
							data-key={item.key}
							class="rounded-lg transition-shadow duration-500 [contain-intrinsic-size:auto_80px] [content-visibility:auto] {flash === item.key
								? 'ring-2 ring-primary/60 ring-offset-4 ring-offset-background'
								: ''}"
						>
							<ChatItem {item} threadId={id} {thread} {owner} {act} {waiting} onreveal={reveal} onlogin={() => (loginOpen = true)} onshow={show} />
						</div>
					{/each}

					{#if chat.streaming}{@render agentText(chat.streaming)}{/if}
					{#if thread?.status === Status.STARTING}
						<div class="flex items-center gap-2 text-sm text-muted-foreground"><Loader /> Starting environment…</div>
					{:else if thread?.status === Status.SUSPENDED && thread.environmentDeleted}
						<div class="flex items-center gap-3 rounded-lg border bg-muted/40 px-4 py-3 text-sm">
							<ArchiveIcon class="size-4 shrink-0 text-muted-foreground" />
							<span class="flex-1">Environment deleted. Branch <code>{thread.branch}</code> is on origin.</span>
							{#if owner}
								<Button size="sm" onclick={recreate}><RotateCcwIcon /> Recreate environment</Button>
							{/if}
						</div>
					{:else if thread?.status === Status.SUSPENDED}
						<div class="flex items-center gap-2 text-sm text-muted-foreground"><PauseIcon class="size-4" /> Environment paused. Sending a message resumes it.</div>
					{:else if thread?.status === Status.ERRORED}
						<ThreadError {thread} {owner} {reason} {chat} {retry} onterminal={() => show('terminal')} />
					{:else if !chat.items.length}
						<ConversationEmptyState title="No messages yet" description="" />
					{/if}
				</div>
			</ConversationContent>
			<ConversationScrollButton />
		</Conversation>

		<Composer {thread} {owner} {chat} {typing} bind:error {act} {send} {typed} {setAgent} {toggleMode} />
	{/if}
</div>

{#if !missing && !failed}
	<ThreadPane threadId={id} {thread} {owner} bind:open={diffOpen} bind:tab bind:pending bind:focus bind:ask bind:port={previewPort} {posted} {prVersion} onsend={sendReview} />
{/if}

<LoginDialog bind:open={loginOpen} />
{#if thread}<ForkDialog bind:open={forkOpen} {thread} />{/if}
{#if thread}<SpawnDialog bind:open={spawnOpen} {thread} />{/if}
{#if thread}<HandoffDialog bind:open={handoffOpen} {thread} />{/if}
{#if thread}<PullRequestDialog bind:open={prOpen} {thread} summary={lastReply} ondone={refresh} />{/if}
