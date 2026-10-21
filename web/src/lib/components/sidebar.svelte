<script lang="ts">
	import { page } from '$app/state';
	import BellIcon from '@lucide/svelte/icons/bell';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ListFilterIcon from '@lucide/svelte/icons/list-filter';
	import SearchIcon from '@lucide/svelte/icons/search';
	import SquarePenIcon from '@lucide/svelte/icons/square-pen';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import IconButton from '#lib/components/icon-button.svelte';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import * as InputGroup from '#lib/components/ui/input-group/index.js';
	import * as Sidebar from '#lib/components/ui/sidebar/index.js';
	import NewThreadDialog from '#lib/components/new-thread-dialog.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { askNotify, matches, projectName, projects, sidebarSections, threads, unread, userName } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';

	const sections = $derived.by(sidebarSections);
	const name = $derived(threads.me?.name ?? '');
	let newOpen = $state(false);

	// On phones the sidebar is a drawer: close it once a link was followed.
	const sidebar = Sidebar.useSidebar();
	$effect(() => {
		void page.url.pathname;
		sidebar.setOpenMobile(false);
	});
</script>

{#snippet title(text: string, n: number)}
	{text}
	<span class="ml-auto rounded-full bg-sidebar-accent px-1.5 text-[10px] tabular-nums">{n}</span>
{/snippet}

{#snippet row(t: Thread, byline = false)}
	<a
		href="/t/{t.id}"
		class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-sidebar-accent/50 {page.params.id === t.id
			? 'bg-sidebar-accent font-medium hover:bg-sidebar-accent'
			: ''}"
	>
		{#if byline}
			<span class="relative shrink-0">
				<UserAvatar name={userName(t.ownerId)} class="size-5" />
				<span class="absolute -right-0.5 -bottom-0.5 flex rounded-full bg-sidebar p-px"><StatusDot status={t.status} /></span>
			</span>
		{:else}<StatusDot status={t.status} />{/if}
		<span class="truncate {unread(t) ? 'font-semibold text-sidebar-foreground' : ''}">{t.title || t.repoUrl.split('/').pop() || t.id}</span>
		{#if unread(t)}<span class="ml-auto size-1.5 shrink-0 rounded-full bg-primary" aria-label="Unread"></span>{/if}
		<span class="shrink-0 text-xs font-normal text-muted-foreground tabular-nums {unread(t) ? '' : 'ml-auto'}">{ago(t.updatedAt, threads.now)}</span>
	</a>
{/snippet}

{#snippet section(text: string, list: Thread[], fold = false, byline = false)}
	{#if list.length && fold}
		<details class="group space-y-0.5">
			<summary class="flex cursor-pointer list-none items-center gap-1 px-2 py-1 text-[11px] font-medium tracking-wider text-muted-foreground uppercase">
				<ChevronRightIcon class="size-3 transition-transform group-open:rotate-90" />
				{@render title(text, list.length)}
			</summary>
			{#each list as t (t.id)}{@render row(t)}{/each}
		</details>
	{:else if list.length}
		<div class="space-y-0.5">
			<h2 class="flex items-center px-2 py-1 text-[11px] font-medium tracking-wider text-muted-foreground uppercase">{@render title(text, list.length)}</h2>
			{#each list as t (t.id)}{@render row(t, byline)}{/each}
		</div>
	{/if}
{/snippet}

<Sidebar.Root>
	<Sidebar.Header class="gap-3 p-3">
		<span class="px-1 text-lg font-semibold tracking-tight">Banda</span>
		<div class="flex items-center gap-1">
			<InputGroup.Root class="h-8">
				<InputGroup.Addon><SearchIcon /></InputGroup.Addon>
				<InputGroup.Input placeholder="Search threads" aria-label="Search threads" bind:value={threads.search} />
			</InputGroup.Root>
			<DropdownMenu.Root>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<IconButton
							{...props}
							icon={ListFilterIcon}
							label={threads.project ? `Project: ${projectName(threads.project)}` : 'Filter by project'}
							variant={threads.project ? 'secondary' : 'ghost'}
						/>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="end" class="w-60">
					<DropdownMenu.RadioGroup bind:value={threads.project}>
						<DropdownMenu.RadioItem value="">All projects</DropdownMenu.RadioItem>
						{#each projects() as p (p)}
							<DropdownMenu.RadioItem value={p} title={p}><span class="truncate">{projectName(p)}</span></DropdownMenu.RadioItem>
						{/each}
					</DropdownMenu.RadioGroup>
				</DropdownMenu.Content>
			</DropdownMenu.Root>
			<IconButton icon={SquarePenIcon} label="New thread" keys="C" onclick={() => (newOpen = true)} />
		</div>
	</Sidebar.Header>
	<Sidebar.Content class="space-y-3 px-2 pb-3">
		{@render section('Mentioned', threads.mentioned.filter(matches), false, true)}
		{@render section('Needs input', sections.needs)}
		{@render section('Working', sections.working, true)}
		{@render section('Snoozed', sections.snoozed, true)}
		{@render section('Settled', sections.settled, true)}
	</Sidebar.Content>
	<Sidebar.Footer class="flex-row items-center gap-2 border-t p-3">
		<UserAvatar {name} class="size-7" />
		<div class="min-w-0 flex-1 leading-tight">
			<div class="truncate text-sm">{name}</div>
			<a href="/auth/logout" data-sveltekit-reload class="text-xs text-muted-foreground hover:underline">Sign out</a>
		</div>
		{#if threads.notify === 'default'}
			<IconButton icon={BellIcon} label="Notify me when I'm mentioned or my agent needs me" onclick={askNotify} />
		{/if}
		<IconButton icon={SettingsIcon} label="Settings" keys="G S" href="/settings" variant={page.url.pathname === '/settings' ? 'secondary' : 'ghost'} />
	</Sidebar.Footer>
</Sidebar.Root>
<NewThreadDialog bind:open={newOpen} />
