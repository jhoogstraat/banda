<script lang="ts">
	import './layout.css';
	import { page } from '$app/state';
	import { ModeWatcher, setMode } from 'mode-watcher';
	import Sidebar from '#lib/components/sidebar.svelte';
	import { TooltipProvider } from '#lib/components/ui/tooltip/index.js';
	import { loadThreads } from '#lib/threads.svelte.js';
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

<TooltipProvider>
	<div class="flex h-dvh">
		<Sidebar />
		<main class="flex min-w-0 flex-1">
			{#key page.url.pathname}
				{@render children()}
			{/key}
		</main>
	</div>
</TooltipProvider>
