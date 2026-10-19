<script lang="ts">
	import { page } from '$app/state';
	import { timestampMs } from '@bufbuild/protobuf/wkt';
	import AtSignIcon from '@lucide/svelte/icons/at-sign';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import { Button } from '#lib/components/ui/button/index.js';
	import { NativeSelect, NativeSelectOptGroup, NativeSelectOption } from '#lib/components/ui/native-select/index.js';
	import StatusDot from '#lib/components/status-dot.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import { Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { threads, userName, view } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';

	// Precedence: settled > snoozed > status.
	const sectionOf = (t: Thread) =>
		t.settled
			? 'settled'
			: t.snoozedUntil && timestampMs(t.snoozedUntil) > Date.now()
				? 'snoozed'
				: t.status === Status.STARTING || t.status === Status.WORKING
					? 'working'
					: 'needs';

	const sections = $derived.by(() => {
		const s: Record<string, Thread[]> = { needs: [], working: [], snoozed: [], settled: [] };
		for (const t of threads.list) s[sectionOf(t)].push(t);
		return s;
	});
	const others = $derived(threads.users.filter((u) => u.id !== threads.me?.id));
	const viewed = $derived(others.find((u) => u.id === threads.viewing));
	const name = $derived(threads.me?.name ?? '');
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
		{#if byline}<UserAvatar name={userName(t.ownerId)} class="size-5" />{:else}<StatusDot status={t.status} />{/if}
		<span class="truncate">{t.title || t.repoUrl.split('/').pop() || t.id}</span>
		<span class="ml-auto shrink-0 text-xs font-normal text-muted-foreground tabular-nums">{ago(t.updatedAt)}</span>
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

<aside class="flex w-64 shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground">
	<div class="flex flex-col gap-3 p-3">
		<span class="px-1 text-lg font-semibold tracking-tight">Banda</span>
		<Button href="/" variant="outline"><PlusIcon /> New thread</Button>
		<NativeSelect aria-label="People" value={threads.viewing} onchange={(e) => view(e.currentTarget.value)} class="w-full">
			<NativeSelectOption value="">My threads</NativeSelectOption>
			<NativeSelectOptGroup label="People">
				{#each others as u (u.id)}<NativeSelectOption value={u.id}>{u.name}</NativeSelectOption>{/each}
			</NativeSelectOptGroup>
		</NativeSelect>
	</div>
	<nav class="flex-1 space-y-3 overflow-y-auto px-2 pb-3">
		{#if threads.viewing}
			<div class="flex items-center justify-between px-2 pb-1 text-sm font-medium">
				<span class="truncate">{viewed?.name ?? threads.viewing}'s threads</span>
				<Button variant="ghost" size="xs" onclick={() => view('')}>My threads</Button>
			</div>
		{:else}
			{@render section('Mentioned', threads.mentioned, false, true)}
		{/if}
		{#if threads.mentionsOnly}
			{#if !threads.mentioned.length}<p class="px-2 text-sm text-muted-foreground">No open mentions.</p>{/if}
		{:else}
			{@render section('Needs input', sections.needs)}
			{@render section('Working', sections.working, true)}
			{@render section('Snoozed', sections.snoozed, true)}
			{@render section('Settled', sections.settled, true)}
		{/if}
	</nav>
	<div class="flex items-center gap-2 border-t p-3">
		<UserAvatar {name} class="size-7" />
		<div class="min-w-0 flex-1 leading-tight">
			<div class="truncate text-sm">{name}</div>
			<a href="/auth/logout" data-sveltekit-reload class="text-xs text-muted-foreground hover:underline">Sign out</a>
		</div>
		<Button
			variant={threads.mentionsOnly ? 'secondary' : 'ghost'}
			size="sm"
			title="Only threads you are mentioned in"
			aria-pressed={threads.mentionsOnly}
			onclick={() => view('', !threads.mentionsOnly)}
		>
			<AtSignIcon /> {threads.mentioned.length}
		</Button>
		<Button variant="ghost" size="icon" aria-label="Settings"><SettingsIcon /></Button>
	</div>
</aside>
