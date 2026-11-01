<script lang="ts">
	// One row of Accounts in Settings: the person's connection to a forge host (GitHub, GitLab). Connected as
	// @login with Disconnect, broken with Reconnect, or not connected with Connect; the forge's mark says which.
	import type { Snippet } from 'svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import * as Avatar from '#lib/components/ui/avatar/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import type { ForgeConnection } from '#lib/gen/banda/v1/banda_pb.js';
	import { initials, since } from '#lib/utils.js';

	let {
		name,
		host,
		showHost = false,
		conn,
		connectUrl,
		busy = false,
		ondisconnect,
		mark,
		note
	}: {
		/** "GitHub", "GitLab": the buttons say "Connect GitHub". */
		name: string;
		host: string;
		/** Name the host too: it isn't the forge's own, or there are several. */
		showHost?: boolean;
		conn?: ForgeConnection;
		connectUrl: string;
		busy?: boolean;
		ondisconnect: () => void;
		mark: Snippet<[string]>;
		/** What connecting involves, under "Not connected". */
		note?: Snippet;
	} = $props();
</script>

<div class="flex flex-wrap items-center gap-x-3 gap-y-3 p-4">
	<span class="relative shrink-0">
		{#if conn}
			<Avatar.Root class="size-9">
				<Avatar.Image src={conn.avatarUrl} alt="" />
				<Avatar.Fallback class="text-xs">{initials(conn.login)}</Avatar.Fallback>
			</Avatar.Root>
			<span class="absolute -right-1 -bottom-1 grid size-4.5 place-items-center rounded-full bg-background ring-2 ring-background">
				{@render mark('size-3.5')}
			</span>
		{:else}
			<span class="grid size-9 place-items-center rounded-full bg-muted text-muted-foreground">{@render mark('size-5')}</span>
		{/if}
	</span>
	<div class="min-w-0 flex-1 basis-48">
		<p class="truncate text-sm font-medium">
			{name}{#if showHost}<span class="font-normal text-muted-foreground"> on {host}</span>{/if}
		</p>
		<p class="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
			{#if conn?.broken}
				<span class="size-2 shrink-0 rounded-full bg-attention"></span>
				<span class="text-attention-text">{name} no longer accepts your connection as @{conn.login}</span>
			{:else if conn}
				<span class="size-2 shrink-0 rounded-full bg-success"></span>
				<span class="truncate">
					Connected as <span class="text-foreground">@{conn.login}</span>
					<span title={conn.connectedAt && timestampDate(conn.connectedAt).toLocaleString()}>· {since(conn.connectedAt)}</span>
				</span>
			{:else}
				<span class="size-2 shrink-0 rounded-full border border-muted-foreground"></span>
				<span>Not connected{#if note}. {@render note()}{/if}</span>
			{/if}
		</p>
	</div>
	<div class="ml-auto flex gap-1">
		{#if !conn || conn.broken}
			<Button size="sm" href={connectUrl} data-sveltekit-reload>{@render mark('size-3.5')} {conn ? 'Reconnect' : 'Connect'} {name}</Button>
		{/if}
		{#if conn}
			<Button size="sm" variant={conn.broken ? 'ghost' : 'outline'} disabled={busy} onclick={ondisconnect} aria-label="Disconnect {name} on {host}">
				{busy ? 'Disconnecting…' : 'Disconnect'}
			</Button>
		{/if}
	</div>
</div>
