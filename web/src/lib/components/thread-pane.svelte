<script lang="ts">
	import XIcon from '@lucide/svelte/icons/x';
	import Browser from '#lib/components/browser.svelte';
	import DiffView from '#lib/components/diff-view.svelte';
	import FileBrowser from '#lib/components/file-browser.svelte';
	import IconButton from '#lib/components/icon-button.svelte';
	import SidePane from '#lib/components/side-pane.svelte';
	import Terminal from '#lib/components/terminal.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Tabs, TabsContent, TabsList, TabsTrigger } from '#lib/components/ui/tabs/index.js';
	import { api } from '#lib/api.js';
	import type { Comment } from '#lib/diff.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';

	// A thread's right pane. pending, focus, ask, posted and onsend are the Diff tab's review (see DiffView).
	let {
		threadId,
		thread,
		owner,
		open = $bindable(),
		tab = $bindable(),
		pending = $bindable(),
		focus = $bindable(),
		ask = $bindable(),
		posted,
		onsend
	}: {
		threadId: string;
		thread?: Thread;
		owner: boolean;
		open?: boolean;
		tab: string;
		pending: Comment[];
		focus?: Comment;
		ask: boolean;
		posted: (Comment & { by: string })[];
		onsend: (summary: string) => Promise<boolean>;
	} = $props();

	// The terminal, the file browser and the browser mount when their tab is first opened and then stay alive.
	let termOpened = $state(false);
	let filesOpened = $state(false);
	let browserOpened = $state(false);
	// A thread without a repository has no diff: its files are its work.
	const noRepo = $derived(!!thread && !thread.repoUrl);
	$effect(() => {
		if (noRepo && tab === 'diff') tab = 'files';
	});
	$effect(() => {
		if (tab === 'terminal') termOpened = true;
		if (tab === 'files') filesOpened = true;
		if (tab === 'browser') browserOpened = true;
	});
</script>

<SidePane bind:open>
	<Tabs bind:value={tab} class="min-h-0 flex-1 gap-0">
		<TabsList variant="line" class="group-data-horizontal/tabs:h-12 w-full shrink-0 justify-start border-b px-2">
			{#if !noRepo}
				<TabsTrigger value="diff">Diff{#if pending.length}<Badge variant="secondary" class="ml-1 h-4 px-1 text-[10px]">{pending.length}</Badge>{/if}</TabsTrigger>
			{/if}
			<TabsTrigger value="files">Files</TabsTrigger>
			<TabsTrigger value="browser">Browser</TabsTrigger>
			<TabsTrigger value="terminal">Terminal</TabsTrigger>
			<IconButton icon={XIcon} label="Close the pane" class="ml-auto" onclick={() => (open = false)} />
		</TabsList>
		<TabsContent value="diff" class="flex min-h-0 flex-col">
			<DiffView diff={thread?.diff ?? ''} bind:pending bind:focus bind:ask {posted} {onsend} />
		</TabsContent>
		<TabsContent value="files" class="min-h-0 overflow-auto">
			{#if filesOpened}<FileBrowser {threadId} version={noRepo ? String(thread?.status) : (thread?.diff ?? '')} />{/if}
		</TabsContent>
		{#if browserOpened}
			<!-- Hidden rather than unmounted, so the framed app keeps its state. -->
			<div class="min-h-0 flex-1 {tab === 'browser' ? '' : 'hidden'}">
				<Browser {threadId} status={thread?.status} {owner} />
			</div>
		{/if}
		{#if owner && termOpened}
			<div class="min-h-0 flex-1 {tab === 'terminal' ? '' : 'hidden'}">
				<Terminal
					open={(cols, rows, signal) => api.openTerminal({ threadId, cols, rows }, { signal })}
					write={(data) => api.writeTerminal({ threadId, data })}
					resize={(cols, rows) => api.resizeTerminal({ threadId, cols, rows })}
				/>
			</div>
		{:else if tab === 'terminal'}
			<p class="p-4 text-sm text-muted-foreground">Only the thread owner can open the terminal.</p>
		{/if}
	</Tabs>
</SidePane>
