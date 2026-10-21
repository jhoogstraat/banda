<script lang="ts">
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import type { Attachment } from '#lib/gen/banda/v1/banda_pb.js';

	let { attachments }: { attachments: Attachment[] } = $props();

	const url = (a: Attachment) => `/files/${a.id}`;
	const size = (n: bigint) => (n < 1024n ? `${n} B` : n < 1048576n ? `${(Number(n) / 1024).toFixed(0)} KB` : `${(Number(n) / 1048576).toFixed(1)} MB`);
	let open = $state<Attachment>();
</script>

<!-- Images as thumbnails that open full size, text files as chips that open in a new tab. -->
<div class="flex flex-wrap gap-2">
	{#each attachments as a (a.id)}
		{#if a.mediaType.startsWith('image/')}
			<button type="button" class="overflow-hidden rounded-md border hover:opacity-90" title={a.name} onclick={() => (open = a)}>
				<img src={url(a)} alt={a.name} loading="lazy" class="size-24 object-cover" />
			</button>
		{:else}
			<a href={url(a)} target="_blank" rel="noopener" class="flex h-8 items-center gap-1.5 rounded-md border px-2 text-xs hover:bg-muted/50">
				<FileTextIcon class="size-3.5 shrink-0 text-muted-foreground" />
				<span class="max-w-48 truncate">{a.name}</span>
				<span class="text-muted-foreground">{size(a.size)}</span>
			</a>
		{/if}
	{/each}
</div>

<Dialog.Root open={!!open} onOpenChange={(o) => !o && (open = undefined)}>
	<Dialog.Content class="max-h-[90dvh] w-auto max-w-[min(90vw,80rem)] sm:max-w-[min(90vw,80rem)]">
		{#if open}
			<Dialog.Header>
				<Dialog.Title class="truncate pr-6 text-sm">
					<a href={url(open)} target="_blank" rel="noopener" class="hover:underline">{open.name}</a>
				</Dialog.Title>
			</Dialog.Header>
			<img src={url(open)} alt={open.name} class="max-h-[75dvh] min-h-0 w-auto object-contain" />
		{/if}
	</Dialog.Content>
</Dialog.Root>
