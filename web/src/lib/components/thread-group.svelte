<script lang="ts">
	import GitMergeConflictIcon from '@lucide/svelte/icons/git-merge-conflict';
	import StatusDot from '#lib/components/status-dot.svelte';
	import PullRequestChip from '#lib/components/pull-request-chip.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import { Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { projectName, threads, unread, userName, waiting } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';

	// One group of a person's threads as cards (the team board, a person's page), the first `max` shown until unfolded.
	let { name, list, max = Infinity }: { name: string; list: Thread[]; max?: number } = $props();
	let all = $state(false);
	const more = $derived(all ? 0 : list.length - max);

	function doing(t: Thread) {
		if (waiting(t)) return waiting(t);
		if (t.envWaiting) return t.envWaiting;
		if (t.status === Status.AWAITING_INPUT) return `Ready for ${t.ownerId === threads.me?.id ? 'you' : userName(t.ownerId)}`;
		if (t.status === Status.SUSPENDED) return 'Paused';
		const a = t.activity;
		if (!a?.text) return t.status === Status.ERRORED ? 'Errored' : '';
		return a.actor === 'agent' ? `Agent: ${a.text}` : a.actor === 'system' ? a.text : `${userName(a.actor)}: ${a.text}`;
	}
</script>

{#if list.length}
	<section aria-label={name} class="flex flex-col gap-2">
		<h3 class="px-1 text-[11px] font-medium tracking-wider text-muted-foreground uppercase">
			{name} <span class="tabular-nums">{list.length}</span>
		</h3>
		{#each more > 0 ? list.slice(0, max) : list as t (t.id)}{@render card(t)}{/each}
		{#if more > 0}
			<button class="px-1 text-left text-xs text-muted-foreground hover:text-foreground" onclick={() => (all = true)}>
				{more} more
			</button>
		{/if}
	</section>
{/if}

{#snippet card(t: Thread)}
	{@const working = t.status === Status.WORKING || t.status === Status.STARTING}
	<a
		href="/t/{t.id}"
		class="block rounded-lg border bg-card p-3 text-sm transition-colors hover:border-foreground/25 {working
			? 'border-l-2 border-l-sky-400'
			: ''} {t.status === Status.ERRORED ? 'border-l-2 border-l-red-500' : ''} {t.status === Status.SUSPENDED
			? 'opacity-60'
			: ''}"
	>
		<div class="flex items-center gap-2">
			<StatusDot status={t.status} waiting={waiting(t)} />
			<span class="truncate {unread(t) ? 'font-semibold' : ''}">{t.title || 'Untitled thread'}</span>
			{#if unread(t)}<span class="ml-auto size-1.5 shrink-0 rounded-full bg-primary" aria-label="Unread"></span>{/if}
		</div>
		<p class="mt-1 text-xs {t.envWaiting ? 'break-words' : 'truncate'} {t.status === Status.ERRORED ? 'text-destructive' : 'text-muted-foreground'}" title={doing(t)}>
			{doing(t) || ' '}
		</p>
		<div class="mt-2 flex items-center gap-2 text-[11px] text-muted-foreground">
			<span class="truncate">{projectName(t.repoUrl)}</span>
			<span class="shrink-0 tabular-nums">{ago(t.activity?.time ?? t.updatedAt, threads.now)}</span>
			{#if t.pullRequest}<PullRequestChip pr={t.pullRequest} small />{/if}
			{#if t.overlaps.length}
				{@const label = `Overlaps with ${t.overlaps.map((o) => o.title || 'Untitled thread').join(', ')}`}
				<span role="img" class="flex shrink-0 items-center gap-0.5 text-amber-600 dark:text-amber-400" aria-label={label} title={label}>
					<GitMergeConflictIcon class="size-3" />{t.overlaps.length}
				</span>
			{/if}
			{#if t.presentUserIds.length}
				<span class="ml-auto flex shrink-0 -space-x-1.5" title="Here now: {t.presentUserIds.map(userName).join(', ')}">
					{#each t.presentUserIds.slice(0, 3) as u (u)}
						<UserAvatar name={userName(u)} class="size-5 ring-2 ring-card" />
					{/each}
				</span>
			{/if}
		</div>
	</a>
{/snippet}
