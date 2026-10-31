<script lang="ts">
	// The recipes of Workload identity in Settings: how a secret manager or cloud trusts Banda's tokens, filled in
	// with this Banda's values. Loaded only once someone opens them, with the issuer's public keys.
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import CopyText from '#lib/components/copy-text.svelte';
	import * as NativeSelect from '#lib/components/ui/native-select/index.js';
	import * as Tabs from '#lib/components/ui/tabs/index.js';
	import type { GetWorkloadIdentityResponse } from '#lib/gen/banda/v1/banda_pb.js';
	import { jwksToPem, marked, recipes, type Step } from '#lib/identity-recipes.js';

	let { wi, audience = $bindable() }: { wi: GetWorkloadIdentityResponse; audience: string } = $props();

	let pems = $state<string[]>([]);
	$effect(() => {
		// The public keys as PEM, for a vault that can't reach Banda. Same origin as this page, also in dev.
		fetch('/.well-known/jwks.json')
			.then((r) => r.json())
			.then(jwksToPem)
			.then((p) => (pems = p))
			.catch(() => {}); // that step is left out
	});

	let tab = $state('vault');
	let forRepo = $state<string>();
	const chosen = $derived(wi.audiences.find((a) => a.audience === audience) ?? wi.audiences[0]);
	const repo = $derived(forRepo ?? wi.repos[0] ?? '');
	const values = $derived({ issuer: wi.issuer, audience: chosen.audience, path: chosen.path, repo, email: wi.email || 'you@example.com', pems });
	const shown = $derived(recipes(values));
	const filled = $derived([values.issuer, values.audience, values.path, values.repo, wi.email, `user:${values.email}`]);
	const r = $derived(shown.find((x) => x.id === tab) ?? shown[0]);
</script>

{#snippet step(s: Step, title: boolean)}
	<div class="space-y-1.5">
		{#if title}<h5 class="font-medium">{s.title}</h5>{/if}
		{#if s.note}<p class="max-w-prose text-xs text-muted-foreground">{s.note}</p>{/if}
		{#if s.code}
			<div class="min-w-0 rounded-md border bg-muted/40">
				<div class="flex items-center justify-between border-b py-0.5 pr-1 pl-3 font-mono text-[11px] text-muted-foreground">
					{s.lang ?? ''}
					<CopyText text={s.code} label="Copy: {s.title}" />
				</div>
				<pre class="overflow-x-auto p-3 font-mono text-xs leading-relaxed"><code
						>{#each marked(s.code, filled) as m, i (i)}{#if m.filled}<span
								class="rounded-[3px] bg-info/10 text-info-foreground ring-1 ring-info/20">{m.text}</span
							>{:else}{m.text}{/if}{/each}</code
					></pre>
			</div>
		{/if}
	</div>
{/snippet}

<Tabs.Root bind:value={tab} class="gap-0">
	<div class="space-y-2 border-b px-4 py-3 text-sm">
		<Tabs.List class="max-w-full flex-wrap justify-start group-data-horizontal/tabs:h-auto">
			{#each shown as x (x.id)}<Tabs.Trigger value={x.id} class="h-7 flex-none px-2">{x.label}</Tabs.Trigger>{/each}
		</Tabs.List>
		<div class="flex flex-wrap gap-x-6 gap-y-2">
			{#if wi.audiences.length > 1}
				<label class="flex items-center gap-2 text-muted-foreground">
					Audience
					<NativeSelect.Root size="sm" class="font-mono" value={values.audience} onchange={(e) => (audience = e.currentTarget.value)}>
						{#each wi.audiences as a (a.audience)}<NativeSelect.Option value={a.audience}>{a.audience}</NativeSelect.Option>{/each}
					</NativeSelect.Root>
				</label>
			{/if}
			<label class="flex min-w-0 items-center gap-2 text-muted-foreground">
				Only threads on
				<NativeSelect.Root size="sm" class="max-w-56 font-mono" value={repo} onchange={(e) => (forRepo = e.currentTarget.value)}>
					{#each wi.repos as x (x)}<NativeSelect.Option value={x}>{x}</NativeSelect.Option>{/each}
					<NativeSelect.Option value="">any repository</NativeSelect.Option>
				</NativeSelect.Root>
			</label>
		</div>
		<p class="max-w-prose text-xs text-muted-foreground">
			{#if repo}Only threads on <span class="font-mono break-all text-foreground">{repo}</span>, the most recently used repository; pick another
				or any.{:else}Threads on any repository.{/if}
			Every recipe binds the person too: a thread’s repository is only what it was created with, and anyone can name any repository, so it
			narrows access and never grants it.
		</p>
	</div>
	<Tabs.Content value={r.id} class="min-w-0 space-y-5 p-4">
		<p class="max-w-prose text-muted-foreground">{r.intro}</p>
		{#if !wi.email}
			<p class="text-warning-foreground">You signed in without an email, so tokens can’t name you: the recipes show you@example.com.</p>
		{/if}
		{#each r.steps as s (s.title)}
			{#if s.optional}
				<details class="group/step">
					<summary
						class="flex w-fit cursor-pointer list-none items-center gap-1.5 rounded-sm font-medium text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden"
					>
						<ChevronRightIcon class="size-4 transition-transform group-open/step:rotate-90" />{s.title}
					</summary>
					<div class="mt-1.5 pl-5">{@render step(s, false)}</div>
				</details>
			{:else}
				<div>{@render step(s, true)}</div>
			{/if}
		{/each}
		<p class="text-xs text-muted-foreground">
			<span class="rounded-[3px] bg-info/10 px-1 text-info-foreground ring-1 ring-info/20">Highlighted</span> values are this Banda’s
			own; the rest is yours to adjust.
		</p>
	</Tabs.Content>
</Tabs.Root>
