<script lang="ts">
	import { timestampDate, timestampFromDate } from '@bufbuild/protobuf/wkt';
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
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';

	// `act` runs a call, reports its error and refreshes the page and sidebar.
	let { thread, act }: { thread: Thread; act: (call: Promise<unknown>) => Promise<boolean> } = $props();

	const at = (days: number, hour = 9) => {
		const d = new Date();
		d.setDate(d.getDate() + days);
		d.setHours(hour, 0, 0, 0);
		return d;
	};
	const later = (h: number) => new Date(Date.now() + h * 3600_000);
	const presets: [string, () => Date][] = [
		['1 hour', () => later(1)],
		['4 hours', () => later(4)],
		['Tomorrow 09:00', () => at(1)],
		['Next Monday 09:00', () => at((8 - new Date().getDay()) % 7 || 7)]
	];

	const when = (d: Date) => d.toLocaleString([], { dateStyle: 'short', timeStyle: 'short' });
	const until = $derived(thread.snoozedUntil && timestampDate(thread.snoozedUntil));
	const snoozed = $derived(!!until && until > new Date());
	let custom = $state('');
	let open = $state(false);

	const snooze = (d?: Date) => {
		open = false;
		return act(api.snooze({ threadId: thread.id, until: d && timestampFromDate(d) }));
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
			<DropdownMenuItem onclick={() => snooze(date())}>{label}</DropdownMenuItem>
		{/each}
		<DropdownMenuSeparator />
		<div class="flex gap-1 p-1" onkeydown={(e) => e.key !== 'Escape' && e.stopPropagation()} role="presentation">
			<Input type="datetime-local" bind:value={custom} class="h-7 min-w-0 text-xs" aria-label="Snooze until" />
			<Button size="sm" disabled={!custom} onclick={() => snooze(new Date(custom))}>Set</Button>
		</div>
		{#if snoozed}
			<DropdownMenuSeparator />
			<DropdownMenuItem onclick={() => snooze()}>Unsnooze (until {when(until!)})</DropdownMenuItem>
		{/if}
	</DropdownMenuContent>
</DropdownMenu>
<IconButton
	icon={CheckIcon}
	variant={thread.settled ? 'secondary' : 'ghost'}
	label={thread.settled ? 'Unsettle' : 'Settle'}
	onclick={() => act(api.settle({ threadId: thread.id, settled: !thread.settled }))}
/>
