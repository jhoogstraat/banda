<script lang="ts" module>
	import { timestampDate, timestampFromDate } from '@bufbuild/protobuf/wkt';
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';

	const at = (days: number, hour = 9) => {
		const d = new Date();
		d.setDate(d.getDate() + days);
		d.setHours(hour, 0, 0, 0);
		return d;
	};
	const later = (h: number) => new Date(Date.now() + h * 3600_000);
	export const presets: [string, () => Date][] = [
		['1 hour', () => later(1)],
		['4 hours', () => later(4)],
		['Tomorrow 09:00', () => at(1)],
		['Next Monday 09:00', () => at((8 - new Date().getDay()) % 7 || 7)]
	];

	// Also used by the command palette. No date unsnoozes.
	export const snooze = (t: Thread, d?: Date) => api.snooze({ threadId: t.id, until: d && timestampFromDate(d) });
	export const settle = (t: Thread) => api.settle({ threadId: t.id, settled: !t.settled });
	export const isSnoozed = (t: Thread) => !!t.snoozedUntil && timestampDate(t.snoozedUntil) > new Date();
</script>

<script lang="ts">
	import BellOffIcon from '@lucide/svelte/icons/bell-off';
	import CheckIcon from '@lucide/svelte/icons/check';
	import { Button } from '#lib/components/ui/button/index.js';
	import IconButton from '#lib/components/icon-button.svelte';
	import {
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuSeparator,
		DropdownMenuTrigger
	} from '#lib/components/ui/dropdown-menu/index.js';
	import { Input } from '#lib/components/ui/input/index.js';

	// `act` runs a call, reports its error and refreshes the page and sidebar.
	let { thread, act }: { thread: Thread; act: (call: Promise<unknown>) => Promise<boolean> } = $props();

	const when = (d: Date) => d.toLocaleString([], { dateStyle: 'short', timeStyle: 'short' });
	const until = $derived(thread.snoozedUntil && timestampDate(thread.snoozedUntil));
	const snoozed = $derived(isSnoozed(thread));
	let custom = $state('');
	let open = $state(false);

	const pick = (d?: Date) => {
		open = false;
		return act(snooze(thread, d));
	};
</script>

<DropdownMenu bind:open>
	<DropdownMenuTrigger>
		{#snippet child({ props })}
			<IconButton
				{...props}
				icon={BellOffIcon}
				variant={snoozed ? 'secondary' : 'ghost'}
				label={snoozed ? `Snoozed until ${when(until!)}` : 'Snooze'}
			/>
		{/snippet}
	</DropdownMenuTrigger>
	<DropdownMenuContent align="end" class="w-56">
		{#each presets as [label, date] (label)}
			<DropdownMenuItem onclick={() => pick(date())}>{label}</DropdownMenuItem>
		{/each}
		<DropdownMenuSeparator />
		<div class="flex gap-1 p-1" onkeydown={(e) => e.key !== 'Escape' && e.stopPropagation()} role="presentation">
			<Input type="datetime-local" bind:value={custom} class="h-7 min-w-0 text-xs" aria-label="Snooze until" />
			<Button size="sm" disabled={!custom} onclick={() => pick(new Date(custom))}>Set</Button>
		</div>
		{#if snoozed}
			<DropdownMenuSeparator />
			<DropdownMenuItem onclick={() => pick()}>Unsnooze (until {when(until!)})</DropdownMenuItem>
		{/if}
	</DropdownMenuContent>
</DropdownMenu>
<IconButton
	icon={CheckIcon}
	variant={thread.settled ? 'secondary' : 'ghost'}
	label={thread.settled ? 'Unsettle' : 'Settle'}
	onclick={() => act(settle(thread))}
/>
