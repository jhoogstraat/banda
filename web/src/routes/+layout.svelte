<script lang="ts">
	import ConfirmDialog from '#lib/components/confirm-dialog.svelte';
	import './layout.css';
	import favicon from '#lib/assets/favicon.svg';
	import { page } from '$app/state';
	import { ModeWatcher, mode, theme } from 'mode-watcher';
	import CommandPalette from '#lib/components/command-palette.svelte';
	import AppSidebar from '#lib/components/sidebar.svelte';
	import * as Sidebar from '#lib/components/ui/sidebar/index.js';
	import { TooltipProvider } from '#lib/components/ui/tooltip/index.js';
	import { onkeydown, ui } from '#lib/shortcuts.svelte.js';
	import { attention, loadThreads, threads, watchInbox } from '#lib/threads.svelte.js';
	import { themeOf } from '#lib/themes.js';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	// The sign-in page shows without a session: no sidebar, no calls that need one.
	const bare = $derived(page.url.pathname === '/signin');

	$effect(() => {
		if (bare) return;
		const ctrl = new AbortController();
		// Until it loads, the app doesn't know who is signed in (and so who owns what): retry.
		let retry: ReturnType<typeof setTimeout>;
		const load = () => loadThreads().catch(() => (retry = setTimeout(load, 2000)));
		load();
		watchInbox(ctrl.signal);
		const timer = setInterval(() => (threads.now = Date.now()), 30_000);
		return () => {
			ctrl.abort();
			clearTimeout(retry);
			clearInterval(timer);
		};
	});
	const count = $derived(attention());
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<!-- Here, not through ModeWatcher's themeColors: its meta tag doesn't follow a change of theme in light mode. -->
	<meta name="theme-color" content={themeOf(theme.current).colors[mode.current ?? 'dark']} />
	<title>{count ? `(${count}) ` : ''}{ui.title ? `${ui.title} · ` : ''}Banda</title>
</svelte:head>

<svelte:window {onkeydown} />

<!-- The mode (System, Light or Dark) and the theme are chosen in Settings, per browser; app.html applies them before the app loads. -->
<ModeWatcher />

{#if bare}
	{@render children()}
{:else}
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
		<ConfirmDialog />
	</TooltipProvider>
{/if}
