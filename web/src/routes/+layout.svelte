<script lang="ts">
	import './layout.css';
	import { page } from '$app/state';
	import { ModeWatcher, setMode } from 'mode-watcher';
	import AppSidebar from '#lib/components/sidebar.svelte';
	import * as Sidebar from '#lib/components/ui/sidebar/index.js';
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
	<!-- The only place that knows about screen sizes: the sidebar is a column on desktop and a drawer on phones. -->
	<Sidebar.Provider class="h-dvh">
		<AppSidebar />
		<Sidebar.Inset class="min-w-0 flex-row overflow-hidden">
			{#key page.url.pathname}
				{@render children()}
			{/key}
		</Sidebar.Inset>
	</Sidebar.Provider>
</TooltipProvider>
