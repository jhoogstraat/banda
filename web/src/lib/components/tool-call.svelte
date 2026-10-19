<script lang="ts">
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import FilePenIcon from '@lucide/svelte/icons/file-pen';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import GlobeIcon from '@lucide/svelte/icons/globe';
	import SearchIcon from '@lucide/svelte/icons/search';
	import TerminalIcon from '@lucide/svelte/icons/terminal';
	import WrenchIcon from '@lucide/svelte/icons/wrench';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import Tool from '#lib/components/ai-elements/tool/tool.svelte';
	import ToolContent from '#lib/components/ai-elements/tool/tool-content.svelte';
	import ToolInput from '#lib/components/ai-elements/tool/tool-input.svelte';
	import ToolOutput from '#lib/components/ai-elements/tool/tool-output.svelte';
	import { CollapsibleTrigger } from '#lib/components/ui/collapsible/index.js';
	import type { ToolCall, ToolResult } from '#lib/gen/banda/v1/banda_pb.js';

	let { call, result }: { call: ToolCall; result?: ToolResult } = $props();

	const icons = {
		Bash: TerminalIcon,
		Read: FileTextIcon,
		Write: FilePenIcon,
		Edit: FilePenIcon,
		Grep: SearchIcon,
		Glob: SearchIcon,
		WebFetch: GlobeIcon,
		WebSearch: GlobeIcon
	} as Record<string, typeof WrenchIcon>;

	const input = $derived.by(() => {
		try {
			return JSON.parse(call.inputJson);
		} catch {
			return call.inputJson;
		}
	});
	// The most telling argument: a path, a command, a pattern, ...
	const summary = $derived(
		input?.file_path ?? input?.command ?? input?.pattern ?? input?.url ?? input?.query ?? input?.path ?? input?.description ?? ''
	);
	const Icon = $derived(icons[call.name] ?? WrenchIcon);
</script>

<Tool class="group/tool mb-0 border-0">
	<CollapsibleTrigger class="flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs hover:bg-muted/50">
		<Icon class="size-3.5 shrink-0 text-muted-foreground" />
		<span class="shrink-0 font-medium">{call.name}</span>
		<span class="min-w-0 flex-1 truncate font-mono text-muted-foreground">{summary}</span>
		{#if !result}
			<Loader size={12} class="text-muted-foreground" />
		{:else if result.isError}
			<XCircleIcon class="size-3.5 shrink-0 text-destructive" />
		{/if}
		<ChevronRightIcon class="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/tool:rotate-90" />
	</CollapsibleTrigger>
	<ToolContent class="ml-4 border-l">
		<ToolInput {input} />
		{#if result}
			<ToolOutput output={result.isError ? undefined : result.output} errorText={result.isError ? result.output || 'Error' : undefined} />
		{/if}
	</ToolContent>
</Tool>
