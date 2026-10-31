<script lang="ts">
	import BrainIcon from '@lucide/svelte/icons/brain';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import { Reasoning, ReasoningContent, ReasoningTrigger } from '#lib/components/ai-elements/reasoning/index.js';
	import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '#lib/components/ui/collapsible/index.js';
	import type { Step, Tool } from '#lib/events.js';
	import { describe, plural } from '#lib/tools.js';
	import ToolCall from './tool-call.svelte';

	// What the agent did between two texts. More than one step folds into a "Used N tools" row
	// that shows the latest action while closed.
	let { steps }: { steps: Step[] } = $props();

	const tools = $derived(steps.filter((s): s is Tool => s.kind === 'tool'));
	const running = $derived(tools.some((t) => !t.result));
	const errors = $derived(tools.filter((t) => t.result?.isError).length);
	const last = $derived(tools.at(-1));
	const now = $derived(last && describe(last));
</script>

{#snippet step(s: Step)}
	{#if s.kind === 'tool'}
		<ToolCall tool={s} />
	{:else}
		<Reasoning defaultOpen={false} class="mb-0">
			<ReasoningTrigger><BrainIcon class="size-4" /> Thinking</ReasoningTrigger>
			<ReasoningContent content={s.text} />
		</Reasoning>
	{/if}
{/snippet}

{#if steps.length === 1}
	{@render step(steps[0])}
{:else}
	<Collapsible class="group/steps">
		<CollapsibleTrigger class="flex w-full items-center gap-2 rounded-md px-2 py-1 pointer-coarse:py-2 text-left text-xs text-muted-foreground hover:bg-muted/50">
			<ChevronRightIcon class="size-3.5 shrink-0 transition-transform group-data-[state=open]/steps:rotate-90" />
			<span class="shrink-0 font-medium text-foreground">{tools.length ? `Used ${plural(tools.length, 'tool')}` : 'Thought'}</span>
			{#if errors}<span class="flex shrink-0 items-center gap-1 text-destructive"><XCircleIcon class="size-3.5" />{errors}</span>{/if}
			{#if now}
				<span class="min-w-0 flex-1 truncate font-mono group-data-[state=open]/steps:invisible">{`${now.label} ${now.detail}`.trim()}</span>
			{:else}
				<span class="flex-1"></span>
			{/if}
			{#if running}<Loader size={12} />{/if}
		</CollapsibleTrigger>
		<CollapsibleContent class="ml-4 border-l pl-1">
			{#each steps as s, i (i)}{@render step(s)}{/each}
		</CollapsibleContent>
	</Collapsible>
{/if}
