<script lang="ts">
	import BellOffIcon from '@lucide/svelte/icons/bell-off';
	import CornerLeftUpIcon from '@lucide/svelte/icons/corner-left-up';
	import FolderIcon from '@lucide/svelte/icons/folder';
	import GitPullRequestIcon from '@lucide/svelte/icons/git-pull-request';
	import PanelRightIcon from '@lucide/svelte/icons/panel-right';
	import SquareIcon from '@lucide/svelte/icons/square';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import IconButton from '#lib/components/icon-button.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import ThreadActions, { isSnoozed } from '#lib/components/thread-actions.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Tooltip, TooltipContent, TooltipTrigger } from '#lib/components/ui/tooltip/index.js';
	import { Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { threadKeys } from '#lib/shortcuts.svelte.js';
	import { userName, waiting } from '#lib/threads.svelte.js';

	// The title gets the room; at a glance: status, Stop, Push or the pull request, presence, the pane. The rest is in
	// the "…" menu (ThreadActions). here: the others with the thread open. The actions are the command palette's too.
	// It sits in the thread page's @container, so it adapts to the column's width, phones included.
	let {
		thread,
		owner,
		offline,
		here,
		act,
		interrupt,
		toggleMode,
		retry,
		push,
		forkOpen = $bindable(),
		spawnOpen = $bindable(),
		handoffOpen = $bindable(),
		paneOpen = $bindable()
	}: {
		thread?: Thread;
		owner: boolean;
		offline: boolean;
		here: string[];
		act: (call: Promise<unknown>) => Promise<boolean>;
		interrupt: () => void;
		toggleMode: () => void;
		retry: () => void;
		push: () => void;
		forkOpen: boolean;
		spawnOpen: boolean;
		handoffOpen: boolean;
		paneOpen?: boolean;
	} = $props();

	const pr = $derived(thread?.pullRequestUrl ?? '');
</script>

<PageHeader>
	<h1 class="min-w-0 flex-1 truncate text-sm font-medium" title={thread?.title}>{thread?.title || 'Untitled thread'}</h1>
	{#if thread}
		<span class="@lg:hidden"><StatusDot status={thread.status} waiting={waiting(thread)} /></span>
		<span class="hidden @lg:contents"><StatusDot status={thread.status} label waiting={waiting(thread)} /></span>
		{#if thread.settled}
			<span class="hidden text-xs text-muted-foreground @2xl:inline">Settled</span>
		{:else if isSnoozed(thread)}
			<BellOffIcon class="hidden size-3.5 shrink-0 text-muted-foreground @2xl:block" aria-label="Snoozed" />
		{/if}
		{#if !thread.repoUrl}
			<span class="hidden shrink-0 items-center gap-1 text-xs text-muted-foreground @2xl:flex" title="The agent works in a folder that lives only in this thread's environment: nothing is pushed.">
				<FolderIcon class="size-3.5" />No repository
			</span>
		{/if}
	{/if}
	{#if thread?.parentId}
		<a href="/t/{thread.parentId}" class="hidden max-w-48 min-w-0 shrink items-center gap-1 text-xs text-muted-foreground hover:text-foreground @3xl:flex" title="Spawned from {thread.parentTitle}">
			<CornerLeftUpIcon class="size-3.5 shrink-0" /><span class="truncate">from {thread.parentTitle || 'its parent'}</span>
		</a>
	{/if}
	{#if offline}<span class="shrink-0 text-xs text-muted-foreground">reconnecting…</span>{/if}
	<div class="flex shrink-0 items-center gap-0.5">
		{#if here.length}
			<div class="mr-1.5 flex -space-x-1.5" role="group" aria-label="Here now">
				{#each here.slice(0, 4) as u, i (u)}
					<Tooltip>
						<TooltipTrigger>
							{#snippet child({ props })}
								<!-- Phones show one face and the count. -->
								<span {...props} class={i ? 'hidden @lg:inline' : ''}><UserAvatar name={userName(u)} class="size-6 ring-2 ring-background" /></span>
							{/snippet}
						</TooltipTrigger>
						<TooltipContent>{userName(u)} · here now</TooltipContent>
					</Tooltip>
				{/each}
				{#if here.length > 1}
					<span class="flex size-6 items-center justify-center rounded-full bg-muted text-[10px] font-medium ring-2 ring-background @lg:hidden">+{here.length - 1}</span>
				{/if}
				{#if here.length > 4}
					<Tooltip>
						<TooltipTrigger>
							{#snippet child({ props })}
								<span {...props} class="hidden size-6 items-center justify-center rounded-full bg-muted text-[10px] font-medium ring-2 ring-background @lg:flex">+{here.length - 4}</span>
							{/snippet}
						</TooltipTrigger>
						<TooltipContent>{here.slice(4).map(userName).join(', ')} · here now</TooltipContent>
					</Tooltip>
				{/if}
			</div>
		{/if}
		{#if owner && thread?.status === Status.WORKING}
			<Button variant="destructive" size="sm" class="mr-1 h-7" onclick={interrupt}><SquareIcon class="fill-current" /> Stop</Button>
		{/if}
		{#if pr}
			<IconButton icon={GitPullRequestIcon} label="Open a pull request" href={pr} target="_blank" />
		{:else if owner && thread?.repoUrl}
			<IconButton icon={UploadIcon} label="Push the branch to origin" onclick={push} />
		{/if}
		<IconButton icon={PanelRightIcon} label="Toggle right pane" keys={threadKeys.pane} variant={paneOpen ? 'secondary' : 'ghost'} onclick={() => (paneOpen = !paneOpen)} />
		{#if thread}
			<ThreadActions {thread} {owner} {act} {toggleMode} {retry} onfork={() => (forkOpen = true)} onspawn={() => (spawnOpen = true)} onhandoff={() => (handoffOpen = true)} />
		{/if}
	</div>
</PageHeader>
