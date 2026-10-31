<script lang="ts">
	import { goto } from '$app/navigation';
	import ChatItem from '#lib/components/chat-item.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import { api } from '#lib/api.js';
	import { apply, newChat } from '#lib/events.js';
	import { PermissionDecision, PermissionMode, type Decision } from '#lib/gen/banda/v1/banda_pb.js';
	import { ui } from '#lib/shortcuts.svelte.js';
	import { projectName, threads, userName } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';

	// Every pending decision in the threads I can decide, oldest first, live from the inbox stream. Each is the
	// chat's own card, so answering here makes the same calls; the keys answer the selected one.
	const key = (d: Decision) => `${d.request!.threadId}#${d.request!.seq}`;
	const href = (d: Decision) => `/t/${d.request!.threadId}#${d.request!.seq}`;
	// Answered here: hidden at once, before the stream drops them.
	let done = $state<string[]>([]);
	const list = $derived(
		threads.decisions
			.filter((d) => !done.includes(key(d)))
			.map((d) => {
				const chat = newChat();
				apply(chat, d.request!);
				return { d, item: chat.items[0] };
			})
	);
	let at = $state(0);
	const sel = $derived(Math.max(0, Math.min(at, list.length - 1)));
	let error = $state('');

	async function act(k: string, call: Promise<unknown>) {
		try {
			await call;
			error = '';
			done.push(k);
			return true;
		} catch (e) {
			error = (e as Error).message;
			return false;
		}
	}

	function answer(d: Decision, always = false) {
		const threadId = d.request!.threadId;
		const k = d.request!.kind;
		if (k.case === 'turnRequest') return act(key(d), api.approveTurn({ threadId, requestSeq: d.request!.seq }));
		if (k.case !== 'permissionRequested') return;
		// A question is answered with its options, in its card.
		if (k.value.toolName === 'AskUserQuestion') return;
		const plan = k.value.toolName === 'ExitPlanMode';
		if (plan && always) return;
		const decision = always ? PermissionDecision.ALLOW_ALWAYS : PermissionDecision.ALLOW;
		act(key(d), api.permissionAnswer({ threadId, requestId: k.value.id, decision, mode: plan ? PermissionMode.ACCEPT_EDITS : PermissionMode.UNSPECIFIED }));
	}

	const card = (n: number) => document.querySelector<HTMLElement>(`[data-decision="${n}"]`);

	// Before the app's shortcuts (capture), which skip what this handled: j/k move here, not through the sidebar.
	function onkeydown(e: KeyboardEvent) {
		const el = e.target as HTMLElement;
		if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey || ui.palette || ui.help) return;
		if (el.matches?.('input, textarea, select, [contenteditable]')) return;
		const d = list[sel]?.d;
		if (e.key === 'j' || e.key === 'k') at = Math.max(0, Math.min(sel + (e.key === 'j' ? 1 : -1), list.length - 1));
		else if (!d) return;
		else if (e.key === 'y' || e.key === 'a') answer(d, e.key === 'a');
		else if (e.key === 'n') {
			// A turn request's note sits behind a toggle; the input focuses itself when it appears.
			const c = card(sel);
			const input = c?.querySelector<HTMLInputElement>('input[aria-label="Reason"], input[aria-label="Decline note"]');
			if (input) input.focus();
			else c?.querySelector<HTMLButtonElement>('button[aria-label="Add a note to the decline"]')?.click();
		}
		else if (e.key === 'o') goto(href(d));
		else return;
		e.preventDefault();
	}
	$effect(() => {
		window.addEventListener('keydown', onkeydown, true);
		return () => window.removeEventListener('keydown', onkeydown, true);
	});
	$effect(() => card(sel)?.scrollIntoView({ block: 'nearest' }));
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader>
		<h1 class="text-sm font-medium">Decisions</h1>
		{#if list.length}<span class="text-xs text-muted-foreground tabular-nums">{list.length} pending</span>{/if}
		{#if list.length}
			<span class="ml-auto hidden pr-2 text-xs text-muted-foreground md:block [&_kbd]:rounded [&_kbd]:border [&_kbd]:bg-muted [&_kbd]:px-1 [&_kbd]:font-sans">
				<kbd>J</kbd>/<kbd>K</kbd> move · <kbd>Y</kbd> allow · <kbd>N</kbd> deny · <kbd>A</kbd> always · <kbd>O</kbd> open
			</span>
		{/if}
	</PageHeader>
	<div class="min-h-0 flex-1 overflow-y-auto">
		<div class="mx-auto max-w-3xl space-y-3 p-3 md:p-4">
			{#if error}<p class="rounded-md border border-destructive/40 px-3 py-2 text-sm text-destructive">{error}</p>{/if}
			{#each list as { d, item }, n (key(d))}
				{@const t = d.thread!}
				{@const mine = t.ownerId === threads.me?.id}
				<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
				<article
					data-decision={n}
					aria-label="Decision in {t.title}"
					aria-current={n === sel}
					class="space-y-2 rounded-xl border p-2 transition-shadow md:p-3 {n === sel ? 'ring-2 ring-primary/50' : ''}"
					onclick={() => (at = n)}
				>
					<header class="flex min-w-0 items-center gap-2 px-1 text-xs text-muted-foreground">
						{#if !mine}<UserAvatar name={userName(t.ownerId)} class="size-4" />{/if}
						<a href={href(d)} class="min-w-0 truncate font-medium text-foreground hover:underline">{t.title || 'Untitled thread'}</a>
						<span class="hidden shrink-0 sm:inline">{projectName(t.repoUrl)}</span>
						{#if !mine}<span class="shrink-0">{userName(t.ownerId)}’s thread</span>{/if}
						<span class="ml-auto shrink-0 tabular-nums">{ago(d.request!.time, threads.now)}</span>
					</header>
					<ChatItem {item} threadId={t.id} thread={t} owner={mine} act={(c) => act(key(d), c)} onreveal={() => goto(href(d))} onlogin={() => goto('/settings')} />
				</article>
			{:else}
				<div class="py-16 text-center text-sm text-muted-foreground">
					<p class="font-medium text-foreground">Nothing to decide</p>
					<p class="mt-1">Permission requests, plans and teammates’ turn requests show up here as they come.</p>
					<Button size="sm" variant="outline" href="/" class="mt-4">Go to your threads</Button>
				</div>
			{/each}
		</div>
	</div>
</div>
