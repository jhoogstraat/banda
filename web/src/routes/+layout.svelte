<script lang="ts">
	import './layout.css';
	import { page } from '$app/state';
	import { ModeWatcher, setMode } from 'mode-watcher';
	import CommandPalette from '#lib/components/command-palette.svelte';
	import AppSidebar from '#lib/components/sidebar.svelte';
	import * as Sidebar from '#lib/components/ui/sidebar/index.js';
	import { TooltipProvider } from '#lib/components/ui/tooltip/index.js';
	import { onkeydown } from '#lib/shortcuts.svelte.js';
	import { attention, loadThreads, threads, watchInbox } from '#lib/threads.svelte.js';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	// Dark by default. ModeWatcher's defaultMode only applies to a server-rendered head script.
	setMode('dark');

	$effect(() => {
		const ctrl = new AbortController();
		loadThreads().catch(() => {});
		watchInbox(ctrl.signal);
		const timer = setInterval(() => (threads.now = Date.now()), 30_000);
		return () => {
			ctrl.abort();
			clearInterval(timer);
		};
	});
	const count = $derived(attention());
</script>

<svelte:head>
	<title>{count ? `(${count}) ` : ''}Banda</title>
</svelte:head>

<svelte:window {onkeydown} />

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
	<CommandPalette />
</TooltipProvider>
