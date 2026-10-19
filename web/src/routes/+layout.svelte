<script lang="ts">
	import './layout.css';
	import { page } from '$app/state';
	import { ModeWatcher, setMode } from 'mode-watcher';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import { Avatar, AvatarFallback } from '#lib/components/ui/avatar/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import StatusDot from '#lib/components/status-dot.svelte';
	import { loadThreads, threads } from '#lib/threads.svelte.js';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	// Dark by default. ModeWatcher's defaultMode only applies to a server-rendered head script.
	setMode('dark');

	$effect(() => {
		const poll = () => loadThreads().catch(() => {});
		poll();
		// ponytail: polling, replace with a watch stream when needed
		const timer = setInterval(poll, 5000);
		return () => clearInterval(timer);
	});
</script>

<svelte:head>
	<title>Banda</title>
</svelte:head>

<ModeWatcher />

<div class="flex h-dvh">
	<aside class="flex w-64 shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground">
		<div class="flex flex-col gap-3 p-3">
			<span class="px-1 text-lg font-semibold tracking-tight">Banda</span>
			<Button href="/" variant="outline"><PlusIcon /> New thread</Button>
		</div>
		<nav class="flex-1 space-y-0.5 overflow-y-auto px-2">
			{#each threads.list as t (t.id)}
				<a
					href="/t/{t.id}"
					class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-sidebar-accent {page.params
						.id === t.id
						? 'bg-sidebar-accent'
						: ''}"
				>
					<StatusDot status={t.status} />
					<span class="truncate">{t.title || t.repoUrl.split('/').pop() || t.id}</span>
				</a>
			{/each}
		</nav>
		<div class="flex items-center justify-between border-t p-3">
			<div class="flex items-center gap-2 text-sm">
				<Avatar class="size-7"><AvatarFallback>D</AvatarFallback></Avatar> dev
			</div>
			<Button variant="ghost" size="icon" aria-label="Settings"><SettingsIcon /></Button>
		</div>
	</aside>
	<main class="flex min-w-0 flex-1">
		{#key page.url.pathname}
			{@render children()}
		{/key}
	</main>
</div>
