<script lang="ts">
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import GitMergeConflictIcon from '@lucide/svelte/icons/git-merge-conflict';
	import StatusDot from '#lib/components/status-dot.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import type { Overlap } from '#lib/gen/banda/v1/banda_pb.js';
	import { userName } from '#lib/threads.svelte.js';

	// Other active threads on the same repository and base branch that change some of the same files.
	let { overlaps }: { overlaps: Overlap[] } = $props();
	let open = $state(false);
</script>

{#if overlaps.length}
	<section class="shrink-0 border-b text-xs" aria-label="Overlaps">
		<button class="flex w-full items-center gap-1.5 px-3 py-1.5 text-warning-foreground hover:bg-muted/50" aria-expanded={open} onclick={() => (open = !open)}>
			<GitMergeConflictIcon class="size-3.5 shrink-0" />
			Overlaps with {overlaps.length} thread{overlaps.length > 1 ? 's' : ''}
			<span class="truncate text-muted-foreground">· {overlaps.map((o) => o.title || 'Untitled thread').join(', ')}</span>
			<ChevronRightIcon class="ml-auto size-3.5 shrink-0 text-muted-foreground transition-transform {open ? 'rotate-90' : ''}" />
		</button>
		{#if open}
			<ul class="space-y-2 px-3 pb-2">
				{#each overlaps as o (o.threadId)}
					<li class="space-y-0.5">
						<a href="/t/{o.threadId}" class="flex min-w-0 items-center gap-2 hover:underline">
							<UserAvatar name={userName(o.ownerId)} class="size-5" />
							<StatusDot status={o.status} />
							<span class="truncate font-medium">{o.title || 'Untitled thread'}</span>
							<span class="shrink-0 text-muted-foreground">{userName(o.ownerId)}</span>
						</a>
						<div class="flex flex-wrap gap-x-3 pl-7 font-mono text-muted-foreground">
							{#each o.paths as p (p)}
								<a href="/t/{o.threadId}?file={encodeURIComponent(p)}" class="hover:text-foreground hover:underline" title="Show {p} in its diff">{p}</a>
							{/each}
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
{/if}
