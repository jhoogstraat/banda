<script lang="ts">
	import BotIcon from '@lucide/svelte/icons/bot';
	import SquareTerminalIcon from '@lucide/svelte/icons/square-terminal';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import { SearchKind, type SearchResult } from '#lib/gen/banda/v1/banda_pb.js';
	import { projectName, userName } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';

	// One search result: who, where and when, then the passage with the matched words marked. `people`: the author's
	// name links to their page (when the row's own link lies under it).
	let { hit, compact = false, people = false }: { hit: SearchResult; compact?: boolean; people?: boolean } = $props();
	const agent = $derived(hit.actor === 'agent' || hit.actor.startsWith('agent:'));
</script>

<div class="min-w-0 flex-1 space-y-0.5">
	<div class="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
		{#if hit.kind === SearchKind.CODE}
			<SquareTerminalIcon class="size-4 shrink-0" />
		{:else if agent}
			<BotIcon class="size-4 shrink-0" />
		{:else}
			<UserAvatar name={userName(hit.actor)} class="size-4" />
		{/if}
		{#if people && !agent && hit.kind !== SearchKind.CODE}
			<a href="/u/{hit.actor}" class="relative shrink-0 font-medium text-foreground hover:underline">{userName(hit.actor)}</a>
		{:else}
			<span class="shrink-0 font-medium text-foreground">{agent ? 'Agent' : userName(hit.actor)}</span>
		{/if}
		<span class="min-w-0 truncate">in {hit.threadTitle || 'Untitled thread'}{compact ? '' : ` · ${projectName(hit.repoUrl)}`}</span>
		<span class="ml-auto shrink-0">{ago(hit.time)}</span>
	</div>
	<p class="text-sm {compact ? 'line-clamp-1' : 'line-clamp-3'} break-words">
		{#each hit.snippet as part, i}{#if i % 2}<mark class="rounded-sm bg-amber-400/30 px-0.5 text-foreground">{part}</mark>{:else}{part}{/if}{/each}
	</p>
</div>
