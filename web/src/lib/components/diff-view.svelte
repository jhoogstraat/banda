<script lang="ts">
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import GitCompareIcon from '@lucide/svelte/icons/git-compare';
	import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '#lib/components/ui/collapsible/index.js';

	// Plain unified diff, one collapsible section per file.
	let { diff }: { diff: string } = $props();

	const tone = (l: string) =>
		l.startsWith('@@')
			? 'bg-blue-500/10 text-blue-300'
			: l[0] === '+'
				? 'bg-green-500/15 text-green-300'
				: l[0] === '-'
					? 'bg-red-500/15 text-red-300'
					: 'text-muted-foreground';

	const files = $derived(
		diff
			.split(/^(?=diff --git )/m)
			.filter(Boolean)
			.map((chunk) => {
				const lines = chunk.replace(/\n$/, '').split('\n');
				const hunk = lines.findIndex((l) => l.startsWith('@@'));
				// Without hunks (binary, mode change) show the header lines instead.
				const body = lines.slice(hunk < 0 ? 1 : hunk);
				return {
					path: /^diff --git a\/.* b\/(.*)$/.exec(lines[0])?.[1] ?? lines[0],
					body,
					add: body.filter((l) => l[0] === '+').length,
					del: body.filter((l) => l[0] === '-').length
				};
			})
	);
</script>

{#if files.length}
	<p class="px-3 py-2 text-xs text-muted-foreground">
		{files.length} file{files.length > 1 ? 's' : ''} changed
		<span class="font-mono text-green-400">+{files.reduce((n, f) => n + f.add, 0)}</span>
		<span class="font-mono text-red-400">−{files.reduce((n, f) => n + f.del, 0)}</span>
	</p>
	{#each files as f (f.path)}
		<Collapsible open class="group/file border-t">
			<CollapsibleTrigger class="sticky top-0 z-10 flex w-full items-center gap-2 bg-card px-3 py-1.5 text-left text-xs hover:bg-muted">
				<ChevronRightIcon class="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/file:rotate-90" />
				<span class="min-w-0 flex-1 truncate font-mono" title={f.path}>{f.path}</span>
				<span class="shrink-0 font-mono text-green-400">+{f.add}</span>
				<span class="shrink-0 font-mono text-red-400">−{f.del}</span>
			</CollapsibleTrigger>
			<CollapsibleContent class="overflow-x-auto">
				<pre class="min-w-max py-1 font-mono text-xs leading-5">{#each f.body as line}<div class="px-3 {tone(line)}">{line || ' '}</div>{/each}</pre>
			</CollapsibleContent>
		</Collapsible>
	{/each}
{:else}
	<div class="flex flex-col items-center gap-2 p-10 text-sm text-muted-foreground">
		<GitCompareIcon class="size-6" />
		No changes yet
	</div>
{/if}
