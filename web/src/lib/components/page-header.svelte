<script lang="ts">
	import type { Snippet } from 'svelte';
	import { SidebarTrigger } from '#lib/components/ui/sidebar/index.js';

	// The top bar of every page. Its sidebar toggle collapses the column on desktop and opens the drawer on phones.
	let { children }: { children?: Snippet } = $props();
</script>

<header class="flex h-12 shrink-0 items-center gap-2 border-b px-2">
	<SidebarTrigger />
	{@render children?.()}
</header>
