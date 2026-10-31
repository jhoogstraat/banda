<script lang="ts">
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import UserCheckIcon from '@lucide/svelte/icons/user-check';
	import UserXIcon from '@lucide/svelte/icons/user-x';
	import { ChecksState, Mergeable, ReviewState, type PullRequestSummary } from '#lib/gen/banda/v1/banda_pb.js';
	import { checkStates, describe, live, pullStates, ref } from '#lib/pullrequests.js';

	// A pull request at a glance: its state, number, and only the checks, review and conflicts that say something.
	// Small in lists; in the header a button (onclick) that opens the Pull request tab.
	let { pr, small = false, onclick }: { pr: PullRequestSummary; small?: boolean; onclick?: () => void } = $props();
	const look = $derived(pullStates[pr.state]);
	const checks = $derived(live(pr) && pr.checks !== ChecksState.UNSPECIFIED ? checkStates[pr.checks] : undefined);
	const icon = $derived(small ? 'size-3' : 'size-3.5');
</script>

{#snippet body()}
	<look.icon class="{icon} shrink-0 {look.class}" />
	<span class="tabular-nums">{ref(pr)}</span>
	{#if checks}<checks.icon class="{icon} shrink-0 {checks.class}" />{/if}
	{#if live(pr) && pr.review === ReviewState.APPROVED}<UserCheckIcon class="{icon} shrink-0 text-success-foreground" />{/if}
	{#if live(pr) && pr.review === ReviewState.CHANGES_REQUESTED}<UserXIcon class="{icon} shrink-0 text-destructive" />{/if}
	{#if live(pr) && pr.mergeable === Mergeable.CONFLICTS}<TriangleAlertIcon class="{icon} shrink-0 text-destructive" />{/if}
{/snippet}

{#if onclick}
	<button
		type="button"
		class="flex h-7 shrink-0 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
		title={describe(pr)}
		aria-label={describe(pr)}
		{onclick}>{@render body()}</button
	>
{:else}
	<span role="img" class="flex shrink-0 items-center gap-0.5 {small ? 'text-[11px]' : 'text-xs'} text-muted-foreground" title={describe(pr)} aria-label={describe(pr)}>
		{@render body()}
	</span>
{/if}
