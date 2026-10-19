<script lang="ts">
	import type { Snippet } from 'svelte';
	import { IsMobile } from '#lib/hooks/is-mobile.svelte.js';

	// The right pane: a column next to the page on desktop, a full-screen layer on phones (closed at first there).
	// It is hidden rather than removed, so closing it keeps its contents (the terminal) running.
	let { open = $bindable(), children }: { open?: boolean; children: Snippet } = $props();

	const mobile = new IsMobile();
	open ??= !mobile.current;
</script>

<aside
	class="{open ? 'flex' : 'hidden'} flex-col bg-background {mobile.current
		? 'fixed inset-0 z-40'
		: 'w-[32rem] max-w-[45%] shrink-0 border-l'}"
>
	{@render children()}
</aside>
