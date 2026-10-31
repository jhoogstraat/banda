<script lang="ts">
	import { page } from '$app/state';
	import LoadState from '#lib/components/load-state.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader />
	{#if page.status === 404}
		<LoadState title="Page not found" description="The link is wrong, or the page has moved." />
	{:else}
		<LoadState title="Something went wrong" description={page.error?.message ?? 'Banda hit an unexpected error.'} retry={() => location.reload()} />
	{/if}
</div>
