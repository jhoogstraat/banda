<script lang="ts">
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import WrenchIcon from '@lucide/svelte/icons/wrench';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import { Response } from '#lib/components/ai-elements/response/index.js';
	import ToolInput from '#lib/components/ai-elements/tool/tool-input.svelte';
	import ToolOutput from '#lib/components/ai-elements/tool/tool-output.svelte';
	import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '#lib/components/ui/collapsible/index.js';
	import { page } from '$app/state';
	import { api } from '#lib/api.js';
	import type { Tool } from '#lib/events.js';
	import { describe, diff, exitCode, icons, input, lines } from '#lib/tools.js';
	import Self from './tool-call.svelte';

	let { tool }: { tool: Tool } = $props();

	const name = $derived(tool.call.name);
	const result = $derived(tool.result);
	const head = $derived(describe(tool));
	const Icon = $derived(icons[name] ?? WrenchIcon);
	const code = $derived(name === 'Bash' ? exitCode(tool) : undefined);
	const failed = $derived(!!result?.isError);
	const subagent = $derived(name === 'Task' || name === 'Agent');

	// Long outputs show their first lines; "show more" renders the rest.
	const FOLD = 20;
	let full = $state(false);

	// The stream cuts large inputs and outputs; this loads both events in full.
	let loading = $state(false);
	async function loadAll() {
		loading = true;
		try {
			for (const seq of [tool.seq, tool.resultSeq]) {
				if (!seq) continue;
				const [e] = (await api.history({ threadId: page.params.id!, beforeSeq: seq + 1n, limit: 1, full: true })).events;
				if (e?.kind.case === 'toolCall') tool.call = e.kind.value;
				if (e?.kind.case === 'toolResult') tool.result = e.kind.value;
			}
			tool.cut = false;
			full = true;
		} finally {
			loading = false;
		}
	}
	const tone = (l: string) =>
		l === '@@'
			? 'text-muted-foreground/60'
			: l[0] === '+'
				? 'bg-green-500/15 text-green-700 dark:text-green-300'
				: l[0] === '-'
					? 'bg-red-500/15 text-red-700 dark:text-red-300'
					: 'text-muted-foreground';
</script>

{#snippet more(n: number)}
	{#if n > FOLD && !full}
		<button class="w-full px-3 py-1 text-left text-xs text-muted-foreground hover:text-foreground" onclick={() => (full = true)}>
			Show {n - FOLD} more lines
		</button>
	{/if}
{/snippet}

{#snippet output(text: string)}
	{@const all = lines(text)}
	<div class="my-1 overflow-hidden rounded-md {failed ? 'bg-destructive/10 text-destructive' : 'bg-muted/50'}">
		<pre class="overflow-x-auto p-3 font-mono text-xs whitespace-pre-wrap">{(full ? all : all.slice(0, FOLD)).join('\n')}</pre>
		{@render more(all.length)}
	</div>
{/snippet}

<Collapsible class="group/tool">
	<CollapsibleTrigger class="flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs hover:bg-muted/50">
		<Icon class="size-3.5 shrink-0 text-muted-foreground" />
		{#if head.label}<span class="max-w-1/2 shrink-0 truncate font-medium">{head.label}</span>{/if}
		<span class="min-w-0 flex-1 truncate font-mono text-muted-foreground" title={head.detail}>{head.detail}</span>
		{#if head.meta}<span class="shrink-0 font-mono text-muted-foreground">{head.meta}</span>{/if}
		{#if !result}
			<Loader size={12} class="text-muted-foreground" />
		{:else if code}
			<span class="shrink-0 font-mono text-destructive">exit {code}</span>
		{:else if failed}
			<XCircleIcon class="size-3.5 shrink-0 text-destructive" />
		{/if}
		<ChevronRightIcon class="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/tool:rotate-90" />
	</CollapsibleTrigger>
	<CollapsibleContent class="mb-1 ml-4 border-l pl-2">
		{#if tool.cut}
			<button class="px-1 text-xs text-muted-foreground hover:text-foreground" disabled={loading} onclick={loadAll}>
				{loading ? 'Loading…' : 'Cut at 4 KB · load all'}
			</button>
		{/if}
		{@const edits = diff(tool)}
		{#if name === 'Bash'}
			{@const out = lines(result?.output)}
			<div class="my-1 overflow-hidden rounded-md bg-zinc-950 font-mono text-xs text-zinc-200">
				<pre class="overflow-x-auto p-3 whitespace-pre-wrap"><span class="text-emerald-400">$</span> {input(tool).command}{#if out.length}{'\n'}{(full ? out : out.slice(0, FOLD)).join('\n')}{/if}</pre>
				{@render more(out.length)}
				{#if code !== undefined}
					<div class="border-t border-zinc-800 px-3 py-1 {code ? 'text-red-400' : 'text-zinc-500'}">exit {code}</div>
				{/if}
			</div>
		{:else if edits}
			<div class="my-1 overflow-hidden rounded-md bg-muted/30">
				<pre class="overflow-x-auto py-1 font-mono text-xs leading-5">{#each full ? edits : edits.slice(0, FOLD) as line, i (i)}<div class="px-3 {tone(line)}">{line === '@@' ? '⋯' : line || ' '}</div>{/each}</pre>
				{@render more(edits.length)}
			</div>
			{#if failed}{@render output(result!.output)}{/if}
		{:else if subagent}
			{#each tool.children as child (child.call.id)}<Self tool={child} />{/each}
			{#if result}
				<div class="my-1 max-h-72 overflow-auto rounded-md bg-muted/30 px-3 py-2 text-sm">
					<Response content={result.output} class="h-auto" />
				</div>
			{/if}
		{:else if name in icons}
			{#if result}{@render output(result.output)}{/if}
		{:else}
			<ToolInput input={input(tool)} />
			{#if result}
				<ToolOutput output={failed ? undefined : result.output} errorText={failed ? result.output || 'Error' : undefined} />
			{/if}
		{/if}
	</CollapsibleContent>
</Collapsible>
