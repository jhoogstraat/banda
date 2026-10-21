<script lang="ts">
	import type { Snippet } from 'svelte';
	import SearchIcon from '@lucide/svelte/icons/search';
	import IconButton from '#lib/components/icon-button.svelte';
	import { SidebarTrigger } from '#lib/components/ui/sidebar/index.js';
	import { mod, ui } from '#lib/shortcuts.svelte.js';

	// The top bar of every page. Its sidebar toggle collapses the column on desktop and opens the drawer on phones;
	// the search button opens the command palette (phones have no Cmd-K).
	let { children }: { children?: Snippet } = $props();
</script>

<header class="flex h-12 shrink-0 items-center gap-2 border-b px-2">
	<SidebarTrigger />
	<IconButton icon={SearchIcon} label="Command palette" keys="{mod}K" onclick={() => (ui.palette = true)} />
	{@render children?.()}
</header>
