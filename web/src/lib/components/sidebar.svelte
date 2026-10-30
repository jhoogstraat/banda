<script lang="ts">
	import { page } from '$app/state';
	import BellIcon from '@lucide/svelte/icons/bell';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ChevronsUpDownIcon from '@lucide/svelte/icons/chevrons-up-down';
	import InboxIcon from '@lucide/svelte/icons/inbox';
	import KeyboardIcon from '@lucide/svelte/icons/keyboard';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import ListFilterIcon from '@lucide/svelte/icons/list-filter';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import SearchIcon from '@lucide/svelte/icons/search';
	import SquarePenIcon from '@lucide/svelte/icons/square-pen';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import UserIcon from '@lucide/svelte/icons/user';
	import UsersIcon from '@lucide/svelte/icons/users';
	import IconButton from '#lib/components/icon-button.svelte';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import * as InputGroup from '#lib/components/ui/input-group/index.js';
	import * as Sidebar from '#lib/components/ui/sidebar/index.js';
	import NewThreadDialog from '#lib/components/new-thread-dialog.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import PullRequestChip from '#lib/components/pull-request-chip.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { asking, asks, raceTitle, size, type Family } from '#lib/inbox.js';
	import { askNotify, matches, noRepo, projectName, projects, sidebarSections, threads, unfolded, unread, userName, waiting } from '#lib/threads.svelte.js';
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

