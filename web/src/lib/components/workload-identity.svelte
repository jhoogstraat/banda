<script lang="ts">
	// Workload identity in Settings: Banda's issuer, the team's audiences
	// (none: off) and, on request, recipes for the secret managers and clouds
	// that trust it (workload-recipes.svelte, loaded only then).
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { ConnectError } from '@connectrpc/connect';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import CopyText from '#lib/components/copy-text.svelte';
	import SettingsHeading from '#lib/components/settings-heading.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { settingsApi } from '#lib/api.js';
	import type { GetWorkloadIdentityResponse } from '#lib/gen/banda/v1/banda_pb.js';
	import { userName } from '#lib/threads.svelte.js';
	import { since } from '#lib/utils.js';

	let wi = $state<GetWorkloadIdentityResponse>();
	let error = $state('');
	let audience = $state('');
	// The recipes: shown once asked for, or right after adding an audience (what comes next), for that audience.
	let showRecipes = $state(false);
	let forAudience = $state('');

	const message = (e: unknown) => ConnectError.from(e).rawMessage;
	const suggestions = ['sts.amazonaws.com', 'vault', 'kubernetes'];

	async function load() {
		try {
			wi = await settingsApi.getWorkloadIdentity({});
			error = '';
		} catch (e) {
			error = message(e);
		}
	}
	$effect(() => {
		load();
	});

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

	async function add(aud: string, e?: SubmitEvent) {
		e?.preventDefault();
		if (await act(settingsApi.addIdTokenAudience({ audience: aud.trim() }))) {
			audience = '';
			forAudience = aud.trim();
			showRecipes = true;
		}
	}

	function remove(aud: string) {
		if (confirm(`Remove the audience ${aud}? Running threads lose its token within seconds.`))
			act(settingsApi.removeIdTokenAudience({ audience: aud }));
	}

	const https = $derived(wi?.issuer.startsWith('https://') ?? true);
</script>

<section id="workload-identity" aria-label="Workload identity">
	<SettingsHeading title="Workload identity" note="short-lived tokens naming each thread’s owner, for secret managers and clouds to trust" {error} />

	{#if wi}
		<div class="space-y-4">
			{#if !https}
				<p class="flex gap-2 text-sm text-amber-700 dark:text-amber-400">
					<CircleAlertIcon class="mt-0.5 size-4 shrink-0" />
					<span>
						The issuer isn’t https: cloud providers require https and a valid certificate. Set <code class="font-mono text-xs">PUBLIC_URL</code> to
						Banda’s https address. A vault on your network can still trust its keys directly.
					</span>
				</p>
			{/if}

			<dl class="divide-y rounded-lg border text-sm">
				{#each [['Issuer', wi.issuer, 'Copy the issuer URL'], ['Public keys', wi.jwksUrl, 'Copy the public keys URL']] as [label, url, copy] (label)}
					<div class="grid gap-x-3 gap-y-0.5 px-4 py-2 sm:grid-cols-[6rem_1fr] sm:items-center">
						<dt class="text-muted-foreground">{label}</dt>
						<dd class="flex min-w-0 items-center gap-1">
							<span class="min-w-0 truncate font-mono text-xs" title={url}>{url}</span>
							<CopyText text={url} label={copy} />
						</dd>
					</div>
				{/each}
			</dl>

			<div class="divide-y rounded-lg border">
				<h4 class="flex flex-wrap items-baseline gap-x-2 px-4 py-2 text-sm font-medium">
					Audiences
					<span class="text-xs font-normal text-muted-foreground">a token for each in every running thread, renewed every few minutes</span>
				</h4>
				{#each wi.audiences as a (a.audience)}
					<div class="grid grid-cols-[1fr_auto] items-start gap-x-3 px-4 py-2">
						<div class="min-w-0">
							<p class="flex flex-wrap items-baseline gap-x-3">
								<span class="min-w-0 font-mono text-sm break-all">{a.audience}</span>
								<span class="text-xs text-muted-foreground" title={a.addedAt && timestampDate(a.addedAt).toLocaleString()}>
									added by {userName(a.addedBy)} · {since(a.addedAt)}
								</span>
							</p>
							<p class="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
								<span class="min-w-0 truncate font-mono" title={a.path}>{a.path}</span>
								<CopyText text={a.path} label="Copy the token path of {a.audience}" />
							</p>
						</div>
						<Button size="sm" variant="ghost" class="text-destructive" aria-label="Remove {a.audience}" onclick={() => remove(a.audience)}>Remove</Button>
					</div>
				{:else}
					<div class="space-y-2 px-4 py-3 text-sm">
						<p>Off: threads get no tokens until the team adds an audience, the <code class="font-mono text-xs">aud</code> value your secret manager or cloud expects.</p>
						<div class="flex flex-wrap items-center gap-1.5">
							<span class="text-muted-foreground">Common ones:</span>
							{#each suggestions as s (s)}
								<Button size="xs" variant="outline" class="font-mono" onclick={() => add(s)}>{s}</Button>
							{/each}
						</div>
					</div>
				{/each}
				<form class="flex flex-wrap gap-2 p-4" onsubmit={(e) => add(audience, e)}>
					<Input class="w-48 flex-auto font-mono" aria-label="Audience" placeholder="sts.amazonaws.com" autocomplete="off" spellcheck={false} bind:value={audience} />
					<Button type="submit" size="sm" disabled={!audience.trim()}>Add audience</Button>
				</form>
			</div>

			{#if wi.audiences.length}
				<div class="rounded-lg border">
					<button
						type="button"
						class="flex w-full items-center gap-2 rounded-lg px-4 py-3 text-left text-sm hover:bg-muted/50 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
						aria-expanded={showRecipes}
						onclick={() => (showRecipes = !showRecipes)}
					>
						<span class="min-w-0 flex-1">
							<span class="font-medium">Recipes</span>
							<span class="text-xs text-muted-foreground">to trust these tokens in OpenBao or Vault, Infisical, AWS, Google Cloud and Kubernetes</span>
						</span>
						<ChevronDownIcon class="size-4 shrink-0 text-muted-foreground transition-transform {showRecipes ? 'rotate-180' : ''}" />
					</button>
					{#if showRecipes}
						<div class="border-t">
							{#await import('#lib/components/workload-recipes.svelte')}
								<p class="p-4 text-sm text-muted-foreground">Loading…</p>
							{:then { default: Recipes }}
								<Recipes {wi} bind:audience={forAudience} />
							{/await}
						</div>
					{/if}
				</div>
			{/if}
		</div>
	{:else if !error}
		<p class="rounded-lg border p-4 text-sm text-muted-foreground">Loading…</p>
	{/if}
</section>
