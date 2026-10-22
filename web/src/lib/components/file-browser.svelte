<script lang="ts">
	import { untrack } from 'svelte';
	import { ConnectError } from '@connectrpc/connect';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import FilesIcon from '@lucide/svelte/icons/files';
	import * as Code from '#lib/components/ai-elements/code/index.js';
	import type { SupportedLanguage } from '#lib/components/ai-elements/code/shiki.js';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import IconButton from '#lib/components/icon-button.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { api } from '#lib/api.js';
	import type { ReadFileResponse } from '#lib/gen/banda/v1/banda_pb.js';

	// Read-only: a filterable list of the checkout's files, and one file's content.
	// `version` changes whenever the agent may have changed files (the diff, or without a repository the
	// thread's status), so it triggers a refresh.
	let { threadId, version }: { threadId: string; version: string } = $props();

	let paths = $state<string[]>([]);
	let truncated = $state(false);
	let loaded = $state(false);
	let error = $state('');
	let filter = $state('');
	let open = $state<string>();
	let file = $state<ReadFileResponse>();
	let fileError = $state('');

	const langs: Record<string, SupportedLanguage> = {
		sh: 'bash', bash: 'bash', diff: 'diff', patch: 'diff', js: 'javascript', mjs: 'javascript', cjs: 'javascript',
		json: 'json', svelte: 'svelte', ts: 'typescript', py: 'python', tsx: 'tsx', jsx: 'jsx', css: 'css',
		go: 'go', yaml: 'yaml', yml: 'yaml', html: 'html'
	};
	const lang = $derived(langs[/\.(\w+)$/.exec(open ?? '')?.[1] ?? ''] ?? 'text');
	const shown = $derived(paths.filter((p) => p.toLowerCase().includes(filter.trim().toLowerCase())));

	async function list() {
		try {
			({ paths, truncated } = await api.listFiles({ threadId }));
			error = '';
		} catch (e) {
			error = ConnectError.from(e).rawMessage;
		}
		loaded = true;
	}

	async function read(path: string) {
		try {
			const res = await api.readFile({ threadId, path });
			if (open === path) [file, fileError] = [res, ''];
		} catch (e) {
			if (open === path) [file, fileError] = [undefined, ConnectError.from(e).rawMessage];
		}
	}

	$effect(() => {
		version;
		untrack(() => {
			list();
			if (open) read(open);
		});
	});
</script>

{#snippet note(text: string)}
	<p class="px-3 py-4 text-center text-sm text-muted-foreground first-letter:uppercase">{text}</p>
{/snippet}

{#if open}
	<div class="sticky top-0 z-10 flex items-center gap-1 border-b bg-card px-2 py-1">
		<IconButton icon={ArrowLeftIcon} label="Back to the files" onclick={() => (open = undefined)} />
		<span class="min-w-0 flex-1 truncate font-mono text-xs" title={open}>{open}</span>
	</div>
	{#if fileError}
		{@render note(fileError)}
	{:else if !file}
		<div class="flex justify-center p-6"><Loader class="text-muted-foreground" /></div>
	{:else if file.binary}
		{@render note('Binary file, not shown.')}
	{:else}
		<!-- data-code-overflow lifts the code block's own 650px height cap. -->
		<Code.Root code={file.content.replace(/\n$/, '')} {lang} data-code-overflow class="rounded-none border-0 bg-transparent">
			<Code.CopyButton />
		</Code.Root>
		{#if file.truncated}{@render note('Only the first 512 KiB are shown.')}{/if}
	{/if}
{:else if error}
	<div class="flex flex-col items-center gap-3 p-6">
		<FilesIcon class="size-6 text-muted-foreground" />
		<p class="text-center text-sm text-muted-foreground first-letter:uppercase">{error}</p>
		<Button size="sm" variant="outline" onclick={list}>Reload</Button>
	</div>
{:else if !loaded}
	<div class="flex justify-center p-6"><Loader class="text-muted-foreground" /></div>
{:else}
	<div class="sticky top-0 z-10 border-b bg-card p-2">
		<Input bind:value={filter} placeholder="Filter files" class="h-7 text-xs" />
	</div>
	<p class="px-3 py-1.5 text-xs text-muted-foreground">
		{shown.length} of {paths.length} files{truncated ? ', the list is truncated' : ''}
	</p>
	{#each shown as p (p)}
		{@const dir = p.slice(0, p.lastIndexOf('/') + 1)}
		<button
			class="flex w-full px-3 py-1 text-left pointer-coarse:py-2.5 font-mono text-xs hover:bg-muted"
			title={p}
			onclick={() => {
				file = undefined;
				fileError = '';
				open = p;
				read(p);
			}}
		>
			<span class="min-w-0 truncate text-muted-foreground">{dir}</span><span class="shrink-0">{p.slice(dir.length)}</span>
		</button>
	{/each}
{/if}
