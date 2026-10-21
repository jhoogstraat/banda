<script lang="ts">
	import CheckIcon from '@lucide/svelte/icons/check';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import CircleIcon from '@lucide/svelte/icons/circle';
	import ListTodoIcon from '@lucide/svelte/icons/list-todo';
	import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '#lib/components/ui/collapsible/index.js';
	import type { Todo } from '#lib/events.js';

	// The agent's latest TodoWrite list, pinned above the composer while something is left to do.
	let { plan }: { plan: Todo[] } = $props();

	let open = $state(true);
	const done = $derived(plan.filter((t) => t.status === 'completed').length);
	const current = $derived(plan.find((t) => t.status === 'in_progress'));
</script>

{#if done < plan.length}
	<Collapsible bind:open class="group/plan mb-2 rounded-lg border bg-muted/40 text-xs">
		<CollapsibleTrigger class="flex w-full items-center gap-2 px-3 py-1.5 text-left">
			<ListTodoIcon class="size-3.5 shrink-0 text-muted-foreground" />
			<span class="shrink-0 font-medium">Plan</span>
			<span class="shrink-0 text-muted-foreground">{done}/{plan.length} done</span>
			<span class="min-w-0 flex-1 truncate text-muted-foreground group-data-[state=open]/plan:invisible">{current?.activeForm ?? current?.content ?? ''}</span>
			<ChevronRightIcon class="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/plan:rotate-90" />
		</CollapsibleTrigger>
		<CollapsibleContent>
			<ul class="max-h-48 space-y-0.5 overflow-y-auto px-3 pb-2">
				{#each plan as t, i (i)}
					<li
						class="flex items-center gap-2 rounded px-1 py-0.5 {t.status === 'completed'
							? 'text-muted-foreground line-through'
							: t.status === 'in_progress'
								? 'bg-primary/10 font-medium'
								: ''}"
					>
						{#if t.status === 'completed'}
							<CheckIcon class="size-3.5 shrink-0 text-emerald-500" />
						{:else}
							<CircleIcon class="size-3.5 shrink-0 {t.status === 'in_progress' ? 'animate-pulse fill-primary text-primary' : 'text-muted-foreground'}" />
						{/if}
						<span class="min-w-0 truncate">{t.status === 'in_progress' ? (t.activeForm ?? t.content) : t.content}</span>
					</li>
				{/each}
			</ul>
		</CollapsibleContent>
	</Collapsible>
{/if}
