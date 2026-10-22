<script lang="ts">
	// The team's secrets in Settings: like the user's own, in every thread of
	// every teammate, and anyone may replace or delete them.
	import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
	import { ConnectError } from '@connectrpc/connect';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { settingsApi } from '#lib/api.js';
	import type { SecretInfo } from '#lib/gen/banda/v1/banda_pb.js';
	import { userName } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';

	let secrets = $state<SecretInfo[]>([]);
	let error = $state('');
	// The add form, and the row being replaced.
	let name = $state('');
	let value = $state('');
	let replacing = $state('');
	let newValue = $state('');
	const validName = (n: string) => /^[A-Z_][A-Z0-9_]*$/.test(n);

	const message = (e: unknown) => ConnectError.from(e).rawMessage;

	async function load() {
		try {
			({ secrets } = await settingsApi.listTeamSecrets({}));
			error = '';
		} catch (e) {
			error = message(e);
		}
	}
	$effect(() => {
		load();
	});

	// Runs a change, then reloads; errors (also the server's validation) show above the list.
	async function act(call: Promise<unknown>) {
		try {
			await call;
		} catch (e) {
			error = message(e);
			return false;
		}
		await load();
		return true;
	}

	const since = (t?: Timestamp) => {
		const a = ago(t);
		return a === 'now' ? 'just now' : `${a} ago`;
	};

	async function add(e: SubmitEvent) {
		e.preventDefault();
		if (!validName(name)) {
			error = 'Names are environment variables: capital letters, digits and _, not starting with a digit.';
			return;
		}
		if (await act(settingsApi.setTeamSecret({ name, value }))) name = value = '';
	}

	async function replace(e: SubmitEvent) {
		e.preventDefault();
		if (await act(settingsApi.setTeamSecret({ name: replacing, value: newValue }))) replacing = newValue = '';
	}

	function remove(n: string) {
		if (confirm(`Delete the team secret ${n}? Everyone's environments started afterwards won't have it.`))
			act(settingsApi.deleteTeamSecret({ name: n }));
	}
</script>

<section aria-label="Team secrets">
	<h2 class="mb-3 flex flex-wrap items-baseline gap-x-2 text-sm font-medium">
		Team secrets
		<span class="text-xs font-normal text-muted-foreground">
			passed to every thread of every teammate; a personal secret of the same name wins
		</span>
	</h2>
	{#if error}<p class="mb-3 text-sm text-destructive">{error}</p>{/if}
	<div class="divide-y rounded-lg border">
		{#each secrets as s (s.name)}
			<div class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2">
				<span class="min-w-0 truncate font-mono text-sm">{s.name}</span>
				<span class="text-xs text-muted-foreground" title={s.updatedAt && timestampDate(s.updatedAt).toLocaleString()}>
					set by {userName(s.updatedBy)} · {since(s.updatedAt)}
				</span>
				{#if replacing === s.name}
					<form class="flex w-full gap-2" onsubmit={replace}>
						<!-- svelte-ignore a11y_autofocus -->
						<Input type="password" autocomplete="off" aria-label="New value of {s.name}" placeholder="New value" autofocus bind:value={newValue} />
						<Button type="submit" size="sm">Save</Button>
						<Button size="sm" variant="ghost" onclick={() => (replacing = newValue = '')}>Cancel</Button>
					</form>
				{:else}
					<div class="ml-auto flex gap-1">
						<Button size="sm" variant="ghost" aria-label="Replace {s.name}" onclick={() => ((replacing = s.name), (newValue = ''))}>Replace</Button>
						<Button size="sm" variant="ghost" class="text-destructive" aria-label="Delete {s.name}" onclick={() => remove(s.name)}>Delete</Button>
					</div>
				{/if}
			</div>
		{/each}
		<form class="flex flex-wrap gap-2 p-4" onsubmit={add}>
			<Input
				class="w-48 flex-auto font-mono"
				aria-label="Name"
				placeholder="NAME"
				autocomplete="off"
				aria-invalid={!!name && !validName(name)}
				bind:value={name}
			/>
			<Input class="w-48 flex-[2_1_12rem]" type="password" autocomplete="off" aria-label="Value" placeholder="value" bind:value />
			<Button type="submit" size="sm" disabled={!name || !value}>Add</Button>
		</form>
	</div>
</section>