<!-- An account menu entry; sign-out leaves the SPA with a full reload. -->
{#snippet link(href: string, Icon: typeof UserIcon, label: string, keys = '', reload = false)}
	<DropdownMenu.Item>
		{#snippet child({ props })}
			<a {href} {...props} data-sveltekit-reload={reload || undefined}>
				<Icon />{label}{#if keys}<DropdownMenu.Shortcut>{keys}</DropdownMenu.Shortcut>{/if}
			</a>
		{/snippet}
	</DropdownMenu.Item>
{/snippet}

<!-- A thread, or with race the race's row: its most urgent try t, its title and count, linking to compare. -->
{#snippet row(t: Thread, byline = false, race?: Family)}
	{@const fresh = race ? race.children.some((c) => unread(c.thread)) : unread(t)}
	{@const wait = waiting(t)}
	<a
		href={race ? `/compare?race=${race.race}` : `/t/${t.id}`}
			title={race ? raceTitle(t) : t.title || undefined}
		class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-sidebar-accent/50 {!race && page.params.id === t.id
			? 'bg-sidebar-accent font-medium hover:bg-sidebar-accent'
			: ''}"
	>
		{#if byline}
			<span class="relative shrink-0">
				<UserAvatar name={userName(t.ownerId)} class="size-5" />
				<span class="absolute -right-0.5 -bottom-0.5 flex rounded-full bg-sidebar p-px"><StatusDot status={t.status} waiting={wait} /></span>
			</span>
		{:else}<StatusDot status={t.status} waiting={wait} />{/if}
		<span class="truncate {fresh ? 'font-semibold text-sidebar-foreground' : ''}">
			{race ? `${raceTitle(t)} · ${race.children.length} tries` : t.title || t.repoUrl.split('/').pop() || t.id}
			{#if t.envWaiting && !race}<span class="block truncate text-xs font-normal text-muted-foreground" title={t.envWaiting}>{t.envWaiting}</span>{/if}
		</span>
		{#if wait && !byline}
			<span class="shrink-0 rounded-full bg-amber-400/20 px-1.5 text-[10px] text-amber-700 dark:text-amber-500" title={wait}>{asking(asks(t))}</span>
		{:else if t.pullRequest && !race}
			<PullRequestChip pr={t.pullRequest} small />
		{/if}
		{#if fresh}<span class="ml-auto size-1.5 shrink-0 rounded-full bg-primary" aria-label="Unread"></span>{/if}
		<span class="shrink-0 text-xs font-normal text-muted-foreground tabular-nums {fresh ? '' : 'ml-auto'}">{ago(t.updatedAt, threads.now)}</span>
	</a>
{/snippet}

<!-- A thread with its sub-threads, folded by default: the fold button shows their status dots. -->
{#snippet family(f: Family, byline = false)}
	{@const open = unfolded(f)}
	<div class="flex items-center">
		<div class="min-w-0 flex-1">{@render row(f.thread, byline, f.race ? f : undefined)}</div>
		{#if f.children.length}
			<button
				class="flex shrink-0 items-center gap-0.5 rounded-md px-1 py-1.5 text-[10px] text-muted-foreground tabular-nums hover:bg-sidebar-accent/50"
				aria-expanded={open}
				aria-label="{open ? 'Fold' : 'Unfold'} {f.children.length} {f.race ? 'tries' : 'sub-threads'}"
				onclick={() => (threads.expanded[f.race ?? f.thread.id] = !open)}
			>
				{#if !open}{#each f.children.slice(0, 4) as c (c.thread.id)}<StatusDot status={c.thread.status} waiting={waiting(c.thread)} />{/each}{/if}
				{f.children.length}
				<ChevronRightIcon class="size-3 transition-transform {open ? 'rotate-90' : ''}" />
			</button>
		{/if}
	</div>
	{#if open}
		<div class="ml-3 space-y-0.5 border-l pl-1">{#each f.children as c (c.thread.id)}{@render family(c)}{/each}</div>
	{/if}
{/snippet}

{#snippet section(text: string, list: Family[], fold = false, byline = false)}
	{@const n = list.reduce((n, f) => n + size(f), 0)}
	{#if text === 'Needs input' && threads.loginNeeded}
		<section class="space-y-0.5" aria-label={text}>
			<h2 class="flex items-center px-2 py-1 text-xs font-medium text-muted-foreground">{@render title(text, n + 1)}</h2>
			<a href="/settings" class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm font-semibold hover:bg-sidebar-accent/50">
				<KeyRoundIcon class="size-3.5 text-amber-500" /> Log in to Claude
			</a>
			{#each list as f (f.race ?? f.thread.id)}{@render family(f, byline)}{/each}
		</section>
	{:else if list.length && fold}
		<details class="group space-y-0.5" aria-label={text}>
			<summary class="flex cursor-pointer list-none items-center gap-1 px-2 py-1 text-xs font-medium text-muted-foreground">
				<ChevronRightIcon class="size-3 transition-transform group-open:rotate-90" />
				{@render title(text, n)}
			</summary>
			{#each list as f (f.race ?? f.thread.id)}{@render family(f)}{/each}
		</details>
	{:else if list.length}
		<section class="space-y-0.5" aria-label={text}>
			<h2 class="flex items-center px-2 py-1 text-xs font-medium text-muted-foreground">{@render title(text, n)}</h2>
			{#each list as f (f.race ?? f.thread.id)}{@render family(f, byline)}{/each}
		</section>
	{/if}
{/snippet}

<Sidebar.Root>
	<Sidebar.Header class="gap-3 p-3">
		<span class="px-1 text-lg font-semibold tracking-tight">Banda</span>
		<div class="flex items-center gap-1">
			<InputGroup.Root class="h-8">
				<InputGroup.Addon><SearchIcon /></InputGroup.Addon>
				<InputGroup.Input placeholder="Search" aria-label="Search threads" bind:value={threads.search} />
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
							<DropdownMenu.RadioItem value={p} title={p === noRepo ? undefined : p}><span class="truncate">{projectName(p)}</span></DropdownMenu.RadioItem>
						{/each}
					</DropdownMenu.RadioGroup>
				</DropdownMenu.Content>
			</DropdownMenu.Root>
			<IconButton icon={UsersIcon} label="Team" keys="G T" href="/team" variant={page.url.pathname === '/team' ? 'secondary' : 'ghost'} />
			<IconButton icon={SquarePenIcon} label="New thread" keys="C" onclick={() => (newOpen = true)} />
		</div>
	</Sidebar.Header>
	<Sidebar.Content class="space-y-3 px-2 pb-3">
		<a
			href="/decisions"
			class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-sidebar-accent/50 {page.url.pathname === '/decisions'
				? 'bg-sidebar-accent font-medium hover:bg-sidebar-accent'
				: ''}"
		>
			<InboxIcon class="size-4 text-muted-foreground" /> Decisions
			{#if threads.decisions.length}
				<span class="ml-auto rounded-full bg-amber-400 px-1.5 text-[10px] font-semibold text-black tabular-nums" aria-label="{threads.decisions.length} pending">
					{threads.decisions.length}
				</span>
			{/if}
		</a>
		{@render section('Mentioned', threads.mentioned.filter(matches).map((thread) => ({ thread, children: [] })), false, true)}
		{@render section('Needs input', sections.needs)}
		{@render section('Ready', sections.ready)}
		{@render section('Working', sections.working, true)}
		{@render section('Earlier', sections.earlier, true)}
		{@render section('Snoozed', sections.snoozed, true)}
		{@render section('Settled', sections.settled, true)}
	</Sidebar.Content>
	<Sidebar.Footer class="border-t p-2">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger class="flex w-full items-center gap-2 rounded-md p-1 text-left hover:bg-sidebar-accent/50" aria-label="Account">
				<UserAvatar {name} class="size-7" />
				<span class="min-w-0 flex-1 truncate text-sm">{name}</span>
				<ChevronsUpDownIcon class="size-4 shrink-0 text-muted-foreground" />
			</DropdownMenu.Trigger>
			<DropdownMenu.Content side="top" align="start" class="w-(--bits-dropdown-menu-anchor-width) min-w-56">
				{@render link(`/u/${threads.me?.id ?? ''}`, UserIcon, 'Your profile')}
				{@render link('/settings', SettingsIcon, 'Settings', 'G S')}
				{#if threads.notify === 'default'}
					<DropdownMenu.Item onSelect={askNotify}><BellIcon />Enable notifications</DropdownMenu.Item>
				{:else}
					{@render link('/settings#notifications', BellIcon, 'Notifications')}
				{/if}
				{@render link('/settings#shortcuts', KeyboardIcon, 'Keyboard shortcuts')}
				<DropdownMenu.Separator />
				{@render link('/auth/logout', LogOutIcon, 'Sign out', '', true)}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</Sidebar.Footer>
</Sidebar.Root>
<NewThreadDialog bind:open={newOpen} />
